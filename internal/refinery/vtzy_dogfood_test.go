package refinery

// Dogfood (dogfood_a, rc-vtzy, C-11): exercise the probe-only source-issue close GATE against a
// REAL git repo — the seam the unit test (TestChangedFilesAreProbeOnly) stubs with a mocked file
// list. This drives mrIsProbeOnly -> e.git.DiffNameOnly on actual branches/commits, proving the
// real merge-flow decision (close vs leave-open) for gastown_eng_lead's 3 spec scenarios.
//
// Run: go test ./internal/refinery/ -run TestVtzyDogfood -v

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// writeFileNested writes content to a repo-relative path, creating parent dirs (batch_test.go's
// writeFile assumes a flat path; probe dirs like qa/ and internal/refinery/ need mkdir -p).
func writeFileNested(t *testing.T, dir, relPath, content string) {
	t.Helper()
	full := filepath.Join(dir, relPath)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// createFeatureBranchMulti branches from main, writes+commits every file in files, returns to main.
// Multi-file sibling of batch_test.go's single-file createFeatureBranch — needed for the
// probe+code mixed-diff scenario.
func createFeatureBranchMulti(t *testing.T, workDir, branchName string, files map[string]string) {
	t.Helper()
	run(t, workDir, "git", "checkout", "-b", branchName, "main")
	for path, content := range files {
		writeFileNested(t, workDir, path, content)
	}
	run(t, workDir, "git", "add", "-A")
	run(t, workDir, "git", "commit", "-m", fmt.Sprintf("change %d file(s) on %s", len(files), branchName))
	run(t, workDir, "git", "checkout", "main")
}

func TestVtzyDogfood_RealGitGate(t *testing.T) {
	cases := []struct {
		name        string
		files       map[string]string // path -> content, all committed on the feature branch
		wantProbe   bool              // true => source bug bead LEFT OPEN; false => bead CLOSES
		description string
	}{
		{
			name:        "scenario1_probe_only_leaves_open",
			files:       map[string]string{"qa/probe_mr0s.py": "assert False  # RED-by-design\n"},
			wantProbe:   true,
			description: "all changed files under qa/ -> probe-only -> source bug bead LEFT OPEN",
		},
		{
			name:        "scenario1b_tests_and_evals_leave_open",
			files:       map[string]string{"tests/test_guard.py": "x\n", "evals/scen.json": "{}\n"},
			wantProbe:   true,
			description: "tests/ + evals/ only -> still probe-only -> LEFT OPEN",
		},
		{
			name:        "scenario2_real_code_closes",
			files:       map[string]string{"internal/refinery/fix.go": "package refinery\n"},
			wantProbe:   false,
			description: "a product-code file outside probe prefixes -> real fix -> bead CLOSES",
		},
		{
			name:        "scenario2b_nonsrc_layout_code_closes",
			files:       map[string]string{"lib/handler.rb": "# fix\n"},
			wantProbe:   false,
			description: "code in a non-src/ layout rig -> CLOSES (layout-agnostic, not src/-only)",
		},
		{
			name:        "scenario3_probe_plus_code_closes",
			files:       map[string]string{"qa/probe.py": "x\n", "engine.py": "y\n"},
			wantProbe:   false,
			description: "a real fix riding with a probe -> still a fix -> bead CLOSES",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			workDir, g, cleanup := testGitRepo(t)
			defer cleanup()

			branch := "feat/" + tc.name
			createFeatureBranchMulti(t, workDir, branch, tc.files)

			e := newTestEngineer(t, workDir, g)
			mr := makeMR("mr-"+tc.name, branch, "main")

			got := e.mrIsProbeOnly(mr)
			if got != tc.wantProbe {
				t.Fatalf("%s\n  mrIsProbeOnly(real git diff) = %v, want %v\n  => bug bead would %s, spec wants %s",
					tc.description, got, tc.wantProbe,
					closeWord(got), closeWord(tc.wantProbe))
			}
			t.Logf("PASS: %s (mrIsProbeOnly=%v => bug bead %s)", tc.description, got, closeWord(got))
		})
	}
}

func closeWord(probeOnly bool) string {
	if probeOnly {
		return "LEFT OPEN"
	}
	return "CLOSE"
}
