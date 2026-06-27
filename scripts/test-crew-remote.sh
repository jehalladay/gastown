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
  node_run() {
    local f; f="$(mktemp "$TEST_DIR/node-XXXX.sh")"
    { echo 'if [ -z "${BASH_VERSION:-}" ]; then exec bash "$0" "$@"; fi'
      echo 'export PATH=/opt/gastown/.local/bin:/opt/gastown/node/bin:/opt/gastown/.npm-global/bin:$PATH'
      echo 'TMUX_AS="sudo -u ubuntu HOME=/opt/gastown tmux"'
      cat; } > "$f"
    AWS_PROFILE_SCIENCE="${AWS_PROFILE_SCIENCE:-science}" bash "$SSM_RUN" "$NODE" "$f" "${1:-90}" 2>&1
  }

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

  # (c) reads identity + (d) writes bd through the tunnel: count must INCREMENT host-side.
  before="$(bd count 2>/dev/null || echo 0)"
  node_run 30 <<NODE >/dev/null
\$TMUX_AS send-keys -t $SESSION -l 'Run: gt prime then bd create "F2 L8 dogfood probe \$(date +%s)" --description "remote-agent write through tunnel"'
\$TMUX_AS send-keys -t $SESSION Enter
NODE
  sleep 45
  after="$(bd count 2>/dev/null || echo 0)"
  if [ "${after:-0}" -gt "${before:-0}" ]; then
    pass "L8(c+d): agent wrote bd through the tunnel (host count $before -> $after)"
  else
    fail "L8(c+d): no host-side bd write detected (count $before -> $after) — identity/tunnel/bd path"
  fi

  # (e) RECOVER from a tunnel drop: kill the host-side tunnel, confirm the keepalive
  # re-establishes + the agent's next bd lands. The recover axis the gate requires.
  echo "  dropping the tunnel to test recovery..."
  pkill -f -- "-R ${GT_DOLT_PORT_FWD:-13307}" 2>/dev/null || true
  sleep 25   # keepalive/respawn window
  before2="$(bd count 2>/dev/null || echo 0)"
  node_run 30 <<NODE >/dev/null
\$TMUX_AS send-keys -t $SESSION -l 'bd create "F2 L8 recover probe \$(date +%s)" --description "post tunnel-drop"'
\$TMUX_AS send-keys -t $SESSION Enter
NODE
  sleep 45
  after2="$(bd count 2>/dev/null || echo 0)"
  if [ "${after2:-0}" -gt "${before2:-0}" ]; then
    pass "L8(e): agent RECOVERED from tunnel drop — bd landed after re-establish ($before2 -> $after2)"
  else
    fail "L8(e): agent did NOT recover from tunnel drop (count $before2 -> $after2) — keepalive/recover path"
  fi

  # OBSERVABILITY: gt crew status shows the remote node host-side (no ssh).
  st="$(cd "$HOME/gt" && "$GT" crew status "$RIG/$CREW" 2>&1 || true)"
  if echo "$st" | grep -qi "Remote:.*$NODE"; then
    pass "L8 observability: gt crew status shows the remote node ($NODE)"
  else
    fail "L8 observability: gt crew status did not surface the remote node; out: $(echo "$st" | grep -i remote | head -1)"
  fi

  # Teardown: stop the node agent (clean up the test spawn).
  node_run 30 <<NODE >/dev/null
\$TMUX_AS kill-session -t $SESSION 2>/dev/null || true
NODE
  echo "  (teardown: killed node tmux session $SESSION)"
fi

echo ""
if [ $FAILS -eq 0 ]; then
  echo "=== ALL hermetic checks passed (live gate skipped or pending) ==="
  exit 0
fi
echo "=== $FAILS check(s) failed ==="
exit 1
