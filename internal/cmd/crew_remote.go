package cmd

import (
	"fmt"

	"github.com/steveyegge/gastown/internal/crew"
	"github.com/steveyegge/gastown/internal/rig"
)

// Reverse-tunnel contract (F2 Tier-1), locked with eng_sr2 + grounded in
// distcompute's phase0 findings. The HOST opens `ssh -R <remoteDoltFwdPort>:
// 127.0.0.1:3307 <node>` over an SSM session (node->host is NOT routable: host
// behind corp NAT), forwarding the host's Dolt 3307 to the node's loopback. The
// spawned agent therefore reaches Dolt at 127.0.0.1:<remoteDoltFwdPort> — and
// GT_DOLT_HOST/PORT route bd/gt-mail there end-to-end with no extra wiring
// (internal/beads translates GT_DOLT_HOST -> BEADS_DOLT_SERVER_HOST, preventing
// the localhost:3307 fallback). bd/mail go STRAIGHT to the tunneled port — NOT
// through gt-proxy (that's the exec/acp proxy, unrelated to the Dolt data plane).
const (
	remoteDoltHost    = "127.0.0.1"
	remoteDoltFwdPort = "13307" // fixed convention so the spawn env is static
)

// runCrewStartRemote spawns a crew member's agent loop on a cluster node instead
// of a local tmux session — F2, the Tier-1 (host-up) memory-relief lever: the
// memory-heavy agent processes move off the local box while still reaching the
// host's Dolt over a host-initiated reverse tunnel (ssh -R over SSM). It does NOT
// need the cluster Dolt hub (that's Tier-2 / host-down persistence).
//
// Shape (smallest path, locked with eng_sr2):
//  0. HOST opens the reverse tunnel: ssh -R 13307:127.0.0.1:3307 over an SSM
//     session to `node` (eng_sr2 owns this script + keepalive).
//  1. SSM-run a job on `node` that exports the agent env (GT_ROLE/GT_RIG/GT_CREW/
//     ... same as a local crew session) PLUS GT_DOLT_HOST/GT_DOLT_PORT = the
//     tunnel endpoint (remoteDoltHost:remoteDoltFwdPort), so the node's bd/gt-mail
//     reach the host Dolt; and
//  2. launches the agent loop persistently (long-lived process on the node).
//
// Tunnel contract (Q1/Q2/Q4): LOCKED — see remoteDoltHost/remoteDoltFwdPort and
// remoteAgentEnv below. Tunnel-open + keepalive (Q3) is eng_sr2's host-side piece,
// wired after this scaffold.
//
// Still pending — the NODE-LAUNCH contract (offload_ops): the warm node's instance
// IDs, that the agent toolchain (claude/RC + gt + bd + uv + creds) is staged there,
// and HOW a long-lived process is kept alive past the SSM run-command timeout
// (nohup/systemd-run/tmux on the node). The SSM dispatch primitive itself is the
// vendored offload.sh/ssm-run (F4); remote-spawn = "launch a long-lived agent" vs
// offload's "run one job + exit" — same dispatch, different lifecycle. Until that
// lands, this returns a clear not-yet-wired error rather than guessing the keepalive
// mechanism. The tunnel env (the eng_sr2 half) is now concrete.
func runCrewStartRemote(crewMgr *crew.Manager, r *rig.Rig, name, node string) error {
	// Resolve (or create) the worker so a remote spawn has the same identity a
	// local one would — session name, rig, clone metadata.
	worker, err := crewMgr.Get(name)
	if err != nil {
		return fmt.Errorf("resolving crew member %q for remote spawn: %w", name, err)
	}
	sessionName := crewMgr.SessionName(name)
	doltEnv := remoteAgentDoltEnv()

	// ponytail: the agent-launch body is gated on offload_ops' node-launch contract
	// (toolchain staging + long-lived-process keepalive). The tunnel env (eng_sr2)
	// is locked; wiring the launch now would hardcode a guessed keepalive interface.
	// Seam fills the instant offload_ops' node contract lands.
	return fmt.Errorf(
		"gt crew start --remote: scaffold ready for %s/%s (session %s) on node %s — "+
			"tunnel env locked (%s=%s %s=%s, via host-initiated ssh -R, eng_sr2); "+
			"pending offload_ops' node-launch contract (toolchain staging + long-lived keepalive) "+
			"— coordinating on channel:offload-persistence",
		r.Name, worker.Name, sessionName, node,
		"GT_DOLT_HOST", doltEnv["GT_DOLT_HOST"], "GT_DOLT_PORT", doltEnv["GT_DOLT_PORT"])
}

// remoteAgentDoltEnv returns the Dolt-connection env a remotely-spawned agent must
// export so its bd/gt-mail reach the host's Dolt through the reverse tunnel. These
// override the local-default localhost:3307; GT_DOLT_HOST/PORT propagate to
// BEADS_DOLT_SERVER_HOST/PORT for bd subprocesses (internal/beads).
func remoteAgentDoltEnv() map[string]string {
	return map[string]string{
		"GT_DOLT_HOST": remoteDoltHost,
		"GT_DOLT_PORT": remoteDoltFwdPort,
	}
}
