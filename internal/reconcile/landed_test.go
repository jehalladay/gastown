package reconcile

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/steveyegge/gastown/internal/git"
)

// gitR runs a git command in dir, failing the test on error.
func gitR(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return string(out)
}

func writeCommit(t *testing.T, dir, file, content, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", file, err)
	}
	gitR(t, dir, "add", ".")
	gitR(t, dir, "commit", "-m", msg)
}

// setupClonePair builds a bare "origin" + a working clone with master + a feature branch, so
// IsBranchLanded's Fetch(remote) path is exercised against a real remote. Returns the work dir.
func setupClonePair(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	work := filepath.Join(root, "work")

	gitR(t, root, "init", "--bare", origin)
	gitR(t, root, "clone", origin, work)
	gitR(t, work, "config", "user.email", "t@t.com")
	gitR(t, work, "config", "user.name", "T")
	gitR(t, work, "checkout", "-b", "master")
	writeCommit(t, work, "README.md", "# base\n", "initial")
	gitR(t, work, "push", "-u", "origin", "master")
	return work
}

func TestIsBranchLanded(t *testing.T) {
	t.Run("regular-merge: ancestor fast-path -> landed", func(t *testing.T) {
		work := setupClonePair(t)
		// feature off master, then merge it into master (regular, no squash) + push.
		gitR(t, work, "checkout", "-b", "feature")
		writeCommit(t, work, "feat.txt", "x\n", "feat")
		gitR(t, work, "checkout", "master")
		gitR(t, work, "merge", "--no-ff", "feature", "-m", "merge feature")
		gitR(t, work, "push", "origin", "master")

		v, err := IsBranchLanded(git.NewGit(work), work, "origin", "origin/master", "feature")
		if err != nil {
			t.Fatalf("IsBranchLanded: %v", err)
		}
		if !v.Landed {
			t.Fatalf("regular-merged feature should be landed, got %+v", v)
		}
	})

	t.Run("SINGLE-commit squash: cherry detects landed", func(t *testing.T) {
		work := setupClonePair(t)
		// A ONE-commit feature, squash-merged. The squash's combined diff == the single source
		// commit's diff, so patch-ids match and git cherry detects it landed. This is the case the
		// spec's "reuse verifyBranchAlreadyMerged" handles cleanly.
		gitR(t, work, "checkout", "-b", "feature")
		writeCommit(t, work, "feat.txt", "a\n", "feat single")
		gitR(t, work, "checkout", "master")
		gitR(t, work, "merge", "--squash", "feature")
		gitR(t, work, "commit", "-m", "squashed single-commit feature")
		gitR(t, work, "push", "origin", "master")

		v, err := IsBranchLanded(git.NewGit(work), work, "origin", "origin/master", "feature")
		if err != nil {
			t.Fatalf("IsBranchLanded: %v", err)
		}
		if !v.Landed {
			t.Fatalf("single-commit squash must be detected as landed via cherry, got %+v", v)
		}
	})

	t.Run("MULTI-commit squash: Layer-3 combined-patchid detects landed (§6 fix)", func(t *testing.T) {
		work := setupClonePair(t)
		// A multi-commit feature, squash-merged into ONE combined commit. cherry (Layer 2) MISSES it
		// (patch-ids the individual source commits; the squash has only the combined diff). Layer 3
		// (§6, distcompute-verified) computes the COMBINED-diff patch-id (merge-base..feature) and finds
		// the squashed upstream commit whose own patch-id matches it deterministically → landed.
		gitR(t, work, "checkout", "-b", "feature")
		writeCommit(t, work, "feat.txt", "a\n", "feat part 1")
		writeCommit(t, work, "feat.txt", "a\nb\n", "feat part 2")
		gitR(t, work, "checkout", "master")
		gitR(t, work, "merge", "--squash", "feature")
		gitR(t, work, "commit", "-m", "squashed multi-commit feature")
		gitR(t, work, "push", "origin", "master")

		v, err := IsBranchLanded(git.NewGit(work), work, "origin", "origin/master", "feature")
		if err != nil {
			t.Fatalf("IsBranchLanded: %v", err)
		}
		if !v.Landed {
			t.Fatalf("multi-commit squash MUST be detected landed via Layer-3 combined-patchid, got %+v", v)
		}
		if v.Method != "combined-patchid" {
			t.Fatalf("multi-commit squash should resolve via combined-patchid (Layer 3), got method %q", v.Method)
		}
	})

	t.Run("not-landed: unmerged feature -> not landed", func(t *testing.T) {
		work := setupClonePair(t)
		gitR(t, work, "checkout", "-b", "feature")
		writeCommit(t, work, "feat.txt", "unmerged\n", "feat not yet merged")
		// feature is NOT merged to master / pushed.

		v, err := IsBranchLanded(git.NewGit(work), work, "origin", "origin/master", "feature")
		if err != nil {
			t.Fatalf("IsBranchLanded: %v", err)
		}
		if v.Landed {
			t.Fatalf("unmerged feature must NOT be landed, got %+v", v)
		}
	})
}
