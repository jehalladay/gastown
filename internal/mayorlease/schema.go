package mayorlease

import (
	"context"
	"fmt"
)

// Schema is the DDL for the lease tables, one row per town in the hub Dolt (gt-hq).
// Idempotent (IF NOT EXISTS) so EnsureSchema is safe to call on every client attach.
// Layout per design doc §A.
const Schema = `
CREATE TABLE IF NOT EXISTS mayor_lease (
  town            varchar(64)  PRIMARY KEY,
  holder          varchar(128),                 -- client id of the active MAYOR (NULL = unheld)
  holder_since    datetime,                     -- when the current holder acquired (audit)
  last_heartbeat  datetime,                     -- renewed every heartbeat; staleness => handoff-eligible
  pinned_client   varchar(128),                 -- permanent-precedence override (NULL = none)
  epoch           bigint NOT NULL DEFAULT 0     -- fencing token; +1 on every acquisition
);
CREATE TABLE IF NOT EXISTS mayor_clients (
  client_id       varchar(128),
  town            varchar(64),
  connected_at    datetime,                     -- "longest-connection-time" evidence for election
  last_heartbeat  datetime,
  is_pinned       boolean NOT NULL DEFAULT false,
  PRIMARY KEY (town, client_id)
);`

// EnsureSchema creates the lease tables if absent and ensures a lease row exists for
// the town (unheld). Safe to call on every attach.
func (l *Lease) EnsureSchema(ctx context.Context) error {
	for _, stmt := range splitDDL(Schema) {
		if _, err := l.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("mayorlease: ensure schema: %w", err)
		}
	}
	// Seed an unheld lease row for this town (no-op if present).
	const seed = `INSERT IGNORE INTO mayor_lease (town, epoch) VALUES (?, 0)`
	if _, err := l.db.ExecContext(ctx, seed, l.town); err != nil {
		return fmt.Errorf("mayorlease: seed lease row: %w", err)
	}
	return nil
}

// RegisterClient records this client's connection start (for longest-connection
// election) and refreshes its own client heartbeat. is_pinned marks the
// permanent-precedence client.
func (l *Lease) RegisterClient(ctx context.Context, clientID string, pinned bool) error {
	const q = `
INSERT INTO mayor_clients (client_id, town, connected_at, last_heartbeat, is_pinned)
VALUES (?, ?, NOW(), NOW(), ?)
ON DUPLICATE KEY UPDATE last_heartbeat=NOW(), is_pinned=VALUES(is_pinned)`
	if _, err := l.db.ExecContext(ctx, q, clientID, l.town, pinned); err != nil {
		return fmt.Errorf("mayorlease: register client: %w", err)
	}
	return nil
}

// IsLongestConnectedLive reports whether clientID is the longest-connected live client
// (no other live client has an earlier connected_at), with ties broken by client_id.
// This is the election PREFERENCE, layered on top of the CAS — it decides WHO among
// eligible clients should win, NOT the split-brain safety (that is the single-row CAS
// in Acquire). Worst case if mayor_clients liveness is racy: a not-longest client wins
// election — NEVER two Mayors. (design doc §acquire/elect, merge_warden note.)
func (l *Lease) IsLongestConnectedLive(ctx context.Context, clientID string) (bool, error) {
	const q = `
SELECT COUNT(*) FROM mayor_clients
 WHERE town=?
   AND last_heartbeat >= NOW() - INTERVAL ? SECOND
   AND ( connected_at < (SELECT connected_at FROM mayor_clients WHERE town=? AND client_id=?)
         OR ( connected_at = (SELECT connected_at FROM mayor_clients WHERE town=? AND client_id=?)
              AND client_id < ? ) )`
	var ahead int
	row := l.db.QueryRowContext(ctx, q, l.town, l.ttlS, l.town, clientID, l.town, clientID, clientID)
	if err := row.Scan(&ahead); err != nil {
		return false, fmt.Errorf("mayorlease: longest-connected check: %w", err)
	}
	return ahead == 0, nil
}

// splitDDL splits the multi-statement Schema into individual statements (the mysql
// driver executes one statement per Exec unless multiStatements is set; we keep the
// DSN simple and split here).
func splitDDL(ddl string) []string {
	var out []string
	cur := ""
	for _, r := range ddl {
		cur += string(r)
		if r == ';' {
			if s := trimStmt(cur); s != "" {
				out = append(out, s)
			}
			cur = ""
		}
	}
	if s := trimStmt(cur); s != "" {
		out = append(out, s)
	}
	return out
}

func trimStmt(s string) string {
	// strip leading/trailing whitespace and a trailing semicolon-only fragment
	start, end := 0, len(s)
	for start < end && (s[start] == ' ' || s[start] == '\n' || s[start] == '\t' || s[start] == '\r') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\n' || s[end-1] == '\t' || s[end-1] == '\r' || s[end-1] == ';') {
		end--
	}
	return s[start:end]
}
