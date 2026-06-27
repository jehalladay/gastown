package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/steveyegge/gastown/internal/config"
	"github.com/steveyegge/gastown/internal/crew"
	"github.com/steveyegge/gastown/internal/offload"
	"github.com/steveyegge/gastown/internal/rig"
	"github.com/steveyegge/gastown/internal/session"
	"github.com/steveyegge/gastown/internal/style"
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

	// bdV52Commit is the beads build commit that carries the v52 schema (the same
	// 7f6752c8f pinned in gt's go.mod by the bd-v52 merge + the host-installed bd).
	// `bd version` embeds it; the spawn guard requires it on the node so a stale v49
	// bd can't half-write the v52 town DB. The version STRING (1.0.5) is identical
	// for v49/v52, so the commit is the reliable discriminator.
	bdV52Commit = "7f6752c8"
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

	// Extract the embedded script suite (open-remote-tunnel.sh + ssm-run.sh) to a
	// tempdir and drive it — the scripts are the source of truth for the ssh-R/SSM
	// mechanics; gt computes identity/env + orchestrates.
	scriptDir, err := offload.ExtractScripts()
	if err != nil {
		return fmt.Errorf("extracting remote-spawn scripts: %w", err)
	}
	defer func() { _ = os.RemoveAll(scriptDir) }()

	plan := buildRemoteSpawnPlan(node, worker.Name, scriptDir, env, startupCmd, sessionName)

	// 1. Open the host-initiated reverse tunnel in the background (keepalive loop).
	//    The agent's bd/gt-mail reach the host Dolt through it (GT_DOLT_PORT=13307).
	fmt.Printf("→ Opening reverse tunnel to %s (node:%s → host Dolt)...\n", node, remoteDoltFwdPort)
	tunnel, tunnelLog, err := offload.StartTunnel(scriptDir, node, remoteDoltFwdPort, tunnelKey)
	if err != nil {
		return fmt.Errorf("opening reverse tunnel: %w", err)
	}
	fmt.Printf("  Tunnel keepalive log: %s\n", tunnelLog)
	// The tunnel must outlive this command (the remote agent uses it for its whole
	// life). Release it rather than kill on return; lifecycle/keepalive is the
	// tunnel script's job. ponytail: detach — a host-side tunnel supervisor (gt
	// daemon) owning these is the upgrade path if we need to reap them.
	if tunnel.Process != nil {
		_ = tunnel.Process.Release()
	}

	// 2. Launch the agent loop on the node via SSM (systemd-run --scope, survives
	//    the send-command exit). The crew clone is at /opt/gastown/<crew> (staged by
	//    provision --agent --crew); run the scope from there.
	//
	//    GUARD (spawn-critical): assert the node's bd writes the v52 schema BEFORE
	//    launching. A stale v49 bd HALF-WRITES against the v52 town DB (the exact
	//    corruption we're fixing — it half-wrote reactivecli-9ncn during the dogfood
	//    when --agent skipped replacing a pre-existing v49 bd). Fail loud here rather
	//    than let the agent's first bd corrupt a bead. offload_ops' --agent adds a
	//    node-side version-check too; this is gt-side defense-in-depth.
	launch := fmt.Sprintf(
		"set -e\n"+
			// Export the node toolchain PATH FIRST so `bd` resolves (it's at
			// .local/bin, not the SSM root shell's bare PATH) — the guard below + the
			// systemd-run both need it.
			"export PATH=%s:$PATH\n"+
			"command -v bd >/dev/null || { echo '[remote-spawn] FATAL: bd not found on node (toolchain not staged?) — re-provision with --agent'; exit 75; }\n"+
			"BDV=$(bd version 2>/dev/null || true)\n"+
			"echo \"[remote-spawn] node bd: $BDV\"\n"+
			// v52 guard: a stale v49 bd HALF-WRITES against the v52 town DB. The bd
			// version STRING is still 1.0.5 for both, but `bd version` embeds the build
			// COMMIT — the v52 build is commit 7f6752c8 (same as the town's bd-v52
			// merge). Accept only if that commit is present. (bd migrate status needs a
			// DB so it can't gate here; the commit is the reliable binary-identity check.)
			"echo \"$BDV\" | grep -q '%s' || { "+
			"echo '[remote-spawn] FATAL: node bd is not the v52 build (commit %s) — a stale v49 bd would half-write the v52 town DB; re-provision with --agent v52 guard'; exit 75; }\n"+
			"cd %s/%s && %s\n",
		remoteNodePATH, bdV52Commit, bdV52Commit, remoteNodeHome, worker.Name, shellJoin(plan.SystemdRun))
	fmt.Printf("→ Launching agent loop on %s (session %s)...\n", node, sessionName)
	out, err := offload.SSMRun(scriptDir, node, launch, "120")
	if err != nil {
		return fmt.Errorf("launching remote agent on %s: %w\n%s", node, err, out)
	}

	fmt.Printf("%s Remote crew agent %s/%s launched on %s (session %s)\n",
		style.Bold.Render("✓"), r.Name, worker.Name, node, sessionName)
	fmt.Printf("  Dolt via tunnel: %s:%s | HOME: %s/%s\n",
		env["GT_DOLT_HOST"], env["GT_DOLT_PORT"], remoteNodeHome, worker.Name)
	if strings.TrimSpace(out) != "" {
		fmt.Printf("  Node output:\n%s\n", indentLines(out, "    "))
	}
	return nil
}

// shellJoin renders argv as a single shell-safe command line (each arg quoted).
func shellJoin(argv []string) string {
	quoted := make([]string, len(argv))
	for i, a := range argv {
		quoted[i] = shellQuote(a)
	}
	return strings.Join(quoted, " ")
}

// shellQuote single-quotes a shell argument (POSIX: wrap in '...', escaping any
// embedded single quote as '\''). Safe for the startup command + --setenv values.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// indentLines prefixes each line of s with prefix (for readable node output).
func indentLines(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
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

// remoteAgentStartupCommand builds the agent startup command the NODE launches.
//
// It deliberately does NOT reuse gt's local BuildStartupCommand* — that resolves
// the agent binary to the HOST's absolute path (e.g. /Users/.../​.toolbox/bin/claude,
// which doesn't exist on the node -> exec status 127) and prepends an `env KEY=VAL`
// block carrying the HOST base env (which double-set GT_DOLT_PORT=3307, defeating
// the tunnel overlay). Both were dogfood-found bugs. Instead this emits a
// node-resolvable command: bare `claude` (found via the node toolchain PATH the
// launch exports) + the Gas Town flags + the startup beacon. Env comes ONLY from
// the systemd --setenv list (which carries the 13307 tunnel overlay) — no embedded
// env prefix. --dangerously-skip-permissions matches a local crew start; settings/
// hooks resolve from the node's own crew clone cwd.
func remoteAgentStartupCommand(rigName, crewName, rigPath, sessionName string) (string, error) {
	beacon := session.FormatStartupBeacon(session.BeaconConfig{
		Recipient: session.BeaconRecipient("crew", crewName, rigName),
		Sender:    "human",
		Topic:     "start",
	})
	// bare `claude` (PATH-resolved on the node), skip-permissions like local crew,
	// beacon as the startup prompt. shellQuote the beacon (it has spaces/brackets).
	return "claude --dangerously-skip-permissions " + shellQuote(beacon), nil
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

// remoteNodePATH is prepended so the agent resolves its staged toolchain: claude
// lives under .npm-global/bin, gt+bd under .local/bin, node under node/bin — none
// on the bare PATH (offload_ops verified; without it `claude` won't resolve).
const remoteNodePATH = remoteNodeHome + "/.local/bin:" + remoteNodeHome + "/node/bin:" + remoteNodeHome + "/.npm-global/bin"

// buildRemoteSpawnPlan assembles the spawn commands for crewName on node, against
// offload_ops' verified step-4 form — a transient systemd SERVICE (not --scope):
//
//	sudo systemd-run --unit=gt-crew-<crew>-<unit> --property=Restart=on-failure \
//	  --setenv=KEY=VALUE... sh -lc 'export PATH=...; <startupCmd>'
//
// --unit (service) over --scope: a long-lived crew agent needs crash-restart
// (Restart=on-failure) + a clean `systemctl stop gt-crew-<crew>` kill handle;
// --scope has neither and dies on reboot. unitSuffix makes the unit stable+unique
// (injected — Date.now is unavailable here). The agent env (incl HOME + the tunnel
// Dolt overlay) becomes --setenv flags; the startup runs under sh -lc with the node
// toolchain PATH exported first.
func buildRemoteSpawnPlan(node, crewName, scriptDir string, env map[string]string, startupCmd, unitSuffix string) remoteSpawnPlan {
	tunnelScript := filepath.Join(scriptDir, "open-remote-tunnel.sh")
	provisionScript := filepath.Join(scriptDir, "provision-node.sh")

	systemd := []string{"sudo", "systemd-run",
		"--unit=gt-crew-" + crewName + "-" + unitSuffix,
		"--property=Restart=on-failure",
		// Run as the node login user (ubuntu), NOT root: claude refuses
		// --dangerously-skip-permissions under root/sudo, and ubuntu owns the
		// toolchain + tunnel key. (Dogfood-found: root launch exited status 1.)
		"--uid=" + remoteNodeUser}
	// HOME is the node's staged toolchain root; ensure it's set even if env omits it.
	if _, ok := env["HOME"]; !ok {
		systemd = append(systemd, "--setenv=HOME="+remoteNodeHome)
	}
	for _, kv := range remoteEnvAssignments(env) {
		systemd = append(systemd, "--setenv="+kv)
	}
	// Export the node toolchain PATH inside the launched shell so `claude` resolves.
	systemd = append(systemd, "sh", "-lc", "export PATH="+remoteNodePATH+":$PATH; "+startupCmd)

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
