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
	remoteDoltFwdPort = "13307" // SETTLED w/ eng_sr2 + offload_ops: node-side fwd port,
	// distinct from any Dolt port. A node may run its OWN Dolt on 3307 (offload nodes
	// can; the cluster hub does), so GT_DOLT_PORT=3307 would silently hit the node's
	// local Dolt instead of the tunneled host. 13307 guarantees -> the -R forward -> host 3307.

	// Node launch contract (confirmed live by offload_ops --agent on the warm nodes):
	// login user is ubuntu (no ec2-user; root SSH forced-command-blocked); the agent
	// toolchain (gt linux/amd64 + claude + bd) and the crew HOME/clone are staged under
	// /opt/gastown, runnable by root via SSM. The agent loop is launched with
	// `systemd-run --scope` (verified to survive the SSM run-command exit — the SSM
	// timeout does not bound the agent); nohup/setsid/tmux also present as fallbacks.
	remoteNodeUser = "ubuntu"
	remoteNodeHome = "/opt/gastown"
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
// BOTH contracts are now LOCKED + proven live:
//   - TUNNEL (eng_sr2): host opens ssh -R 13307:127.0.0.1:3307 over SSM
//     (open-remote-tunnel.sh, 13307-canonical, TUNNEL_SSH_USER=ubuntu); agent
//     exports GT_DOLT_HOST=127.0.0.1 GT_DOLT_PORT=13307. bd reached host Dolt
//     through it live.
//   - NODE (offload_ops): provision-node.sh --agent stages gt(linux/amd64 from
//     bin/gt-linux-amd64) + claude + bd under /opt/gastown as user ubuntu; the
//     agent loop launches via `systemd-run --scope` (survives the SSM exit;
//     the SSM timeout does not bound it). Verified: all 3 binaries + tunnel +
//     keepalive + key-auth.
//
// The orchestration BODY (open tunnel -> SSM systemd-run the agent with the
// computed env/identity) is wired jointly in the live e2e with eng_sr2 (tunnel
// script) + offload_ops (--agent + send-command), since it invokes their proven
// scripts — gt computes the env/command/identity (below) and drives them. That
// e2e is currently gated on ONE host-side, pre-existing, NON-gt-source issue: the
// bd schema skew (town reactivecli DB = v52; the bd binary = v49, which fails on
// the HOST's own bd too). A v52-matching bd build unblocks the live write; the
// spawn/tunnel/toolchain are all proven. Routed the bd-version owner question up.
func runCrewStartRemote(crewMgr *crew.Manager, r *rig.Rig, name, node string) error {
	// Resolve (or create) the worker so a remote spawn has the same identity a
	// local one would — session name, rig, clone metadata.
	worker, err := crewMgr.Get(name)
	if err != nil {
		return fmt.Errorf("resolving crew member %q for remote spawn: %w", name, err)
	}
	sessionName := crewMgr.SessionName(name)
	doltEnv := remoteAgentDoltEnv()

	// ponytail: the orchestration body is wired in the joint live-e2e (it drives
	// eng_sr2's + offload_ops' proven scripts); soloing it would hardcode their CLIs.
	// Contracts locked + binary published; the live proof is gated only on the
	// host-side bd v52 parity. Seam reports the locked contract + the real gate.
	return fmt.Errorf(
		"gt crew start --remote: contract-complete for %s/%s (session %s) on node %s "+
			"[user=%s home=%s, agent env %s=%s %s=%s via host-initiated ssh -R + systemd-run] — "+
			"live e2e gated on bd v52 schema parity (host-side, pre-existing, not gt-source); "+
			"orchestration body wired in the joint e2e once bd parity lands",
		r.Name, worker.Name, sessionName, node,
		remoteNodeUser, remoteNodeHome,
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
