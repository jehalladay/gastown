package offload

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestExtractScripts verifies the embedded suite extracts intact, executable, and
// with the cross-referenced scripts ($HERE/pick-node.sh, $HERE/ssm-run.sh) all
// present in the same dir — the property gt offload relies on.
func TestExtractScripts(t *testing.T) {
	dir, err := extractScripts()
	if err != nil {
		t.Fatalf("extractScripts: %v", err)
	}
	defer os.RemoveAll(dir)

	required := []string{"offload.sh", "pick-node.sh", "ssm-run.sh", "setup-secrets.sh"}
	for _, name := range required {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("expected %s extracted: %v", name, err)
		}
		if info.Mode()&0100 == 0 {
			t.Errorf("%s is not executable (mode %v)", name, info.Mode())
		}
		if info.Size() == 0 {
			t.Errorf("%s is empty", name)
		}
	}
}

// TestEmbeddedScriptsParse runs `bash -n` (syntax check, no execution) on each
// extracted script, so a vendored script that won't parse fails the build's tests
// rather than at dispatch time on a cluster node.
func TestEmbeddedScriptsParse(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	dir, err := extractScripts()
	if err != nil {
		t.Fatalf("extractScripts: %v", err)
	}
	defer os.RemoveAll(dir)

	for _, name := range []string{"offload.sh", "pick-node.sh", "ssm-run.sh", "setup-secrets.sh"} {
		out, err := exec.Command("bash", "-n", filepath.Join(dir, name)).CombinedOutput()
		if err != nil {
			t.Errorf("bash -n %s failed: %v\n%s", name, err, out)
		}
	}
}
