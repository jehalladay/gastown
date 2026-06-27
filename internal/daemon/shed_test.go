package daemon

import (
	"io"
	"log"
	"testing"
)

// TestSessionWorktreeCleanByName_FailSafe verifies the kill-time re-assert is
// fail-safe: an unparseable session name, or a crew whose worktree path doesn't
// exist, resolves to NOT clean (false) → the kill is skipped, work preserved.
// This is the TOCTOU guard merge_warden required (re-check immediately before
// each KillSession, since staleness accumulates across the loop).
func TestSessionWorktreeCleanByName_FailSafe(t *testing.T) {
	d := &Daemon{
		config: &Config{TownRoot: t.TempDir()},
		logger: log.New(io.Discard, "", 0),
	}
	cases := []struct {
		name    string
		session string
	}{
		{"unparseable session", "not-a-valid-identity"},
		{"crew with no worktree on disk", "somerig-crew-ghost"},
		{"town-level role has no rig worktree", "hq-mayor"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if d.sessionWorktreeCleanByName(tc.session) {
				t.Errorf("expected false (keep alive) for %q, got true", tc.session)
			}
		})
	}
}

// shedCandidate mirrors the fields selectShedVictims needs. Kept local to the
// test to document the contract the production type must satisfy.
// CleanWorktree defaults to true so these cases exercise the OTHER gates
// (role/idle/beads); the worktree gate has its own dedicated tests below.
func cand(name, role string, idle bool, activeBeads int) ShedCandidate {
	return ShedCandidate{Session: name, Role: role, Idle: idle, ActiveBeads: activeBeads, CleanWorktree: true}
}

func TestSelectShedVictims_NeverShedsDataPlaneOrInfra(t *testing.T) {
	// Even when idle + zero beads, infra/data-plane roles are NEVER victims.
	in := []ShedCandidate{
		cand("hq-mayor", "mayor", true, 0),
		cand("rig-witness", "witness", true, 0),
		cand("rig-refinery", "refinery", true, 0),
		cand("hq-deacon", "deacon", true, 0),
		cand("hq-boot", "boot", true, 0),
		cand("rig-dog-fido", "dog", true, 0),
	}
	got := selectShedVictims(in, 10) // want many; should still pick none
	if len(got) != 0 {
		t.Fatalf("expected 0 victims (all infra/data-plane), got %v", got)
	}
}

func TestSelectShedVictims_NeverShedsActive(t *testing.T) {
	// A crew with an active bead, or busy (not idle), is never a victim.
	in := []ShedCandidate{
		cand("rig-polecat-busy", "polecat", false, 0), // busy at prompt
		cand("rig-crew-working", "crew", true, 2),     // idle-at-prompt but has 2 active beads
	}
	got := selectShedVictims(in, 10)
	if len(got) != 0 {
		t.Fatalf("expected 0 victims (none idle+0-bead), got %v", got)
	}
}

func TestSelectShedVictims_ShedsOnlyIdleZeroBeadCrewPolecats(t *testing.T) {
	in := []ShedCandidate{
		cand("hq-mayor", "mayor", true, 0),           // keep (infra)
		cand("rig-polecat-idle", "polecat", true, 0), // SHED
		cand("rig-crew-idle", "crew", true, 0),       // SHED
		cand("rig-crew-busy", "crew", false, 0),      // keep (busy)
		cand("rig-crew-working", "crew", true, 1),    // keep (has bead)
	}
	got := selectShedVictims(in, 10)
	if len(got) != 2 {
		t.Fatalf("expected 2 victims, got %d: %v", len(got), got)
	}
	for _, v := range got {
		if v != "rig-polecat-idle" && v != "rig-crew-idle" {
			t.Errorf("unexpected victim %q", v)
		}
	}
}

func TestSelectShedVictims_NeverShedsDirtyWorktree(t *testing.T) {
	// The data-loss gate: an idle, zero-bead crew with UNCOMMITTED work in its
	// worktree must NOT be shed — park=KillSession would orphan that work.
	// Committed work is always safe (worktree persists on disk); uncommitted is
	// the only loss vector, so a dirty worktree keeps the session alive.
	in := []ShedCandidate{
		{Session: "rig-crew-dirty", Role: "crew", Idle: true, ActiveBeads: 0, CleanWorktree: false},
		{Session: "rig-polecat-dirty", Role: "polecat", Idle: true, ActiveBeads: 0, CleanWorktree: false},
	}
	if got := selectShedVictims(in, 10); len(got) != 0 {
		t.Fatalf("expected 0 victims (dirty worktrees), got %v", got)
	}
}

func TestSelectShedVictims_ShedsCleanIdleZeroBeadCrew(t *testing.T) {
	// The safe-by-construction case: clean + idle + 0-bead + crew/polecat is the
	// ONLY thing auto-shed may kill. Worst case = killing a fully-recoverable
	// idle crew with nothing unsaved.
	in := []ShedCandidate{
		{Session: "rig-crew-clean", Role: "crew", Idle: true, ActiveBeads: 0, CleanWorktree: true},
		{Session: "rig-crew-dirty", Role: "crew", Idle: true, ActiveBeads: 0, CleanWorktree: false}, // keep
	}
	got := selectShedVictims(in, 10)
	if len(got) != 1 || got[0] != "rig-crew-clean" {
		t.Fatalf("expected only rig-crew-clean, got %v", got)
	}
}

func TestSelectShedVictims_RespectsMaxToShed(t *testing.T) {
	in := []ShedCandidate{
		cand("rig-crew-a", "crew", true, 0),
		cand("rig-crew-b", "crew", true, 0),
		cand("rig-crew-c", "crew", true, 0),
	}
	got := selectShedVictims(in, 2)
	if len(got) != 2 {
		t.Fatalf("maxToShed=2 should cap at 2, got %d: %v", len(got), got)
	}
}

func TestSelectShedVictims_ZeroMaxShedsNothing(t *testing.T) {
	in := []ShedCandidate{cand("rig-crew-a", "crew", true, 0)}
	if got := selectShedVictims(in, 0); len(got) != 0 {
		t.Fatalf("maxToShed=0 must shed nothing, got %v", got)
	}
}

func TestSwapUsedPercent_DoesNotPanicAndIsRanged(t *testing.T) {
	// Whatever the host reports, the value must be a valid percent or the
	// sentinel -1 (unavailable). Never a wild number that would false-trip.
	p := swapUsedPercent()
	if p != -1 && (p < 0 || p > 100) {
		t.Fatalf("swapUsedPercent must be -1 (unavailable) or 0..100, got %f", p)
	}
}

func TestParseSwapUsage_Darwin(t *testing.T) {
	// Real macOS vm.swapusage format.
	line := "total = 7168.00M  used = 5694.81M  free = 1473.19M  (encrypted)"
	pct, ok := parseSwapUsagePercent(line)
	if !ok {
		t.Fatal("expected parse ok")
	}
	want := 5694.81 / 7168.00 * 100
	if pct < want-0.01 || pct > want+0.01 {
		t.Fatalf("pct = %f, want ~%f", pct, want)
	}
}

func TestParseSwapUsage_ZeroTotalIsUnavailable(t *testing.T) {
	// No swap configured: total=0 must NOT divide-by-zero or report 100%.
	if _, ok := parseSwapUsagePercent("total = 0.00M  used = 0.00M  free = 0.00M"); ok {
		t.Fatal("total=0 should report not-ok (unavailable), not a percent")
	}
}
