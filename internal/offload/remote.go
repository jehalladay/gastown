// Package offload embeds the remote-spawn script suite and drives it so
// gt crew start --remote can run host-independently (the scripts are the source
// of truth for the ssh-R/SSM mechanics; this package extracts + executes them,
// it does not reimplement them). See scripts/VENDORED.md.
package offload

import (
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
)

// scriptsFS (the //go:embed scripts/*.sh var) is declared in offload.go — same
// package, same embed glob, so the F4 offload suite + the F2 remote-spawn suite
// share one embedded FS. ExtractScripts below is the public extractor (vs
// offload.go's private extractScripts for the offload-dispatch path).

// ExtractScripts writes the embedded *.sh to a fresh tempdir (executable) and
// returns the dir. The remote-spawn scripts reference each other via $HERE, so
// they must all land together. Caller removes the dir when done.
func ExtractScripts() (string, error) {
	dir, err := os.MkdirTemp("", "gt-remote-*")
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
		// 0755: the scripts exec each other + are invoked directly.
		if err := os.WriteFile(filepath.Join(dir, e.Name()), content, 0755); err != nil {
			_ = os.RemoveAll(dir)
			return "", fmt.Errorf("writing %s: %w", e.Name(), err)
		}
	}
	return dir, nil
}

// StartTunnel runs the extracted open-remote-tunnel.sh for node in the BACKGROUND
// (it's a keepalive loop that holds the reverse tunnel open) and returns the
// started *exec.Cmd so the caller can stop it (Process.Kill) on shutdown, and the
// path to the tunnel's log file. fwdPort is the node-side forwarded port (13307);
// env (e.g. TUNNEL_SSH_KEY=<path>) is appended to the process environment.
//
// CRITICAL: the tunnel is a LONG-LIVED keepalive loop that outlives this command.
// Its stdout/stderr go to a LOG FILE, NOT the parent's os.Stdout — if it inherited
// the parent stdout, releasing the process would leave the parent's stdout pipe
// held open by the tunnel forever, so `gt crew start --remote | tail` would hang
// with no output even after the spawn finished. (Dogfood-found: the first --remote
// run hung exactly this way.) The operator tails the returned logPath for status.
func StartTunnel(scriptDir, node, fwdPort string, env []string) (cmd *exec.Cmd, logPath string, err error) {
	script := filepath.Join(scriptDir, "open-remote-tunnel.sh")
	if _, statErr := os.Stat(script); statErr != nil {
		return nil, "", fmt.Errorf("tunnel script not found at %s: %w", script, statErr)
	}
	logf, err := os.CreateTemp("", "gt-tunnel-*.log")
	if err != nil {
		return nil, "", fmt.Errorf("creating tunnel log: %w", err)
	}
	logPath = logf.Name()

	cmd = exec.Command(script, node, fwdPort)
	cmd.Stdout = logf
	cmd.Stderr = logf
	cmd.Stdin = nil
	cmd.Env = append(os.Environ(), env...)
	if err := cmd.Start(); err != nil {
		_ = logf.Close()
		return nil, logPath, fmt.Errorf("starting reverse tunnel to %s: %w", node, err)
	}
	// The child holds its own fd to logf; the parent can close its copy.
	_ = logf.Close()
	return cmd, logPath, nil
}

// RunProvision runs the extracted provision-node.sh in the FOREGROUND (blocking) to
// stage the node's toolchain + clone/prime the crew workspace BEFORE the agent launches.
// It is the verb's step 0: without it the agent has no /opt/gastown/<crew> clone, so bd
// has no DB and claude has no identity (the prod-confirmed no-Provision bug, hq-wwxq).
// crewName drives `--agent --crew <crewName>` (the --crew flag is REQUIRED for the clone;
// --agent alone stages toolchain only). repoURL/branch are optional positional args
// (provision defaults them when ""); env carries AWS/bucket overrides + TUNNEL_SSH_KEY.
// Returns the combined output; a non-zero exit (provision failed) is an error so the
// verb fails loud rather than launching against an unprovisioned node.
func RunProvision(scriptDir, node, crewName, repoURL, branch string, env []string) (string, error) {
	script := filepath.Join(scriptDir, "provision-node.sh")
	if _, err := os.Stat(script); err != nil {
		return "", fmt.Errorf("provision-node.sh not found at %s (not vendored into the embed?): %w", script, err)
	}
	args := []string{"--agent", "--crew", crewName, node}
	if repoURL != "" {
		args = append(args, repoURL)
		if branch != "" {
			args = append(args, branch)
		}
	}
	cmd := exec.Command(script, args...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("provision-node.sh --agent --crew %s %s failed: %w", crewName, node, err)
	}
	return string(out), nil
}

// SSMRun executes nodeScript on node via the extracted ssm-run.sh (aws ssm
// send-command + poll). Used to launch the agent loop (the systemd-run line) on
// the node. timeoutSecs bounds the SSM command (the launched scope outlives it —
// systemd-run --scope survives the send-command exit). Returns the combined
// output; a non-zero ssm-run exit (the node command failed) is an error.
func SSMRun(scriptDir, node, nodeScript, timeoutSecs string) (string, error) {
	ssmRun := filepath.Join(scriptDir, "ssm-run.sh")
	if _, err := os.Stat(ssmRun); err != nil {
		return "", fmt.Errorf("ssm-run.sh not found at %s: %w", ssmRun, err)
	}
	// ssm-run.sh takes <instance-id> <script-file> [timeout]; write the node
	// command to a temp file it reads via jq --rawfile.
	f, err := os.CreateTemp("", "gt-remote-launch-*.sh")
	if err != nil {
		return "", fmt.Errorf("creating launch script: %w", err)
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if _, err := f.WriteString(nodeScript); err != nil {
		_ = f.Close()
		return "", fmt.Errorf("writing launch script: %w", err)
	}
	_ = f.Close()

	cmd := exec.Command(ssmRun, node, f.Name(), timeoutSecs)
	cmd.Env = os.Environ()
	out, err := cmd.CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("ssm-run on %s failed: %w", node, err)
	}
	return string(out), nil
}
