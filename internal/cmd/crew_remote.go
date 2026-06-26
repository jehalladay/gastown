package cmd

import (
	"fmt"

	"github.com/steveyegge/gastown/internal/crew"
	"github.com/steveyegge/gastown/internal/rig"
)

// runCrewStartRemote spawns a crew member's agent loop on a cluster node instead
// of a local tmux session — F2, the Tier-1 (host-up) memory-relief lever: the
// memory-heavy agent processes move off the local box while still reaching the
// host's Dolt over a host-initiated reverse tunnel (ssh -R over SSM). It does NOT
// need the cluster Dolt hub (that's Tier-2 / host-down persistence).
//
// Shape (smallest path): SSM-run a job on `node` that
//  1. exports the agent env (GT_ROLE/GT_RIG/GT_CREW/... same as a local crew
//     session) PLUS GT_DOLT_HOST pointed at the reverse-tunnel endpoint, so the
//     node's bd/gt-mail reach the host Dolt (GT_DOLT_HOST already routes the data
//     plane end-to-end — internal/beads translates it to BEADS_DOLT_SERVER_HOST);
//  2. launches the agent loop persistently (the node must keep a long-lived
//     process — nohup/systemd-run/tmux on the node, not a run-and-exit job).
//
// Two contracts this depends on (being settled with eng_sr2 + offload_ops):
//   - TUNNEL (eng_sr2): the value GT_DOLT_HOST must take once the host-initiated
//     reverse tunnel is up (e.g. localhost:<remote-forwarded-port>), and who owns
//     tunnel open/reconnect lifecycle.
//   - NODE (offload_ops): the warm node's instance ID + that the agent toolchain
//     (claude/RC + gt + bd + uv + creds) is staged there, and how a long-lived
//     process is kept alive past the SSM run-command timeout.
//
// Until both land, this returns a clear not-yet-wired error rather than guessing
// their interface. The flag, arg-plumbing, single-target guard, and worker/session
// resolution below are contract-independent and ready.
func runCrewStartRemote(crewMgr *crew.Manager, r *rig.Rig, name, node string) error {
	// Resolve (or create) the worker so a remote spawn has the same identity a
	// local one would — session name, rig, clone metadata.
	worker, err := crewMgr.Get(name)
	if err != nil {
		return fmt.Errorf("resolving crew member %q for remote spawn: %w", name, err)
	}
	sessionName := crewMgr.SessionName(name)

	// ponytail: spawn body is gated on the tunnel + node contracts (eng_sr2 +
	// offload_ops). Wiring it now would hardcode a guessed interface; the seam is
	// ready to fill the instant those land. Tracked: F2 task + channel:offload-persistence.
	return fmt.Errorf(
		"gt crew start --remote: spawn scaffold ready for %s/%s (session %s) on node %s, "+
			"but the reverse-tunnel endpoint (eng_sr2) and warm-node toolchain/lifecycle (offload_ops) "+
			"contracts are not yet wired — coordinating on channel:offload-persistence",
		r.Name, worker.Name, sessionName, node)
}
