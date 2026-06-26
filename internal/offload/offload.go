// Package offload embeds offload_eng's proven offload script suite and runs it.
// gt offload is a thin wrapper: the embedded bash scripts are the source of truth
// for the SSM/presign/Bedrock mechanics (see scripts/VENDORED.md); this package
// only extracts them to a tempdir and shells out, preserving fail-closed exit
// propagation. Do not reimplement the script logic in Go.
package offload

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
)

//go:embed scripts/*.sh
var scriptsFS embed.FS

// extractScripts writes the embedded scripts to a fresh tempdir (executable) and
// returns the dir. The scripts reference each other via $HERE (their own dir), so
// they must all land in the same directory. Caller must remove the dir.
func extractScripts() (string, error) {
	dir, err := os.MkdirTemp("", "gt-offload-*")
	if err != nil {
		return "", fmt.Errorf("creating temp dir: %w", err)
	}
	entries, err := fs.ReadDir(scriptsFS, "scripts")
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", fmt.Errorf("reading embedded scripts: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".sh" {
			continue
		}
		content, err := scriptsFS.ReadFile("scripts/" + e.Name())
		if err != nil {
			_ = os.RemoveAll(dir)
			return "", fmt.Errorf("reading embedded %s: %w", e.Name(), err)
		}
		// 0755: the scripts exec each other via $HERE, so they must be runnable.
		if err := os.WriteFile(filepath.Join(dir, e.Name()), content, 0755); err != nil {
			_ = os.RemoveAll(dir)
			return "", fmt.Errorf("writing %s: %w", e.Name(), err)
		}
	}
	return dir, nil
}

// run extracts the scripts and execs script (by base name) with args, wiring
// stdio straight through so the child's exit code — the load-bearing fail-closed
// signal — propagates to our caller. env entries (KEY=VALUE) are appended to the
// process environment.
func run(script string, args []string, env []string) error {
	dir, err := extractScripts()
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(dir) }()

	cmd := exec.Command(filepath.Join(dir, script), args...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(), env...)
	return cmd.Run() // *exec.ExitError carries the child's non-zero code
}

// Dispatch runs offload.sh with the assembled args (flags already mapped) and the
// positional <repo-url> <branch> <command>. env carries any --timeout etc. mapped
// to the script's env vars (e.g. OFFLOAD_TIMEOUT).
func Dispatch(args []string, env []string) error {
	return run("offload.sh", args, env)
}

// Setup runs setup-secrets.sh (one-time per operator session: stages gh PAT +
// Bedrock bearer to the offload bucket using the operator's admin creds).
func Setup(env []string) error {
	return run("setup-secrets.sh", nil, env)
}
