package cmd

import (
	"strings"
	"testing"
)

// TestRemoteAgentDoltEnv locks the F2 reverse-tunnel contract (eng_sr2): a
// remotely-spawned agent reaches the host Dolt via the node's loopback at the
// fixed forwarded port, NOT the local-default 3307. If this convention changes,
// eng_sr2's host-side `ssh -R <port>:127.0.0.1:3307` must change in lockstep.
func TestRemoteAgentDoltEnv(t *testing.T) {
	env := remoteAgentDoltEnv()
	if env["GT_DOLT_HOST"] != "127.0.0.1" {
		t.Errorf("GT_DOLT_HOST = %q, want 127.0.0.1 (node loopback into the reverse tunnel)", env["GT_DOLT_HOST"])
	}
	if env["GT_DOLT_PORT"] != "13307" {
		t.Errorf("GT_DOLT_PORT = %q, want 13307 (the fixed -R forwarded port)", env["GT_DOLT_PORT"])
	}
	// Must NOT be the local-default port, or the agent would hit the node's own
	// (nonexistent) Dolt instead of the tunneled host one.
	if env["GT_DOLT_PORT"] == "3307" {
		t.Error("GT_DOLT_PORT must not be the local-default 3307 — the tunnel forwards to a distinct node port")
	}
}

// TestRemoteAgentEnv verifies the computed remote-agent env carries the GT_*
// crew identity AND the tunnel Dolt overlay wins over any local default — the two
// properties the proven e2e depends on (identity so the agent is the right crew
// member; tunnel endpoint so its bd reaches the host Dolt).
func TestRemoteAgentEnv(t *testing.T) {
	// rigPath under a town root; townRoot is derived as its parent.
	env := remoteAgentEnv("reactivecli", "gastown_eng_lead", "/town/reactivecli", "rc-crew-gastown_eng_lead")

	if env["GT_ROLE"] == "" && env["GT_RIG"] == "" {
		t.Fatal("expected GT_* crew identity env to be populated")
	}
	if env["GT_RIG"] != "reactivecli" {
		t.Errorf("GT_RIG = %q, want reactivecli", env["GT_RIG"])
	}
	// The tunnel overlay MUST win — bd/mail must reach the host Dolt, not localhost:3307.
	if env["GT_DOLT_HOST"] != "127.0.0.1" || env["GT_DOLT_PORT"] != "13307" {
		t.Errorf("tunnel overlay not applied: GT_DOLT_HOST=%q GT_DOLT_PORT=%q, want 127.0.0.1/13307",
			env["GT_DOLT_HOST"], env["GT_DOLT_PORT"])
	}

	// remoteEnvAssignments renders sorted KEY=VALUE; spot-check it includes the overlay.
	got := remoteEnvAssignments(env)
	var hasPort bool
	for _, a := range got {
		if a == "GT_DOLT_PORT=13307" {
			hasPort = true
		}
	}
	if !hasPort {
		t.Errorf("rendered assignments missing GT_DOLT_PORT=13307: %v", got)
	}
	// Sorted order: each entry is KEY=VALUE and the slice is non-decreasing by key.
	for i := 1; i < len(got); i++ {
		if strings.SplitN(got[i-1], "=", 2)[0] > strings.SplitN(got[i], "=", 2)[0] {
			t.Errorf("assignments not sorted at %d: %q before %q", i, got[i-1], got[i])
		}
	}
}

// TestTunnelKeyEnv verifies gt maps GT_TUNNEL_KEY -> TUNNEL_SSH_KEY (which the
// vendored open-remote-tunnel.sh honors as priority #1, avoiding the fragile 60s
// ephemeral key), and returns nil when unset so the caller fails loud.
func TestTunnelKeyEnv(t *testing.T) {
	t.Run("unset -> nil (caller errors)", func(t *testing.T) {
		t.Setenv("GT_TUNNEL_KEY", "")
		if got := tunnelKeyEnv(); got != nil {
			t.Errorf("tunnelKeyEnv() = %v, want nil when GT_TUNNEL_KEY unset", got)
		}
	})
	t.Run("set -> TUNNEL_SSH_KEY=<path>", func(t *testing.T) {
		t.Setenv("GT_TUNNEL_KEY", "/opt/keys/.offload-tunnel-key")
		got := tunnelKeyEnv()
		if len(got) != 1 || got[0] != "TUNNEL_SSH_KEY=/opt/keys/.offload-tunnel-key" {
			t.Errorf("tunnelKeyEnv() = %v, want [TUNNEL_SSH_KEY=/opt/keys/.offload-tunnel-key]", got)
		}
	})
}

// TestRemoteAgentStartupCommandIsNodeSafe locks the two dogfood-found bugs: the
// remote startup must use a BARE agent name (PATH-resolved on the node), NOT the
// host's absolute claude path, and must carry NO embedded `env KEY=VAL` prefix
// (env comes from systemd --setenv, which has the 13307 tunnel overlay; a host
// env prefix re-set GT_DOLT_PORT=3307 and defeated the tunnel).
func TestRemoteAgentStartupCommandIsNodeSafe(t *testing.T) {
	cmd, err := remoteAgentStartupCommand("reactivecli", "research_bench", "/town/reactivecli", "rc-crew-research_bench")
	if err != nil {
		t.Fatalf("remoteAgentStartupCommand: %v", err)
	}
	if !strings.HasPrefix(cmd, "claude ") {
		t.Errorf("startup must start with bare `claude`, got: %q", cmd)
	}
	// No host absolute path (the exec-127 bug).
	for _, hostPath := range []string{"/Users/", "/.toolbox/", "/opt/homebrew/"} {
		if strings.Contains(cmd, hostPath) {
			t.Errorf("startup must not embed a host path %q: %q", hostPath, cmd)
		}
	}
	// No embedded env prefix (the GT_DOLT_PORT=3307 double-set bug); env is via --setenv.
	if strings.HasPrefix(cmd, "env ") || strings.Contains(cmd, "GT_DOLT_PORT=") {
		t.Errorf("startup must not embed an env prefix (env comes from --setenv): %q", cmd)
	}
	if !strings.Contains(cmd, "--dangerously-skip-permissions") {
		t.Errorf("startup should match local crew (skip-permissions): %q", cmd)
	}
}

// TestBuildRemoteSpawnPlan locks the spawn-command assembly against offload_ops'
// verified shapes: provision --agent, tunnel on the fwd port, and the systemd-run
// --scope line carrying the agent env as --setenv flags + the startup command.
func TestBuildRemoteSpawnPlan(t *testing.T) {
	env := map[string]string{
		"GT_DOLT_HOST": "127.0.0.1",
		"GT_DOLT_PORT": "13307",
		"GT_RIG":       "reactivecli",
	}
	plan := buildRemoteSpawnPlan("i-0abc", "max", "/tmp/scripts", env, "claude --foo beacon", "rc-crew-max")

	// provision-node.sh --agent <node>
	if got := strings.Join(plan.Provision, " "); got != "/tmp/scripts/provision-node.sh --agent i-0abc" {
		t.Errorf("Provision = %q", got)
	}
	// open-remote-tunnel.sh <node> 13307
	if got := strings.Join(plan.Tunnel, " "); got != "/tmp/scripts/open-remote-tunnel.sh i-0abc 13307" {
		t.Errorf("Tunnel = %q", got)
	}
	// systemd-run as a transient SERVICE (--unit + Restart=on-failure, not --scope),
	// stable unit, per-var --setenv, sh -lc with the node PATH exported + startup.
	sr := strings.Join(plan.SystemdRun, " ")
	for _, want := range []string{
		"sudo systemd-run --unit=gt-crew-max-rc-crew-max --property=Restart=on-failure --uid=ubuntu",
		"--setenv=GT_DOLT_HOST=127.0.0.1",
		"--setenv=GT_DOLT_PORT=13307",
		"--setenv=GT_RIG=reactivecli",
	} {
		if !strings.Contains(sr, want) {
			t.Errorf("SystemdRun missing %q in: %s", want, sr)
		}
	}
	if strings.Contains(sr, "--scope") {
		t.Errorf("SystemdRun should use --unit service form, not --scope: %s", sr)
	}
	// HOME defaulted when env omits it (node toolchain root).
	if !strings.Contains(sr, "--setenv=HOME="+remoteNodeHome) {
		t.Errorf("SystemdRun should default HOME=%s when env omits it: %s", remoteNodeHome, sr)
	}
	// The final arg is the sh -lc payload: exports the node toolchain PATH (so claude
	// resolves) then runs the startup command.
	last := plan.SystemdRun[len(plan.SystemdRun)-1]
	if !strings.Contains(last, "export PATH="+remoteNodePATH) {
		t.Errorf("launch payload missing node PATH export: %q", last)
	}
	if !strings.HasSuffix(last, "claude --foo beacon") {
		t.Errorf("launch payload should end with the startup command: %q", last)
	}
}

// TestShellQuoteJoin verifies the shell quoting that renders the systemd-run argv
// into the SSM-delivered command line. Env values + the startup command pass
// through it, so a value with spaces/quotes must not break out of its argument.
func TestShellQuoteJoin(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", "''"},
		{"plain", "'plain'"},
		{"with space", "'with space'"},
		{"GT_ROLE=crew", "'GT_ROLE=crew'"},
		{"it's", `'it'\''s'`}, // embedded single quote escaped
	}
	for _, c := range cases {
		if got := shellQuote(c.in); got != c.want {
			t.Errorf("shellQuote(%q) = %q, want %q", c.in, got, c.want)
		}
	}

	// shellJoin quotes each arg; an injection-y value stays contained in one arg.
	got := shellJoin([]string{"sh", "-lc", "echo hi; rm -rf /"})
	want := `'sh' '-lc' 'echo hi; rm -rf /'`
	if got != want {
		t.Errorf("shellJoin = %q, want %q", got, want)
	}
}
