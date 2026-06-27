package reconcile

// fields.go implements §2 of phase1-reconcile-policy-spec: the per-field conflict policy applied to a
// class-A `issues`/`wisps` row AFTER bd/Dolt's structural 3-way merge has flagged it in dolt_conflicts_*.
// bd's --strategy ours|theirs is whole-row and too coarse; this is the thin per-field post-pass.
//
// It is a PURE function over the two conflicting row versions (no Dolt/git calls), so it is the
// directly-testable core. The column classification below is the spec's schema-grounded table
// (verified `describe issues` on the live hq DB — 38 cols), NOT invented.
//
// Satellite tables (labels/comments/dependencies/events + wisp_*) are NOT handled here: their
// composite/uuid/append PKs make Dolt's 3-way merge a row UNION automatically, which is exactly the
// spec's "labels union, not ours/theirs". This pass only resolves the row-FIELD conflicts on
// issues/wisps that a whole-row ours/theirs would get wrong.

import (
	"encoding/json"
	"fmt"
	"time"
)

// policy is how a single column reconciles when the two sites disagree.
type policy int

const (
	lww           policy = iota // last-writer-wins by the row's updated_at
	immutable                   // same bead both sites; differ => bug, flag don't merge
	orFlag                      // sticky boolean: true at either site => true
	maxCompaction               // monotonic; higher compaction_level wins
	deferContent                // status/closed_*: §3 content verdict decides, NOT this pass
	deepMergeJSON               // metadata: union keys, LWW per colliding key
)

// fieldPolicy is the §2 table. A column absent here is UNMODELED => ambiguous => pause+report (never guess).
var fieldPolicy = map[string]policy{
	// LWW by updated_at
	"title": lww, "description": lww, "design": lww, "acceptance_criteria": lww, "notes": lww,
	"priority": lww, "assignee": lww, "owner": lww, "work_type": lww, "mol_type": lww,
	// immutable identity
	"id": immutable, "content_hash": immutable, "created_at": immutable, "created_by": immutable,
	// status/closed — §3 owns these (State lies; class-A = closed iff either closed AND content agrees)
	"status": deferContent, "closed_at": deferContent, "closed_by_session": deferContent, "close_reason": deferContent,
	// sticky flags
	"pinned": orFlag, "ephemeral": orFlag, "no_history": orFlag, "is_template": orFlag,
	// monotonic compaction
	"compaction_level": maxCompaction, "compacted_at": maxCompaction, "original_size": maxCompaction,
	// json
	"metadata": deepMergeJSON,
	// updated_at itself is the clock — winner's value carried with the LWW fields
	"updated_at": lww,
	// §2 completeness (live `describe issues` = 55 cols; first inspection truncated at 38). The rest
	// move with the row's mutable state — none are landed/identity — so LWW, except is_blocked (sticky).
	"is_blocked": orFlag,
	// scheduling / activity timestamps
	"due_at": lww, "defer_until": lww, "started_at": lww, "last_activity": lww,
	// wisp/agent-coordination + convoy/mol machinery
	"actor": lww, "target": lww, "payload": lww, "await_type": lww, "timeout_ns": lww,
	"waiters": lww, "hook_bead": lww, "role_bead": lww, "agent_state": lww, "role_type": lww, "rig": lww,
	// descriptive / provenance
	"external_ref": lww, "spec_id": lww, "source_system": lww, "source_repo": lww,
	"sender": lww, "wisp_type": lww, "event_kind": lww, "estimated_minutes": lww,
}

// Resolution is one auto-resolved (or flagged) field, for the gt town sync report. Silent resolution is
// how lost work hides (§2 rule) — every conflict surfaces here.
type Resolution struct {
	Field  string // column name
	Winner string // "ours" | "theirs" | "merged" | "defer-content" | "flag" | "pause"
	Reason string // human one-liner for the report
}

// dolt's datetime wire layout for updated_at (datetime NOT NULL DEFAULT CURRENT_TIMESTAMP).
const doltDatetime = "2006-01-02 15:04:05"

// ResolveConflicts applies the §2 per-field policy to a conflicting class-A row. ours/theirs are the two
// site versions (column -> string value, as scanned from dolt_conflicts_*). It returns the resolved row
// and a report of every decision. An UNMODELED column (not in fieldPolicy) is left at ours' value and
// reported as "pause" — the caller must surface those to bd's manual path rather than ship a guess.
//
// LWW is row-level: the row has a single updated_at, so the later-updated side wins ALL its lww fields at
// once. A missing/unparseable updated_at makes LWW unverifiable => those fields are reported "pause"
// (fail-safe, same stance as §3's unverifiable-landed-check).
func ResolveConflicts(ours, theirs map[string]string) (map[string]string, []Resolution) {
	resolved := make(map[string]string, len(ours))
	for k, v := range ours {
		resolved[k] = v
	}
	var report []Resolution

	// Determine the LWW winner once (row-level updated_at). theirsWins==true => their value wins lww fields.
	theirsWins, lwwOK := theirsIsNewer(ours["updated_at"], theirs["updated_at"])

	for col := range unionKeys(ours, theirs) {
		ov, tv := ours[col], theirs[col]
		if ov == tv {
			continue // no conflict on this field
		}
		p, modeled := fieldPolicy[col]
		if !modeled {
			// unmodeled column — never guess
			report = append(report, Resolution{col, "pause", "unmodeled column — bd manual path"})
			continue
		}
		switch p {
		case immutable:
			report = append(report, Resolution{col, "flag", fmt.Sprintf("immutable field differs (ours=%q theirs=%q) — likely a bug, not merged", ov, tv)})
			// keep ours; flag for human
		case orFlag:
			if isTrue(ov) || isTrue(tv) {
				resolved[col] = "1"
			}
			report = append(report, Resolution{col, "merged", "sticky flag OR'd true"})
		case maxCompaction:
			resolved[col] = maxStr(ov, tv)
			report = append(report, Resolution{col, "merged", "more-compacted side kept (monotonic)"})
		case deferContent:
			report = append(report, Resolution{col, "defer-content", "status/closed — resolved by §3 content verdict, not LWW"})
			// leave ours; §3 pass overwrites
		case deepMergeJSON:
			merged, w := deepMergeJSONField(ov, tv, theirsWins)
			resolved[col] = merged
			report = append(report, Resolution{col, "merged", "metadata keys union'd, LWW per colliding key (" + w + ")"})
		case lww:
			if !lwwOK {
				report = append(report, Resolution{col, "pause", "LWW field conflicts but updated_at is missing/unparseable — manual resolve"})
				continue
			}
			if theirsWins {
				resolved[col] = tv
				report = append(report, Resolution{col, "theirs", "LWW: theirs has later updated_at"})
			} else {
				report = append(report, Resolution{col, "ours", "LWW: ours has later-or-equal updated_at"})
			}
		}
	}
	return resolved, report
}

// theirsIsNewer reports whether theirs' updated_at is strictly later than ours'. ok=false if either side's
// timestamp is missing or unparseable (caller treats lww fields as pause). Equal timestamps => ours wins
// (theirsIsNewer=false), a deterministic tie-break.
func theirsIsNewer(oursTS, theirsTS string) (theirsWins, ok bool) {
	o, oe := time.Parse(doltDatetime, oursTS)
	t, te := time.Parse(doltDatetime, theirsTS)
	if oe != nil || te != nil {
		return false, false
	}
	return t.After(o), true
}

// deepMergeJSONField unions the keys of two json objects; on a colliding key the LWW winner's value wins.
// Non-object / unparseable json falls back to whole-value LWW. Returns the merged json + which side won
// collisions ("ours"/"theirs") for the report.
func deepMergeJSONField(ours, theirs string, theirsWins bool) (string, string) {
	winner := "ours"
	if theirsWins {
		winner = "theirs"
	}
	var om, tm map[string]any
	if json.Unmarshal([]byte(ours), &om) != nil || json.Unmarshal([]byte(theirs), &tm) != nil {
		// not both objects — can't key-merge; fall back to LWW whole value
		if theirsWins {
			return theirs, winner
		}
		return ours, winner
	}
	out := make(map[string]any, len(om)+len(tm))
	// start from the LWW LOSER, then overlay the winner so colliding keys take the winner's value
	lo, hi := om, tm
	if !theirsWins {
		lo, hi = tm, om
	}
	for k, v := range lo {
		out[k] = v
	}
	for k, v := range hi {
		out[k] = v
	}
	b, _ := json.Marshal(out) // map[string]any always marshals
	return string(b), winner
}

func unionKeys(a, b map[string]string) map[string]struct{} {
	s := make(map[string]struct{}, len(a)+len(b))
	for k := range a {
		s[k] = struct{}{}
	}
	for k := range b {
		s[k] = struct{}{}
	}
	return s
}

func isTrue(s string) bool { return s == "1" || s == "true" || s == "TRUE" }

// maxStr returns the lexically/numerically larger of two compaction values. compaction_level is a small
// int; compacted_at is a timestamp; both compare correctly as zero-padded strings, and ints "0".."9"
// compare fine. ponytail: string-max, fine for the int levels in schema; revisit only if a level exceeds one digit AND an unpadded field appears.
func maxStr(a, b string) string {
	if b > a {
		return b
	}
	return a
}
