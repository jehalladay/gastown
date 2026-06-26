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
