// Package mayorlease owns the multi-client Mayor lease: the single source of
// truth for "who is the Mayor right now" and the fencing epoch that gates
// Mayor-authoritative writes across a cluster of clients attached to one hub Dolt.
//
// Design: ../../../multi-client-mayor-lease-design.md (distcompute_researcher).
// The split-brain-safe core is a conditional UPDATE (atomic compare-and-set) on a
// single mayor_lease row; distcompute VERIFIED exactly-one-winner under 2-way and
// 8-way concurrent races against a real dolt sql-server. This package is the gt-side
// home for that primitive.
//
// Lane split (gastown_eng_lead):
//   - This package + the election retrofit (manager.go:Start, lifecycle.go) — gastown_eng_a.
//   - The write-fencing wrapper (withMayorEpoch over beads.go writes) — gastown_eng_lead,
//     which imports ReadEpochForUpdate from here so the epoch comparator is the hub row
//     read IN the enforcement txn (never a cached value). ONE epoch definition, one place.
//
// Connection: reuses doltserver.DefaultConfig(townRoot), which reads GT_DOLT_HOST and
// produces a hub-targeting DSN when a client is attached to a remote hub — so the lease
// follows the cluster hub automatically, no new transport.
//
// NOTE (not compile-verified on this host): macs can't build the fork (no
// proxy.golang.org); build/test on the node per the team recipe before landing. Not yet
// wired into manager.go — that waits on the atomicity-review PASS (owner directive).
package mayorlease

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"github.com/steveyegge/gastown/internal/doltserver"
)

// HubDatabase is the town-level Dolt database that holds the lease tables.
// "hq" is the town-level HQ database (doltserver/dolthub.go:38 maps it to "gt-hq").
const HubDatabase = "gt-hq"

// Locked operational values (design doc §heartbeat, coupled with offload_ops' tunnel
// tuning). Heartbeat << staleness so a brief tunnel blip or laptop-wake reconnect
// survives without a spurious handoff, but a real laptop-sleep > staleness reads as a
// stale lease and hands off cleanly. These are defaults; callers may override via config.
const (
	// HeartbeatInterval is how often the Mayor renews the lease.
	HeartbeatInterval = 30 * time.Second
	// StalenessThreshold is the age past which a lease is handoff-eligible (6 missed beats).
	StalenessThreshold = 3 * time.Minute
	// SelfDemoteThreshold: the Mayor proactively demotes itself once its own renew has
	// been failing for (staleness - 1 beat), rather than waiting to be CAS-evicted, so a
	// stale Mayor never believes it is authoritative while its tunnel is dead.
	SelfDemoteThreshold = StalenessThreshold - HeartbeatInterval
)

// ErrFenced is returned when an epoch comparison shows the caller has been fenced
// (a handoff happened): the caller must self-demote to Vice and abort the in-flight
// gated action. Same demotion signal class as a renew returning affected_rows=0.
var ErrFenced = fmt.Errorf("mayorlease: epoch fenced (lease handed off)")

// Lease is a handle to one town's Mayor lease in the hub Dolt. Construct with Open.
type Lease struct {
	db   *sql.DB
	town string
	ttlS int // staleness threshold in seconds, for the SQL guard
}

// Open connects to the hub Dolt (following GT_DOLT_HOST) and returns a Lease handle
// for the given town. The caller owns Close.
func Open(townRoot, town string) (*Lease, error) {
	cfg := doltserver.DefaultConfig(townRoot)
	// TCP to the hub when remote; the existing buildDoltDSN socket-first optimization is
	// for local short-lived CLI calls — the lease is long-lived and (when attached) remote,
	// so we target HostPort directly. user@tcp(host:port)/db.
	dsn := fmt.Sprintf("%s@tcp(%s)/%s?parseTime=true&timeout=5s&readTimeout=30s&writeTimeout=30s",
		dsnUser(cfg), cfg.HostPort(), HubDatabase)
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("mayorlease: open hub dolt: %w", err)
	}
	return &Lease{db: db, town: town, ttlS: int(StalenessThreshold.Seconds())}, nil
}

func dsnUser(cfg *doltserver.Config) string {
	if cfg.Password != "" {
		return cfg.User + ":" + cfg.Password
	}
	return cfg.User
}

// Close releases the underlying connection pool.
func (l *Lease) Close() error { return l.db.Close() }

// Acquire attempts to win (or already hold) the lease via the verified atomic CAS.
// Returns (epoch, true) if this client is now MAYOR — epoch is the fencing token to
// stamp on Mayor-gated writes. Returns (_, false) if another live client holds it
// (this client is VICE). pinned requests permanent-precedence preemption.
//
// The CAS (design doc §acquire): UPDATE ... WHERE holder IS NULL OR stale OR (pinned and
// not me). affected_rows=1 => won. Row-level isolation makes concurrent races
// exactly-one-winner (distcompute verified 2-way + 8-way). The single-row CAS is the
// hard split-brain safety; longest-connection ordering is a preference layered on top
// (see IsLongestConnectedLive), NEVER the safety guard.
func (l *Lease) Acquire(ctx context.Context, clientID string, pinned bool) (epoch int64, won bool, err error) {
	const q = `
UPDATE mayor_lease
   SET holder=?, holder_since=NOW(), last_heartbeat=NOW(), epoch=epoch+1
 WHERE town=?
   AND ( holder IS NULL
         OR last_heartbeat < NOW() - INTERVAL ? SECOND
         OR (? AND holder <> ?) )`
	res, err := l.db.ExecContext(ctx, q, clientID, l.town, l.ttlS, pinned, clientID)
	if err != nil {
		return 0, false, fmt.Errorf("mayorlease: acquire CAS: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, false, fmt.Errorf("mayorlease: acquire rows-affected: %w", err)
	}
	if n == 0 {
		return 0, false, nil // someone else holds it -> we are Vice
	}
	// We won: read back our epoch (the row we just stamped) for the fencing token.
	holder, ep, rerr := l.ReadLease(ctx)
	if rerr != nil {
		return 0, false, rerr
	}
	if holder != clientID {
		// Raced and lost between UPDATE and read-back (should not happen under row-lock,
		// but never claim Mayor on a row we don't hold).
		return 0, false, nil
	}
	return ep, true, nil
}

// Renew refreshes the heartbeat IFF this client still holds the lease (scoped to
// holder=:me so a demoted ex-Mayor can't renew). Returns stillHeld=false when
// affected_rows=0 — THE load-bearing demotion signal: the caller MUST self-demote to
// Vice and abort any in-flight gated action. Ignoring this 0 is a silent fence-bypass.
func (l *Lease) Renew(ctx context.Context, clientID string) (stillHeld bool, err error) {
	const q = `UPDATE mayor_lease SET last_heartbeat=NOW() WHERE town=? AND holder=?`
	res, err := l.db.ExecContext(ctx, q, l.town, clientID)
	if err != nil {
		return false, fmt.Errorf("mayorlease: renew: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("mayorlease: renew rows-affected: %w", err)
	}
	return n == 1, nil
}

// Release performs a clean handoff: clears the holder IFF we hold it, so the lease is
// instantly acquirable by the next-longest (or pinned) client. A crash skips this and
// the lease goes stale after StalenessThreshold instead — either way the window is
// no-Mayor, never two-Mayor (the safety asymmetry).
func (l *Lease) Release(ctx context.Context, clientID string) error {
	const q = `UPDATE mayor_lease SET holder=NULL WHERE town=? AND holder=?`
	if _, err := l.db.ExecContext(ctx, q, l.town, clientID); err != nil {
		return fmt.Errorf("mayorlease: release: %w", err)
	}
	return nil
}

// ReadLease returns the current (holder, epoch) for cheap UI/state and approval routing
// ("show Mayor/Vice", "am I Mayor?"). This is the read-contract seam (design doc
// §lease-READ-contract). It is NOT the enforcement path: a bare check-then-act is
// split-brain-prone, so the authoritative answer for a privileged WRITE is the
// epoch-conditional write itself (ReadEpochForUpdate, in the write's own txn).
func (l *Lease) ReadLease(ctx context.Context) (holder string, epoch int64, err error) {
	const q = `SELECT holder, epoch FROM mayor_lease WHERE town=?`
	var h sql.NullString
	row := l.db.QueryRowContext(ctx, q, l.town)
	if err := row.Scan(&h, &epoch); err != nil {
		if err == sql.ErrNoRows {
			return "", 0, nil // no lease row yet => unheld
		}
		return "", 0, fmt.Errorf("mayorlease: read lease: %w", err)
	}
	return h.String, epoch, nil
}

// AmIMayor reports whether clientID currently holds the lease. Convenience over
// ReadLease for display and the (B) Vice approval-routing gate (gateOrRoute). gt has
// NO IsMayor primitive today; this defines it. Cheap-read only — privileged writes must
// still fence via the epoch-at-commit path, not this check.
func (l *Lease) AmIMayor(ctx context.Context, clientID string) (bool, error) {
	holder, _, err := l.ReadLease(ctx)
	if err != nil {
		return false, err
	}
	return holder != "" && holder == clientID, nil
}

// ReadEpochForUpdate reads the lease epoch inside the caller's transaction with a row
// lock (SELECT ... FOR UPDATE), so the comparator is the authoritative hub row held
// stable across the gated write's lifetime — the comparator-freshness guarantee
// (design doc §FENCING). This is the seam gastown_eng_lead's write-fencing imports.
//
// PENDING the shellout-fork resolution (gated-action-list §1, raised to distcompute):
// the 5 beads.go write methods DEFAULT to a `bd` CLI shellout, which a Go *sql.Tx
// cannot wrap. Whether enforcement is a Go-txn (force the in-process store path) or an
// epoch-arg threaded to bd/Dolt decides this signature. Left unimplemented until that
// fork is chosen so we pin ONE signature, not guess. The election lane (Acquire/Renew/
// Release) is unaffected — those are direct SQL through this package.
func ReadEpochForUpdate(ctx context.Context, tx *sql.Tx, town string) (epoch int64, err error) {
	const q = `SELECT epoch FROM mayor_lease WHERE town=? FOR UPDATE`
	if err := tx.QueryRowContext(ctx, q, town).Scan(&epoch); err != nil {
		return 0, fmt.Errorf("mayorlease: read epoch for update: %w", err)
	}
	return epoch, nil
}
