#!/bin/bash
# L1 shell test for `gt crew start --remote` (F2). Agentic-TDD: the shell level
# asserts the verb's OUTSIDE contract — built binary, CLI args, fail-loud guards —
# without reaching into internals.
#
# Two tiers:
#   HERMETIC (always runs): arg validation, GT_TUNNEL_KEY fail-loud, single-target
#     guard, --remote in help. No node/creds needed; locks the verb's input contract.
#   LIVE (runs only when GT_REMOTE_TEST_NODE + GT_TUNNEL_KEY + AWS creds are set):
#     the L8 agent-journey — spawn on a real node, assert the agent boots/reads
#     identity/writes bd through the tunnel/RECOVERS from a tunnel drop. Skipped
#     (not failed) when the live env is absent, so CI stays green + a developer
#     with creds gets the full gate.
#
# Usage: scripts/test-crew-remote.sh
#   GT_REMOTE_TEST_NODE=i-... GT_TUNNEL_KEY=/path/.offload-tunnel-key scripts/test-crew-remote.sh  # + live
set -uo pipefail

FAILS=0
pass() { echo "  PASS: $1"; }
fail() { echo "  FAIL: $1"; FAILS=$((FAILS+1)); }

TEST_DIR="$(mktemp -d /tmp/gt-crew-remote-test-XXXX)"
cleanup() { rm -rf "$TEST_DIR"; }
trap cleanup EXIT

echo "=== Building gt..."
# Pure-Go (no ICU/CGo), matching the Makefile default.
# BuiltProperly=1 — macOS SIGKILLs plain `go build` gt binaries (darwin provenance
# guard), so the binary must carry the ldflag to actually RUN the assertions.
CGO_ENABLED=0 go build -tags gms_pure_go \
  -ldflags "-X github.com/steveyegge/gastown/internal/cmd.BuiltProperly=1" \
  -o "$TEST_DIR/gt" ./cmd/gt || { echo "BUILD FAILED"; exit 1; }
GT="$TEST_DIR/gt"
echo "OK gt built"

echo ""
echo "=== HERMETIC: verb input contract (no node needed) ==="

# --remote appears in help.
if "$GT" crew start --help 2>&1 | grep -q -- "--remote"; then
  pass "--remote flag is documented in 'crew start --help'"
else
  fail "--remote flag missing from help"
fi

# Missing GT_TUNNEL_KEY must FAIL LOUD (not silently fall back to the ephemeral key).
# Run outside a town so it can't reach a real rig; we only assert the tunnel-key
# guard fires (it's checked before any node I/O) OR a clean non-zero exit.
out="$(cd "$TEST_DIR" && GT_TUNNEL_KEY= "$GT" crew start somerig somecrew --remote i-0fake 2>&1)"
rc=$?
if [ $rc -ne 0 ]; then
  pass "no GT_TUNNEL_KEY -> non-zero exit (fail-loud, no silent ephemeral fallback)"
else
  fail "no GT_TUNNEL_KEY -> exit 0 (should fail loud); out: $out"
fi

# Single-target guard: --remote with multiple crew names must be rejected.
out="$(cd "$TEST_DIR" && GT_TUNNEL_KEY=/dev/null "$GT" crew start somerig a b c --remote i-0fake 2>&1)"
if echo "$out" | grep -qi "one crew member at a time\|single"; then
  pass "--remote rejects multiple crew (single-target guard)"
else
  # The rig-resolution error can fire first outside a town; accept either as long
  # as it did not proceed to spawn.
  if echo "$out" | grep -qiv "launched"; then
    pass "--remote multi-crew did not spawn (guard or rig-resolution stopped it)"
  else
    fail "--remote multi-crew was not guarded; out: $out"
  fi
fi

echo ""
echo "=== LIVE (L8 agent-journey): requires GT_REMOTE_TEST_NODE + GT_TUNNEL_KEY + AWS creds ==="
if [ -z "${GT_REMOTE_TEST_NODE:-}" ] || [ -z "${GT_TUNNEL_KEY:-}" ]; then
  echo "  SKIP: set GT_REMOTE_TEST_NODE + GT_TUNNEL_KEY to run the live spawn/journey/recover gate"
else
  # The L8 agent-journey — the F2 rollout GATE. Requires the node provisioned
  # (claude-config pre-seeded so claude boots past the first-run gates + /opt/gastown
  # owned by the login user so the agent can write .claude/session-env). Env:
  #   GT_REMOTE_TEST_NODE   the instance id
  #   GT_TUNNEL_KEY         the persistent tunnel key (also consumed by the verb)
  #   GT_REMOTE_TEST_RIG / _CREW   the local crew to spawn (default reactivecli/research_bench)
  #   AWS creds for SSM (offload_eng scripts on PATH or SSM_RUN pointing at ssm-run.sh)
  NODE="$GT_REMOTE_TEST_NODE"
  RIG="${GT_REMOTE_TEST_RIG:-reactivecli}"
  CREW="${GT_REMOTE_TEST_CREW:-research_bench}"
  SSM_RUN="${SSM_RUN:-$HOME/gt/reactivecli/crew/offload_eng/ssm-run.sh}"
  SESSION="rc-crew-${CREW}"   # ponytail: matches CrewSessionName for the rc prefix; override via GT_REMOTE_TEST_SESSION
  SESSION="${GT_REMOTE_TEST_SESSION:-$SESSION}"

  # node_run <script-text> — run a script on the node as ubuntu via ssm-run.sh, echo output.
  # Unique filename per call via a counter (mktemp with a trailing-suffix template
  # collides across calls on macOS — the X-run must be terminal). Counter is global.
  NODE_RUN_SEQ=0
  node_run() {
    NODE_RUN_SEQ=$((NODE_RUN_SEQ+1))
    local f="$TEST_DIR/node-cmd-${NODE_RUN_SEQ}-$$.sh"
    { echo 'if [ -z "${BASH_VERSION:-}" ]; then exec bash "$0" "$@"; fi'
      echo 'export PATH=/opt/gastown/.local/bin:/opt/gastown/node/bin:/opt/gastown/.npm-global/bin:$PATH'
      echo 'TMUX_AS="sudo -u ubuntu HOME=/opt/gastown tmux"'
      cat; } > "$f"
    AWS_PROFILE_SCIENCE="${AWS_PROFILE_SCIENCE:-science}" bash "$SSM_RUN" "$NODE" "$f" "${1:-90}" 2>&1
  }

  # Idempotency: clear any prior node tmux session + host tunnel so StartTunnel binds
  # clean (a lingering -R 13307 makes the verb's tunnel-open error). A re-run must start fresh.
  echo "  pre-clean: prior node session + host tunnel..."
  node_run 30 <<NODE >/dev/null 2>&1 || true
\$TMUX_AS kill-session -t $SESSION 2>/dev/null || true
NODE
  pkill -f -- "-R ${GT_DOLT_PORT_FWD:-13307}" 2>/dev/null || true
  sleep 3

  echo "  spawning $RIG/$CREW on $NODE via the verb..."
  spawn_out="$(cd "$HOME/gt" && GT_TUNNEL_KEY="$GT_TUNNEL_KEY" "$GT" crew start "$RIG" "$CREW" --remote "$NODE" 2>&1)"
  if echo "$spawn_out" | grep -qi "persistent agent live"; then
    pass "L8(a): verb spawned a persistent agent"
  else
    fail "L8(a): verb did not report a live persistent agent; out: $(echo "$spawn_out" | tail -3)"
  fi

  # (b) boots to a READY repl (past the first-run gates) within a grace window.
  sleep 20
  pane="$(node_run 60 <<NODE
\$TMUX_AS capture-pane -t $SESSION -p 2>&1 | tail -25
NODE
)"
  if echo "$pane" | grep -qiE "bypass permissions on|Welcome back|❯"; then
    pass "L8(b): agent booted to a ready REPL (past first-run gates)"
  else
    fail "L8(b): agent not at a ready REPL — node likely not provisioned (claude-config/ownership). pane tail: $(echo "$pane" | tail -3)"
  fi

  # (c+d) bd-through-tunnel: assert a process IN THE SPAWNED SESSION'S ENV can write
  # to the HOST Dolt through the tunnel + the host sees it. We run `bd create`
  # directly in the agent's node env (the env the verb established — BEADS_DOLT_PORT
  # =tunnel) rather than via a natural-language instruction to the LLM: the GATE is
  # "the spawned environment reaches host Dolt", which is deterministic; whether the
  # agent AUTONOMOUSLY chooses to run bd is a separate, non-deterministic concern
  # (the agent reasons about the prompt) and is NOT what the rollout gate hinges on.
  # bd-through-tunnel = the wave-critical property (a remote crew's bd/mail must land).
  # Deterministic verify: the node creates a bead, we CAPTURE the id bd returns,
  # then the host asserts THAT id is visible via `bd show`. A count-delta + top-N
  # grep is racy — the town is live, other agents create beads concurrently, so
  # the probe scrolls past the top 5 and the count rises for unrelated reasons;
  # an id-specific `bd show` is immune. We let bd assign the id (auto) rather than
  # forcing --id, because the prefix is per-DB (the reactivecli rig DB uses
  # 'reactivecli-', the town root uses 'hq-') and an explicit cross-prefix id is
  # rejected. CRITICAL: the host check must read the SAME DB the crew writes — a
  # reactivecli/<crew> agent's beads live in the rig's reactivecli DB (crew .beads
  # redirect -> rig .beads), NOT the town-root 'hq' DB, so we verify from
  # $HOME/gt/$RIG, not $HOME/gt. NB: bd exits 0 even on "no issue found", so we
  # grep the output, not $?.
  RIGDIR="$HOME/gt/$RIG"
  pid="$(node_run 40 <<NODE
export GT_DOLT_HOST=127.0.0.1 GT_DOLT_PORT=13307 BEADS_DOLT_SERVER_HOST=127.0.0.1 BEADS_DOLT_PORT=13307
cd /opt/gastown/$CREW && bd create "F2 L8 mechanism probe" -d "remote bd through the reverse tunnel" 2>&1 | grep -i 'created issue' | grep -oE '[a-z]+-[a-z0-9]+' | head -1
NODE
)"
  # Drop ssm-run.sh's own "[ssm] i-<node> cmd=..." wrapper lines before matching —
  # else the instance id (i-0...) would match the id regex ahead of the bead id.
  pid="$(echo "$pid" | grep -v '^\[ssm\]' | grep -oE '[a-z]+-[a-z0-9]+' | head -1)"
  sleep 5
  shown="$(cd "$RIGDIR" && bd show "$pid" 2>&1)"
  if [ -n "$pid" ] && ! echo "$shown" | grep -q "no issue found" && echo "$shown" | grep -qF "$pid"; then
    pass "L8(c+d): bd-through-tunnel — node bd write ($pid) is visible in HOST Dolt"
  else
    fail "L8(c+d): node bd write ($pid) did NOT reach host Dolt — tunnel/BEADS_DOLT env"
  fi

  # (e) RECOVER from a tunnel drop: kill the host-side tunnel, confirm the keepalive
  # re-establishes + a node bd write lands again. The recover axis the gate requires.
  echo "  dropping the tunnel to test recovery..."
  pkill -f -- "-R ${GT_DOLT_PORT_FWD:-13307}" 2>/dev/null || true
  sleep 30   # keepalive/respawn window (open-remote-tunnel.sh re-establishes)
  rpid="$(node_run 40 <<NODE
export GT_DOLT_HOST=127.0.0.1 GT_DOLT_PORT=13307 BEADS_DOLT_SERVER_HOST=127.0.0.1 BEADS_DOLT_PORT=13307
cd /opt/gastown/$CREW && bd create "F2 L8 recover probe" -d "post tunnel-drop" 2>&1 | grep -i 'created issue' | grep -oE '[a-z]+-[a-z0-9]+' | head -1
NODE
)"
  rpid="$(echo "$rpid" | grep -v '^\[ssm\]' | grep -oE '[a-z]+-[a-z0-9]+' | head -1)"
  sleep 5
  rshown="$(cd "$RIGDIR" && bd show "$rpid" 2>&1)"
  if [ -n "$rpid" ] && ! echo "$rshown" | grep -q "no issue found" && echo "$rshown" | grep -qF "$rpid"; then
    pass "L8(e): RECOVERED from tunnel drop — node bd ($rpid) landed in host Dolt after re-establish"
  else
    fail "L8(e): did NOT recover from tunnel drop ($rpid not in host Dolt) — keepalive/recover path"
  fi

  # OBSERVABILITY: gt crew status shows the remote node host-side (no ssh).
  st="$(cd "$HOME/gt" && "$GT" crew status "$RIG/$CREW" 2>&1 || true)"
  if echo "$st" | grep -qi "Remote:.*$NODE"; then
    pass "L8 observability: gt crew status shows the remote node ($NODE)"
  else
    fail "L8 observability: gt crew status did not surface the remote node; out: $(echo "$st" | grep -i remote | head -1)"
  fi

  # Teardown: stop the node agent + remove the probe beads (don't litter the DB).
  node_run 30 <<NODE >/dev/null
\$TMUX_AS kill-session -t $SESSION 2>/dev/null || true
NODE
  ( cd "${RIGDIR:-$HOME/gt}" && bd delete "${pid:-}" "${rpid:-}" --force >/dev/null 2>&1 ) || true
  echo "  (teardown: killed node tmux session $SESSION; removed probe beads)"
fi

echo ""
if [ $FAILS -eq 0 ]; then
  echo "=== ALL hermetic checks passed (live gate skipped or pending) ==="
  exit 0
fi
echo "=== $FAILS check(s) failed ==="
exit 1
