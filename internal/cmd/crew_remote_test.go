package cmd

import "testing"

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
