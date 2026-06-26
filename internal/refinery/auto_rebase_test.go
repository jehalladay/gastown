package refinery

import (
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
		rig:    &rig.Rig{Name: "r", Path: repoDir},
		git:    git.NewGit(repoDir),
		output: io.Discard,
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
