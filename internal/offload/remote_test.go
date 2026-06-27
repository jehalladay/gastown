package offload

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestExtractScripts verifies the embedded remote-spawn suite extracts intact,
// executable, and parses (bash -n) — so a vendored script that won't parse fails
// the build's tests, not a live spawn on a node.
func TestExtractScripts(t *testing.T) {
	dir, err := ExtractScripts()
	if err != nil {
		t.Fatalf("ExtractScripts: %v", err)
	}
	defer os.RemoveAll(dir)

	// The suite the verb drives.
	for _, name := range []string{"open-remote-tunnel.sh", "ssm-run.sh"} {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("expected %s extracted: %v", name, err)
		}
		if info.Mode()&0100 == 0 {
			t.Errorf("%s not executable (mode %v)", name, info.Mode())
		}
		if info.Size() == 0 {
			t.Errorf("%s is empty", name)
		}
		if _, err := exec.LookPath("bash"); err == nil {
			if out, perr := exec.Command("bash", "-n", path).CombinedOutput(); perr != nil {
				t.Errorf("bash -n %s failed: %v\n%s", name, perr, out)
			}
		}
	}
}
