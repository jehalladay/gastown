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
	// the agent toolchain (gt linux/amd64 + claude + bd) and the crew HOME/clone are
	// staged under /opt/gastown, runnable by root via SSM. The agent loop is launched
	// with `systemd-run --scope` (verified to survive the SSM run-command exit — the
	// SSM timeout does not bound the agent); nohup/setsid/tmux also present as fallbacks.
	//
	// The LOGIN USER varies across the fleet (Ubuntu AMIs = ubuntu, Amazon Linux =
	// ec2-user), so it is NOT a fixed const — resolveRemoteNodeUser() reads
	// GT_REMOTE_USER (offload_ops sets it per node from the node's login_user marker)
	// and defaults to ubuntu. A hardcoded user fails `sudo -u ubuntu` -> "unknown user
	// ubuntu" on the ec2-user wave nodes (dogfood-found at first wave spawn).
	remoteNodeUserDefault = "ubuntu"
	remoteNodeHome        = "/opt/gastown"

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

	// GT_SKIP_TUNNEL=1: the verb does NOT open its own reverse tunnel — an external
	// persistent per-node tunnel already provides the data plane. At scale the verb's
	// per-spawn tunnel is redundant AND harmful: every spawn on a node adds another
	// keepalive loop racing the same node:13307 (ExitOnForwardFailure lets only one bind;
	// the rest spin on failed binds = SSM-connect churn — offload_ops saw 44 redundant
	// tunnels + host load 15.8 after 32 migrations). When offload_ops' batch model owns
	// the durable per-node tunnel, skip ours (hq-wisp-zlzb7q, option b). Default (unset)
	// keeps the self-tunnel — the single-spawn path the L8 proved.
	skipTunnel := os.Getenv("GT_SKIP_TUNNEL") == "1"

	// The reverse tunnel needs the persistent key passed explicitly on an
	// embedded-extract host (the vendored script's hardcoded key-paths don't
	// resolve from a tempdir → it would fall to the fragile ephemeral key). Fail
	// loud now rather than let the tunnel silently use the TTL-racing path. Only
	// required when WE open the tunnel — skip the check under GT_SKIP_TUNNEL.
	var tunnelKey []string
	if !skipTunnel {
		tunnelKey = tunnelKeyEnv()
		if tunnelKey == nil {
			return fmt.Errorf("gt crew start --remote: set GT_TUNNEL_KEY to the persistent offload-tunnel key path "+
				"(staged on this host by offload_ops); the reverse tunnel to %s needs it (embedded-extract can't "+
				"auto-detect the reactivecli crew-dir key). Or set GT_SKIP_TUNNEL=1 if an external per-node "+
				"tunnel already provides the data plane", node)
		}
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

	// 0. Provision the node FIRST: stage toolchain + clone/prime the crew workspace at
	//    /opt/gastown/<crew> (with .beads redirect + claude config). WITHOUT this the agent
	//    launches bare with no clone -> bd has no DB, claude has no identity (the
	//    prod-confirmed no-Provision bug, hq-wwxq: runCrewStartRemote built plan.Provision
	//    but never ran it). provision-node.sh --agent --crew is idempotent, so re-spawning
	//    an already-provisioned node just re-syncs. Blocking — the tunnel + launch below
	//    depend on the clone existing.
	fmt.Printf("→ Provisioning %s (toolchain + crew clone /opt/gastown/%s)...\n", node, worker.Name)
	// provision reads AWS creds from the inherited env + generates/uses its own persistent
	// tunnel key under OFFLOAD_STATE_DIR; it needs no TUNNEL_SSH_KEY overlay (that's the
	// tunnel script's input).
	provOut, err := offload.RunProvision(scriptDir, node, worker.Name, "", "", nil)
	if err != nil {
		return fmt.Errorf("provisioning %s for crew %s: %w\n%s", node, worker.Name, err, provOut)
	}

	// 1. Open the host-initiated reverse tunnel in the background (keepalive loop).
	//    The agent's bd/gt-mail reach the host Dolt through it (GT_DOLT_PORT=13307).
	//    Skipped under GT_SKIP_TUNNEL=1 — an external persistent per-node tunnel owns
	//    the data plane; opening ours would race its :13307 bind (see skipTunnel above).
	if skipTunnel {
		fmt.Printf("→ Skipping verb tunnel (GT_SKIP_TUNNEL=1) — relying on an external "+
			"persistent tunnel for node:%s → host Dolt\n", remoteDoltFwdPort)
	} else {
		fmt.Printf("→ Opening reverse tunnel to %s (node:%s → host Dolt)...\n", node, remoteDoltFwdPort)
		tunnel, tunnelLog, terr := offload.StartTunnel(scriptDir, node, remoteDoltFwdPort, tunnelKey)
		if terr != nil {
			return fmt.Errorf("opening reverse tunnel: %w", terr)
		}
		fmt.Printf("  Tunnel keepalive log: %s\n", tunnelLog)
		// The tunnel must outlive this command (the remote agent uses it for its whole
		// life). Release it rather than kill on return; lifecycle/keepalive is the
		// tunnel script's job. ponytail: detach — a host-side tunnel supervisor (gt
		// daemon) owning these is the upgrade path if we need to reap them.
		if tunnel.Process != nil {
			_ = tunnel.Process.Release()
		}
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
	// tmux invoked as the node login user (matches plan.Launch's new-session user),
	// so has-session/send-keys hit the SAME tmux server (/tmp/tmux-<uid>).
	launch := buildRemoteLaunchScript(node, worker.Name, sessionName, plan.Launch, remoteAgentBeacon(r.Name, worker.Name))
	fmt.Printf("→ Launching persistent agent (tmux) on %s (session %s)...\n", node, sessionName)
	out, err := offload.SSMRun(scriptDir, node, launch, "120")
	if err != nil {
		return fmt.Errorf("launching remote agent on %s: %w\n%s", node, err, out)
	}

	// Record the node so the remote agent is inspectable host-side (gt crew status)
	// without sshing it — F2 observability. Non-fatal: the agent is already up.
	if rnErr := crewMgr.SetRemoteNode(worker.Name, node); rnErr != nil {
		style.PrintWarning("agent launched but could not record remote node for %s (gt crew status won't show it): %v", worker.Name, rnErr)
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

// buildRemoteLaunchScript builds the SSM-delivered bash that runs on the node to stand
// up the persistent agent. Pure (no exec) so the guards are unit-testable. It asserts,
// in order: bd present, bd is the v52 build, and the crew clone exists WITH .beads —
// each fails loud (distinct exit code) rather than launching a broken agent. Then it
// starts the detached tmux session (launchArgv) as the node login user and types the
// startup beacon into the live REPL.
func buildRemoteLaunchScript(node, crewName, sessionName string, launchArgv []string, beacon string) string {
	nodeTmux := "sudo -u " + resolveRemoteNodeUser() + " env PATH=" + remoteNodePATH + ":/usr/bin:/bin HOME=" + remoteNodeHome + " tmux"

	// The agent's cwd MUST be its provisioned crew clone (with .beads), or bd from the
	// agent's cwd fails "no beads database found" and claude has no identity/CLAUDE.md —
	// exactly the dogfood failure (hq-wwxq gaps #1+#2): claude launched bare in $HOME with
	// no clone. The clone is staged by offload_ops' `provision-node.sh --agent --crew
	// <name>` (their lane, not vendored). gt does not own that script, so it asserts the
	// clone is present and FAILS LOUD with the exact provision command if not — same
	// defense-in-depth shape as the bd guard.
	cloneDir := remoteNodeHome + "/" + crewName
	provisionHint := fmt.Sprintf("provision-node.sh --agent --crew %s %s", crewName, node)

	// The .claude/settings.json install, run AS THE LOGIN USER (see below). Build it as one
	// inner script we shell-quote into `sudo -u <user> bash -c '<this>'`. Single-quoted
	// heredoc body so nothing inside expands.
	settingsInstall := "install -d -m 700 " + cloneDir + "/.claude && cat > " + cloneDir + "/.claude/settings.json <<'GTSETTINGS'\n" +
		"{\n" +
		"  \"permissions\": { \"defaultMode\": \"bypassPermissions\" },\n" +
		"  \"hooks\": {\n" +
		"    \"SessionStart\": [ { \"matcher\": \"\", \"hooks\": [ { \"type\": \"command\", \"command\": \"gt prime --hook\" } ] } ],\n" +
		"    \"PreCompact\": [ { \"matcher\": \"\", \"hooks\": [ { \"type\": \"command\", \"command\": \"gt prime --hook\" } ] } ]\n" +
		"  }\n" +
		"}\n" +
		"GTSETTINGS"

	return fmt.Sprintf(
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
			// Crew-clone guard (gap #1/#2): the agent cwd must be a provisioned clone WITH
			// .beads, else bd has no DB + claude has no identity. Fail loud with the exact
			// provision command rather than launch a rootless, identity-less REPL.
			"test -d %s/.beads || { echo '[remote-spawn] FATAL: crew clone %s missing or has no .beads — the node is not provisioned for this crew. Run: %s'; exit 78; }\n"+
			// gap #2 (prime): install the SessionStart hook into the clone's
			// .claude/settings.json so claude runs `gt prime --hook` on startup (+ PreCompact
			// so it re-primes after compaction, not just first session). gt prime takes the
			// off-town env-only path on the node (no local town; identity from GT_ROLE/GT_RIG/
			// GT_CREW). Without this the agent launches bare (no identity) — the dogfood-found
			// gap. MUST be written AS THE LOGIN USER (this SSM script runs as root): claude
			// runs as the login user and can't read a root-owned drwx------ .claude/, so a
			// root write makes the hook silently unreadable. bypassPermissions matches the
			// local crew settings; `gt` resolves on the node PATH the user's tmux exports.
			"sudo -u %s bash -c %s\n"+
			"echo '[remote-spawn] installed .claude/settings.json (SessionStart: gt prime --hook)'\n"+
			// Launch the persistent agent in a detached tmux session (bare interactive
			// claude — -c sets cwd; survives the SSM exit; PTY keeps it a loop). Runs
			// as the login user (the tmux + has-session + send-keys must share the same
			// user's tmux server, /tmp/tmux-<uid>).
			"%s\n"+
			"%s has-session -t %s 2>/dev/null || { echo '[remote-spawn] FATAL: agent tmux session did not start (claude exited?)'; exit 76; }\n"+
			// Let claude's REPL come up, then type the startup beacon (the "start"
			// nudge) into the live pane — mirrors local crew (interactive claude +
			// beacon sent via send-keys). claude with a prompt ARG would one-shot+exit.
			"sleep 8\n"+
			"%s has-session -t %s 2>/dev/null || { echo '[remote-spawn] FATAL: agent exited within 8s (not persistent)'; exit 77; }\n"+
			"%s send-keys -t %s -l %s\n"+
			"%s send-keys -t %s Enter\n"+
			"echo '[remote-spawn] persistent agent live + beacon sent: %s'\n",
		remoteNodePATH, bdV52Commit, bdV52Commit,
		cloneDir, cloneDir, provisionHint,
		resolveRemoteNodeUser(), shellQuote(settingsInstall), // sudo -u <user> bash -c '<settings install>'
		shellJoin(launchArgv),
		nodeTmux, sessionName,
		nodeTmux, sessionName,
		nodeTmux, sessionName, shellQuote(beacon),
		nodeTmux, sessionName, sessionName)
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
// export so its bd/gt-mail reach the host's Dolt through the reverse tunnel.
//
// MUST set BOTH the GT_DOLT_* AND the BEADS_DOLT_* vars. The GT_DOLT_* -> BEADS_DOLT_*
// translation (internal/beads translateDoltPort) only fires for bd SUBPROCESSES gt
// spawns — but a remote crew's INTERACTIVE bd (typed into the tmux REPL) reads
// BEADS_DOLT_* straight from its session env. config.AgentEnv seeds the base env with
// BEADS_DOLT_PORT=3307 (the node's local, where no Dolt runs), so we must override it
// to the tunnel port here, or the agent's bd hits the node's empty 3307 and fails
// "no Dolt". (Dogfood-found: agent booted but its bd couldn't reach the host DB.)
func remoteAgentDoltEnv() map[string]string {
	return map[string]string{
		"GT_DOLT_HOST":           remoteDoltHost,
		"GT_DOLT_PORT":           remoteDoltFwdPort,
		"BEADS_DOLT_SERVER_HOST": remoteDoltHost,
		"BEADS_DOLT_PORT":        remoteDoltFwdPort,
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
	// GT_ROOT is left UNSET (TownRoot=""): the dogfood (gap #4) found GT_ROOT=/Users/.../gt
	// — the HOST town root — baked into the node session, a path that does not exist there
	// and misroutes. offload_ops' live-proven remote-crew path (13+ runs) sets NO GT_ROOT:
	// gt resolves identity from the agent's cwd (the /opt/gastown/<crew> clone, set by
	// buildRemoteSpawnPlan) + GT_CREW. Passing TownRoot="" makes AgentEnv omit both GT_ROOT
	// and GIT_CEILING_DIRECTORIES — and on the node the clone's parent (/opt/gastown) is not
	// a git repo, so there's no umbrella-walk to ceiling against. rigPath is intentionally
	// unused: its host-derived town root is exactly the wrong value here.
	_ = rigPath
	env := config.AgentEnv(config.AgentEnvConfig{
		Role:        "crew",
		Rig:         rigName,
		AgentName:   crewName,
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
	// bare `claude` (PATH-resolved on the node), skip-permissions like local crew —
	// but NO prompt arg: claude with a prompt arg runs it ONE-SHOT and exits (dogfood
	// bug #5). The persistent REPL needs claude started bare; the beacon is sent via
	// `tmux send-keys` AFTER it's up (mirrors local crew: interactive claude + the
	// beacon typed in). Probe-verified: bare claude stays alive in a node tmux pane.
	return "claude --dangerously-skip-permissions", nil
}

// remoteAgentBeacon is the startup beacon typed into the live claude REPL via
// tmux send-keys (the predecessor-discovery / "start" nudge), mirroring how local
// crew start seeds the interactive session.
func remoteAgentBeacon(rigName, crewName string) string {
	return session.FormatStartupBeacon(session.BeaconConfig{
		Recipient: session.BeaconRecipient("crew", crewName, rigName),
		Sender:    "human",
		Topic:     "start",
	})
}

// remoteSpawnPlan is the concrete, reviewable set of commands gt drives to spawn a
// remote agent — the gt-owned assembly half. The orchestration executes these in
// order (each shelling to offload_ops'/eng_sr2's proven scripts, extracted from the
// embedded suite at scriptDir); step 3 (Tunnel) runs in the background (keepalive
// loop), then SystemdRun is sent via SSM. Built pure (no exec) so it's testable;
// the embed/extract + execution lands at the F2<->F4 internal/offload reconcile +
// the joint live wiring.
type remoteSpawnPlan struct {
	Provision []string // provision-node.sh --agent <node>  (stages toolchain + clones crew repo, option i)
	Tunnel    []string // open-remote-tunnel.sh <node> 13307  (run in background; TUNNEL_SSH_KEY in env)
	Launch    []string // the SSM-delivered tmux-on-node line that starts the persistent agent session
	TunnelEnv []string // env for the tunnel command (TUNNEL_SSH_KEY=<GT_TUNNEL_KEY>)
}

// remoteNodePATH is prepended so the agent resolves its staged toolchain: claude
// lives under .npm-global/bin, gt+bd under .local/bin, node under node/bin — none
// on the bare PATH (offload_ops verified; without it `claude` won't resolve).
const remoteNodePATH = remoteNodeHome + "/.local/bin:" + remoteNodeHome + "/node/bin:" + remoteNodeHome + "/.npm-global/bin"

// buildRemoteSpawnPlan assembles the spawn commands for crewName on node. The
// agent launches in a DETACHED TMUX SESSION on the node — mirroring the LOCAL crew
// model (claude runs interactively in a tmux pane, a persistent PTY/REPL driven by
// hooks/nudges), NOT a headless systemd one-shot. The earlier systemd-run sh -lc
// form ran claude one-shot (it answered the beacon + exited) because there was no
// PTY; tmux gives the interactive session that keeps the agent alive. `tmux
// new-session -d` detaches so it survives the SSM command exit (like --scope did),
// runs as the node login user, and the agent's whole life is that tmux session
// (`tmux kill-session` is the clean stop handle).
//
// Shape: tmux new-session -d -s <session> -e KEY=VAL... -c <cwd> 'bash -lc "export
// PATH=<node toolchain>; exec <startupCmd>"'. Env via tmux -e (the --setenv-equiv);
// PATH exported in the inner shell so bare `claude` resolves; startupCmd is the
// node-safe bare-claude command. sessionName is the tmux session id (stable/unique).
func buildRemoteSpawnPlan(node, crewName, scriptDir string, env map[string]string, startupCmd, sessionName string) remoteSpawnPlan {
	tunnelScript := filepath.Join(scriptDir, "open-remote-tunnel.sh")
	provisionScript := filepath.Join(scriptDir, "provision-node.sh")
	cwd := remoteNodeHome + "/" + crewName

	// HOME defaults to the node toolchain root if env omits it.
	if _, ok := env["HOME"]; !ok {
		env = cloneEnv(env)
		env["HOME"] = remoteNodeHome
	}

	// Run tmux AS THE NODE LOGIN USER (resolveRemoteNodeUser — ubuntu or ec2-user
	// depending on the AMI), not root: the SSM shell is root, but claude refuses
	// --dangerously-skip-permissions under root, and the login user owns the toolchain
	// + tmux server (/tmp/tmux-<uid>). sudo -u <user>, preserving the node PATH + HOME
	// so tmux + the pane's claude resolve. (Probe-verified this exact form keeps claude
	// alive.)
	tmux := []string{"sudo", "-u", resolveRemoteNodeUser(),
		"env", "PATH=" + remoteNodePATH + ":/usr/bin:/bin", "HOME=" + remoteNodeHome,
		"tmux", "new-session", "-d", "-s", sessionName, "-c", cwd}
	for _, kv := range remoteEnvAssignments(env) {
		tmux = append(tmux, "-e", kv)
	}
	// The pane command: export the node toolchain PATH (so bare claude/gt/bd
	// resolve), then exec the node-safe startup. exec so the agent is the pane's
	// process (clean liveness + kill-session).
	tmux = append(tmux, "bash", "-lc",
		"export PATH="+remoteNodePATH+":$PATH; exec "+startupCmd)

	return remoteSpawnPlan{
		Provision: []string{provisionScript, "--agent", node},
		Tunnel:    []string{tunnelScript, node, remoteDoltFwdPort},
		Launch:    tmux,
		TunnelEnv: tunnelKeyEnv(),
	}
}

// cloneEnv returns a shallow copy so buildRemoteSpawnPlan can add HOME without
// mutating the caller's map.
func cloneEnv(env map[string]string) map[string]string {
	out := make(map[string]string, len(env)+1)
	for k, v := range env {
		out[k] = v
	}
	return out
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

// resolveRemoteNodeUser returns the node login user the spawn runs `sudo -u` as.
// Defaults to ubuntu but is overridable via GT_REMOTE_USER for Amazon-Linux nodes
// (ec2-user) — the fleet is mixed, so a hardcoded user fails on the other AMI.
func resolveRemoteNodeUser() string {
	if u := os.Getenv("GT_REMOTE_USER"); u != "" {
		return u
	}
	return remoteNodeUserDefault
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
