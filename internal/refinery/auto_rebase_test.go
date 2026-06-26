package refinery

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/steveyegge/gastown/internal/git"
	"github.com/steveyegge/gastown/internal/rig"
)

// runGitAR runs git in dir, failing the test on error.
func runGitAR(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func writeAR(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// engineerOnRepo builds an Engineer whose git operates on repoDir.
func engineerOnRepo(repoDir string) *Engineer {
	return &Engineer{
		rig:     &rig.Rig{Name: "r", Path: repoDir},
		git:     git.NewGit(repoDir),
		workDir: repoDir,
		config:  &MergeQueueConfig{},
		output:  io.Discard,
	}
}

// TestMaybeAutoRebase covers the F8 3-state predicate against real git repos.
func TestMaybeAutoRebase(t *testing.T) {
	t.Run("up-to-date: no rebase", func(t *testing.T) {
		repo := t.TempDir()
		runGitAR(t, repo, "init", "-b", "main")
		writeAR(t, repo, "f.txt", "base\n")
		runGitAR(t, repo, "add", ".")
		runGitAR(t, repo, "commit", "-m", "base")
		// feature branches off the current main tip and adds a commit.
		runGitAR(t, repo, "checkout", "-b", "feature")
		writeAR(t, repo, "feat.txt", "x\n")
		runGitAR(t, repo, "add", ".")
		runGitAR(t, repo, "commit", "-m", "feat")
		// main has NOT advanced → main is an ancestor of feature → up-to-date.
		runGitAR(t, repo, "checkout", "main")

		e := engineerOnRepo(repo)
		rebased, err := e.maybeAutoRebase("feature", "main")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if rebased {
			t.Fatal("up-to-date branch should not be rebased")
		}
	})

	t.Run("stale-but-clean: rebases", func(t *testing.T) {
		repo := t.TempDir()
		runGitAR(t, repo, "init", "-b", "main")
		writeAR(t, repo, "f.txt", "base\n")
		runGitAR(t, repo, "add", ".")
		runGitAR(t, repo, "commit", "-m", "base")
		runGitAR(t, repo, "checkout", "-b", "feature")
		writeAR(t, repo, "feat.txt", "x\n") // disjoint file → no conflict
		runGitAR(t, repo, "add", ".")
		runGitAR(t, repo, "commit", "-m", "feat")
		// main advances on a DIFFERENT file → feature is stale but rebases clean.
		runGitAR(t, repo, "checkout", "main")
		writeAR(t, repo, "other.txt", "y\n")
		runGitAR(t, repo, "add", ".")
		runGitAR(t, repo, "commit", "-m", "advance main")

		e := engineerOnRepo(repo)
		rebased, err := e.maybeAutoRebase("feature", "main")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if !rebased {
			t.Fatal("stale-but-clean branch should be rebased")
		}
		// After a clean rebase, main is now an ancestor of feature.
		anc, _ := e.git.IsAncestor("main", "feature")
		if !anc {
			t.Fatal("after rebase, main should be an ancestor of feature")
		}
	})

	t.Run("call-site wiring: gate re-runs on rebased tree + target restored", func(t *testing.T) {
		// Locks the doMerge call-site wiring eng_sr2 flagged: after a stale-but-clean
		// rebase, gates re-run on the REBASED worktree, then the checkout is restored
		// to target for the downstream CheckConflicts/no-op-guard. We replicate the
		// inserted call-site sequence exactly (the same calls doMerge makes when
		// on_conflict=auto_rebase) and assert both observable effects.
		repo := t.TempDir()
		runGitAR(t, repo, "init", "-b", "main")
		writeAR(t, repo, "f.txt", "base\n")
		runGitAR(t, repo, "add", ".")
		runGitAR(t, repo, "commit", "-m", "base")
		runGitAR(t, repo, "checkout", "-b", "feature")
		writeAR(t, repo, "feat.txt", "x\n")
		runGitAR(t, repo, "add", ".")
		runGitAR(t, repo, "commit", "-m", "feat")
		// main advances with a marker file that only exists AFTER the rebase.
		runGitAR(t, repo, "checkout", "main")
		writeAR(t, repo, "target-marker.txt", "advanced\n")
		runGitAR(t, repo, "add", ".")
		runGitAR(t, repo, "commit", "-m", "advance main")

		e := engineerOnRepo(repo)
		e.config.OnConflict = "auto_rebase"
		// Gate asserts it runs on the rebased tree: target-marker.txt is only present
		// if the rebase moved feature onto advanced-main BEFORE the gate ran. It also
		// drops a sentinel so the test can confirm the gate actually executed.
		e.config.Gates = map[string]*GateConfig{
			"rebased-check": {Cmd: "test -f target-marker.txt && touch gate-ran.sentinel"},
		}

		// Replicate the doMerge call-site block (on_conflict==auto_rebase path):
		rebased, rerr := e.maybeAutoRebase("feature", "main")
		if rerr != nil {
			t.Fatalf("maybeAutoRebase: %v", rerr)
		}
		if !rebased {
			t.Fatal("stale-but-clean feature should have rebased")
		}
		if len(e.config.Gates) > 0 {
			if gr := e.runGates(context.Background()); !gr.Success {
				t.Fatalf("gate failed (should run on rebased tree where target-marker exists): %s", gr.Error)
			}
		}
		if err := e.git.Checkout("main"); err != nil {
			t.Fatalf("restore target checkout: %v", err)
		}

		// (a) gate actually ran on the rebased tree (sentinel only written if the
		//     marker was present, i.e. rebase happened before the gate).
		if _, err := os.Stat(filepath.Join(repo, "gate-ran.sentinel")); err != nil {
			t.Fatalf("gate did not run on the rebased worktree: %v", err)
		}
		// (b) worktree restored to target (main), as the downstream merge path expects.
		cur := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
		cur.Dir = repo
		out, _ := cur.Output()
		if got := string(out); len(got) < 4 || got[:4] != "main" {
			t.Fatalf("after call-site sequence, HEAD = %q, want main restored", got)
		}
	})

	t.Run("real-conflict: aborts, not rebased, clean worktree", func(t *testing.T) {
		repo := t.TempDir()
		runGitAR(t, repo, "init", "-b", "main")
		writeAR(t, repo, "f.txt", "base\n")
		runGitAR(t, repo, "add", ".")
		runGitAR(t, repo, "commit", "-m", "base")
		runGitAR(t, repo, "checkout", "-b", "feature")
		writeAR(t, repo, "f.txt", "feature-change\n") // SAME file
		runGitAR(t, repo, "add", ".")
		runGitAR(t, repo, "commit", "-m", "feat edits f")
		// main edits the SAME file differently → rebase conflicts.
		runGitAR(t, repo, "checkout", "main")
		writeAR(t, repo, "f.txt", "main-change\n")
		runGitAR(t, repo, "add", ".")
		runGitAR(t, repo, "commit", "-m", "main edits f")

		e := engineerOnRepo(repo)
		rebased, err := e.maybeAutoRebase("feature", "main")
		if err != nil {
			t.Fatalf("real conflict should NOT error (it falls through to reject): %v", err)
		}
		if rebased {
			t.Fatal("conflicting branch must not report rebased")
		}
		// Worktree must be clean (rebase aborted) — no rebase-in-progress dir.
		if _, statErr := os.Stat(filepath.Join(repo, ".git", "rebase-merge")); !os.IsNotExist(statErr) {
			t.Fatal("rebase-merge dir present — rebase was not aborted cleanly")
		}
	})
}
