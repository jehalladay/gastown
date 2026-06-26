package doctor

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/steveyegge/gastown/internal/git"
)

func TestHTTPVersionCheck_Name(t *testing.T) {
	c := NewHTTPVersionCheck()
	if c.Name() != "http-version-pinned" {
		t.Errorf("name = %q, want http-version-pinned", c.Name())
	}
	if !c.CanFix() {
		t.Error("CanFix should be true")
	}
}

// initRepoForHTTPCheck makes a git repo at rigDir/mayor/rig. If pinLocal, it sets
// the repo-LOCAL http.version=HTTP/1.1.
func initRepoForHTTPCheck(t *testing.T, rigDir string, pinLocal bool) string {
	t.Helper()
	clone := filepath.Join(rigDir, "mayor", "rig")
	if err := os.MkdirAll(clone, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "-C", clone, "init").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if pinLocal {
		if out, err := exec.Command("git", "-C", clone, "config", "--local", "http.version", git.HTTPVersionConfig).CombinedOutput(); err != nil {
			t.Fatalf("pin local: %v\n%s", err, out)
		}
	}
	return clone
}

// TestHTTPVersionCheck_FalseGreenOnGlobalInheritance is the regression guard for
// the dogfood-found bug: a clone with NO local pin must be flagged even when a
// GLOBAL http.version=HTTP/1.1 exists (which --get would inherit, masking the
// missing local pin). Repro uses GIT_CONFIG_GLOBAL to simulate a host with a
// global pin.
func TestHTTPVersionCheck_FalseGreenOnGlobalInheritance(t *testing.T) {
	town := t.TempDir()
	rigDir := filepath.Join(town, "testrig")
	initRepoForHTTPCheck(t, rigDir, false) // NO local pin

	// Simulate a host whose global gitconfig sets http.version=HTTP/1.1.
	globalCfg := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(globalCfg, []byte("[http]\n\tversion = HTTP/1.1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", globalCfg)

	c := NewHTTPVersionCheck()
	res := c.Run(&CheckContext{TownRoot: town, RigName: "testrig"})
	if res.Status == StatusOK {
		t.Fatal("FALSE GREEN: clone with no local pin reported OK because of global inheritance")
	}
	if len(c.misconfiguredClones) == 0 {
		t.Fatal("expected the unpinned clone to be flagged")
	}

	// Fix must repin local; afterward the check passes on the local value.
	if err := c.Fix(&CheckContext{TownRoot: town, RigName: "testrig"}); err != nil {
		t.Fatalf("Fix: %v", err)
	}
	out, err := exec.Command("git", "-C", c.misconfiguredClones[0], "config", "--local", "--get", "http.version").Output()
	if err != nil {
		t.Fatalf("read local after fix: %v", err)
	}
	if got := string(out); got == "" {
		t.Fatal("Fix did not write the local http.version")
	}
}

// TestHTTPVersionCheck_PassesWhenLocallyPinned confirms the happy path.
func TestHTTPVersionCheck_PassesWhenLocallyPinned(t *testing.T) {
	town := t.TempDir()
	rigDir := filepath.Join(town, "testrig")
	initRepoForHTTPCheck(t, rigDir, true) // local pin present

	c := NewHTTPVersionCheck()
	res := c.Run(&CheckContext{TownRoot: town, RigName: "testrig"})
	if res.Status != StatusOK {
		t.Fatalf("expected OK with local pin, got %v: %s", res.Status, res.Message)
	}
}

// TestHTTPVersionCheck_TownWideNoRig confirms it scans all rigs when no --rig is
// given (dogfood point b: town-level should not error "No rig specified").
func TestHTTPVersionCheck_TownWideNoRig(t *testing.T) {
	town := t.TempDir()
	initRepoForHTTPCheck(t, filepath.Join(town, "rigA"), true)
	initRepoForHTTPCheck(t, filepath.Join(town, "rigB"), false) // unpinned

	c := NewHTTPVersionCheck()
	res := c.Run(&CheckContext{TownRoot: town}) // no RigName
	if res.Status == StatusError && res.Message == "No rig specified" {
		t.Fatal("town-wide run should not require --rig")
	}
	if res.Status == StatusOK {
		t.Fatal("expected rigB's unpinned clone to be flagged town-wide")
	}
}
