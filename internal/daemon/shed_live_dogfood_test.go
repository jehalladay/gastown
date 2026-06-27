package daemon

// F10 Phase-2 DOGFOOD: live exercise of the auto-shed CleanWorktree gate.
//
// The shipped shed_test.go proves the PURE selection logic with hand-set
// CleanWorktree flags. This dogfood test exercises the parts those mocks skip:
//
//   1. REAL git worktree clean/dirty discrimination through the production
//      sessionWorktreeClean / sessionWorktreeCleanByName (real `git status`).
//   2. selectShedVictims fed by those REAL-git flags end-to-end: a clean idle
//      0-bead crew is picked; a dirty one (uncommitted file) is skipped.
//   3. A REAL tmux KillSession on an ISOLATED socket: the clean victim's
//      session dies, the dirty one survives — never touching the real town.
//
// Safety: everything runs under a t.TempDir() town root and a per-test tmux
// socket (NewTmuxWithSocket). No real crew/infra session is ever enumerated or
// killed. checkSwapShed itself is NOT invoked (it reads live vm.swapusage and
// the default socket); this faithfully reproduces its post-gate loop body.

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/steveyegge/gastown/internal/tmux"
)

// makeWorktree creates a real git repo at {town}/{rig}/{sub}/{name} with one
// commit, then optionally dirties it with an uncommitted file. Returns the path.
func makeWorktree(t *testing.T, town, rig, sub, name string, dirty bool) string {
	t.Helper()
	wt := filepath.Join(town, rig, sub, name)
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = wt
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, wt, err, out)
		}
	}
	if err := exec.Command("mkdir", "-p", wt).Run(); err != nil {
		t.Fatalf("mkdir %s: %v", wt, err)
	}
	run("init", "-q")
	run("config", "user.email", "dogfood@test")
	run("config", "user.name", "dogfood")
	if err := exec.Command("sh", "-c", "echo seed > "+filepath.Join(wt, "seed.txt")).Run(); err != nil {
		t.Fatalf("seed: %v", err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "seed")
	if dirty {
		// Leave an uncommitted file — the sole data-loss vector park must protect.
		if err := exec.Command("sh", "-c", "echo WIP > "+filepath.Join(wt, "uncommitted.txt")).Run(); err != nil {
			t.Fatalf("dirty: %v", err)
		}
	}
	return wt
}

// TestF10Phase2_LiveCleanWorktreeGate exercises the real git discrimination and
// a real tmux kill, mirroring checkSwapShed's loop body on an isolated socket.
func TestF10Phase2_LiveCleanWorktreeGate(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not available")
	}

	town := t.TempDir()
	const rig = "testrig"
	d := &Daemon{config: &Config{TownRoot: town}}

	// Two real crew worktrees: one clean, one dirty (uncommitted work).
	makeWorktree(t, town, rig, "crew", "clean_one", false)
	makeWorktree(t, town, rig, "crew", "dirty_one", true)

	cleanSession := "testrig-crew-clean_one"
	dirtySession := "testrig-crew-dirty_one"

	// (1) REAL git discrimination through the production helpers.
	if !d.sessionWorktreeCleanByName(cleanSession) {
		t.Errorf("clean worktree reported dirty (sessionWorktreeCleanByName)")
	}
	if d.sessionWorktreeCleanByName(dirtySession) {
		t.Errorf("DIRTY worktree reported clean — data-loss gate would FAIL")
	}

	// (2) selectShedVictims fed by REAL-git CleanWorktree flags. Both crew are
	// idle + 0-bead; only the clean one is eligible.
	candidates := []ShedCandidate{
		{Session: cleanSession, Role: "crew", Idle: true, ActiveBeads: 0,
			CleanWorktree: d.sessionWorktreeCleanByName(cleanSession)},
		{Session: dirtySession, Role: "crew", Idle: true, ActiveBeads: 0,
			CleanWorktree: d.sessionWorktreeCleanByName(dirtySession)},
	}
	victims := selectShedVictims(candidates, shedMaxPerTick)
	if len(victims) != 1 || victims[0] != cleanSession {
		t.Fatalf("selection over real-git flags = %v; want only [%s]", victims, cleanSession)
	}

	// (3) REAL tmux kill on an ISOLATED socket — faithful to checkSwapShed's
	// kill loop (re-assert clean, then KillSession). Never the real town.
	sock := "f10dogfood-" + filepath.Base(town)
	tm := tmux.NewTmuxWithSocket(sock)
	t.Cleanup(func() { _ = exec.Command("tmux", "-L", sock, "kill-server").Run() })

	for _, s := range []string{cleanSession, dirtySession} {
		if err := exec.Command("tmux", "-L", sock, "new-session", "-d", "-s", s, "sh", "-c", "sleep 300").Run(); err != nil {
			t.Fatalf("create session %s: %v", s, err)
		}
	}

	// Re-assert + kill exactly the selected (clean) victim, as the daemon does.
	for _, v := range victims {
		if !d.sessionWorktreeCleanByName(v) {
			t.Fatalf("re-assert flipped on clean victim %s", v)
		}
		if err := tm.KillSession(v); err != nil {
			t.Fatalf("KillSession(%s): %v", v, err)
		}
	}

	// Clean victim must be GONE; dirty one must SURVIVE.
	if has, _ := tm.HasSession(cleanSession); has {
		t.Errorf("clean idle session %s still alive after park", cleanSession)
	}
	if has, _ := tm.HasSession(dirtySession); !has {
		t.Errorf("DIRTY session %s was killed — data-loss-safety VIOLATED", dirtySession)
	}
}
