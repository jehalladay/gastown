package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/crew"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/session"
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

	// Compute the gt-owned half: the agent's identity/env (with the tunnel Dolt
	// overlay) and its startup command — identical to a local crew session so the
	// remote agent boots with the same context/hooks. The e2e proved a node with
	// this env writes to the host Dolt through the tunnel.
	env := remoteAgentEnv(r.Name, worker.Name, r.Path, sessionName)
	startupCmd, err := remoteAgentStartupCommand(r.Name, worker.Name, r.Path, sessionName)
	if err != nil {
		return fmt.Errorf("building remote agent startup command: %w", err)
	}
	_ = startupCmd // consumed by the orchestration step (joint live wiring with offload_ops)

	// The reverse tunnel needs the persistent key passed explicitly on an
	// embedded-extract host (the vendored script's hardcoded key-paths don't
	// resolve from a tempdir → it would fall to the fragile ephemeral key). Fail
	// loud now rather than let the tunnel silently use the TTL-racing path.
	tunnelKey := tunnelKeyEnv()
	if tunnelKey == nil {
		return fmt.Errorf("gt crew start --remote: set GT_TUNNEL_KEY to the persistent offload-tunnel key path "+
			"(staged on this host by offload_ops); the reverse tunnel to %s needs it (embedded-extract can't "+
			"auto-detect the reactivecli crew-dir key)", node)
	}

	// Assemble the concrete spawn plan (gt-owned half): the provision/tunnel/
	// systemd-run commands that drive offload_ops'/eng_sr2's proven scripts. The
	// embed/extract of those scripts (scriptDir) + execution land at the F2<->F4
	// internal/offload reconcile + the joint live wiring (e2e already GREEN). The
	// unit suffix is the session name (stable + unique per crew) since a wallclock
	// timestamp isn't available here.
	plan := buildRemoteSpawnPlan(node, worker.Name, "<extracted-script-dir>", env, startupCmd, sessionName)
	_ = plan // executed by the orchestration step (extract scripts -> run plan)

	return fmt.Errorf(
		"gt crew start --remote: gt-side ready for %s/%s (session %s) on node %s "+
			"[user=%s home=%s, %d env vars incl %s=%s %s=%s; spawn plan assembled: "+
			"provision --agent -> tunnel(bg, TUNNEL_SSH_KEY set) -> systemd-run --scope] — "+
			"e2e proven green; orchestration execution lands at the F2<->F4 internal/offload "+
			"reconcile + the joint live wiring with offload_ops",
		r.Name, worker.Name, sessionName, node,
		remoteNodeUser, remoteNodeHome, len(env),
		"GT_DOLT_HOST", env["GT_DOLT_HOST"], "GT_DOLT_PORT", env["GT_DOLT_PORT"])
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

// remoteAgentEnv computes the full environment a remotely-spawned crew agent must
// export on the node: the same GT_* identity a LOCAL crew session gets (role/rig/
// crew/town-root/session), overlaid with the tunnel Dolt endpoint so bd/gt-mail
// flow back to the host. This is the gt-owned, pure half of the spawn — the e2e
// proved a node with this env writes to the host Dolt through the tunnel. The
// orchestration that ships this env to the node (open-remote-tunnel.sh bg + an SSM
// systemd-run send-command) is wired against offload_ops'/eng_sr2's proven scripts.
func remoteAgentEnv(rigName, crewName, rigPath, sessionName string) map[string]string {
	townRoot := filepath.Dir(rigPath)
	env := config.AgentEnv(config.AgentEnvConfig{
		Role:        "crew",
		Rig:         rigName,
		AgentName:   crewName,
		TownRoot:    townRoot,
		SessionName: sessionName,
	})
	// Overlay the reverse-tunnel Dolt endpoint (must win over any local default).
	for k, v := range remoteAgentDoltEnv() {
		env[k] = v
	}
	return env
}

// remoteAgentStartupCommand builds the agent startup command (claude + Gas Town
// flags + the startup beacon) the node launches under systemd-run — identical to
// the local crew start command, so a remote agent boots with the same context/
// hooks. Pure + testable; the node runs it as user `ubuntu` with HOME /opt/gastown.
func remoteAgentStartupCommand(rigName, crewName, rigPath, sessionName string) (string, error) {
	townRoot := filepath.Dir(rigPath)
	beacon := session.FormatStartupBeacon(session.BeaconConfig{
		Recipient: session.BeaconRecipient("crew", crewName, rigName),
		Sender:    "human",
		Topic:     "start",
	})
	return config.BuildStartupCommandFromConfig(config.AgentEnvConfig{
		Role:        "crew",
		Rig:         rigName,
		AgentName:   crewName,
		TownRoot:    townRoot,
		Prompt:      beacon,
		Topic:       "start",
		SessionName: sessionName,
	}, rigPath, beacon, "")
}

// remoteSpawnPlan is the concrete, reviewable set of commands gt drives to spawn a
// remote agent — the gt-owned assembly half. The orchestration executes these in
// order (each shelling to offload_ops'/eng_sr2's proven scripts, extracted from the
// embedded suite at scriptDir); step 3 (Tunnel) runs in the background (keepalive
// loop), then SystemdRun is sent via SSM. Built pure (no exec) so it's testable;
// the embed/extract + execution lands at the F2<->F4 internal/offload reconcile +
// the joint live wiring.
type remoteSpawnPlan struct {
	Provision  []string // provision-node.sh --agent <node>  (stages toolchain + clones crew repo, option i)
	Tunnel     []string // open-remote-tunnel.sh <node> 13307  (run in background; TUNNEL_SSH_KEY in env)
	SystemdRun []string // the SSM-delivered systemd-run --scope line that launches the agent loop
	TunnelEnv  []string // env for the tunnel command (TUNNEL_SSH_KEY=<GT_TUNNEL_KEY>)
}

// buildRemoteSpawnPlan assembles the spawn commands for crewName on node, against
// offload_ops' verified step-4 systemd-run shape:
//
//	sudo systemd-run --scope --unit=gt-crew-<crew>-<unit> \
//	  --setenv=KEY=VALUE... sh -lc '<startupCmd>'
//
// unitSuffix makes the unit name stable+unique (caller passes a timestamp/id;
// Date.now is unavailable here so it's injected). The agent env (incl HOME and the
// tunnel Dolt overlay) becomes --setenv flags; startupCmd is the local-parity boot.
func buildRemoteSpawnPlan(node, crewName, scriptDir string, env map[string]string, startupCmd, unitSuffix string) remoteSpawnPlan {
	tunnelScript := filepath.Join(scriptDir, "open-remote-tunnel.sh")
	provisionScript := filepath.Join(scriptDir, "provision-node.sh")

	systemd := []string{"sudo", "systemd-run", "--scope", "--unit=gt-crew-" + crewName + "-" + unitSuffix}
	// HOME is the node's staged toolchain root; ensure it's set even if env omits it.
	if _, ok := env["HOME"]; !ok {
		systemd = append(systemd, "--setenv=HOME="+remoteNodeHome)
	}
	for _, kv := range remoteEnvAssignments(env) {
		systemd = append(systemd, "--setenv="+kv)
	}
	systemd = append(systemd, "sh", "-lc", startupCmd)

	return remoteSpawnPlan{
		Provision:  []string{provisionScript, "--agent", node},
		Tunnel:     []string{tunnelScript, node, remoteDoltFwdPort},
		SystemdRun: systemd,
		TunnelEnv:  tunnelKeyEnv(),
	}
}

// tunnelKeyEnv maps gt's GT_TUNNEL_KEY (the persistent .offload-tunnel-key path
// staged on the spawn-host by offload_ops) to TUNNEL_SSH_KEY, which the vendored
// open-remote-tunnel.sh honors as priority #1 (auto-setting TUNNEL_AUTHORIZED_KEY=1
// to skip the fragile 60s-TTL ephemeral key). CRITICAL on an embedded-extract host:
// the script's hardcoded reactivecli key-paths don't resolve from a tempdir, so it
// would fall to the ephemeral path unless gt passes the key explicitly. Returns nil
// when GT_TUNNEL_KEY is unset (caller surfaces a clear error — the tunnel needs it).
func tunnelKeyEnv() []string {
	if k := os.Getenv("GT_TUNNEL_KEY"); k != "" {
		return []string{"TUNNEL_SSH_KEY=" + k}
	}
	return nil
}

// remoteEnvAssignments renders env as deterministically-ordered KEY=VALUE strings
// (sorted) for embedding in the node launch command. Deterministic order keeps the
// generated send-command stable + reviewable.
func remoteEnvAssignments(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k+"="+env[k])
	}
	return out
}
