#!/bin/bash
# provision-node.sh <instance-id> [repo-url] [branch]
#
# The OFFLOAD-READY TEMPLATE (acceleration-plan WS1 item 1). Runs ONCE per node the
# recipe that offload.sh otherwise redoes cold every job: real HOME, uv install, the
# C-9 git transport fix, and a PRE-WARMED uv cache (clone + uv sync). After this, an
# offload.sh dispatch to the same node reuses ~/.cache/uv → seconds, not ~70s.
#
# Idempotent: re-running re-syncs the cache + refreshes the /opt/gastown/.offload-ready
# marker. Drops NO long-lived secret on the node — the gh token lives only in a job-local
# gitconfig during uv sync, exactly as offload.sh does it; the persisted artifact is the
# (secret-free) uv cache + a marker file.
#
# Production-safety: describe + run-command ONLY. Never launches/terminates/modifies instances.
set -uo pipefail
PROFILE="${AWS_PROFILE_SCIENCE:-science}"; REGION="${AWS_REGION:-us-west-2}"
BUCKET="${OFFLOAD_BUCKET:-gastown-offload-221082188800}"
HERE="$(cd "$(dirname "$0")" && pwd)"
# Tunnel key in a STABLE host path (not the script dir) so an embedded/extracted gt shares the same
# key the tunnel script auto-detects. Override with OFFLOAD_STATE_DIR. See warm-pool.sh.
STATE_DIR="${OFFLOAD_STATE_DIR:-$HOME/gt/.offload}"
mkdir -p "$STATE_DIR" 2>/dev/null

FRESH=""; AGENT=""; CREW=""
while :; do case "${1:-}" in
  -f) FRESH=1; shift;;                # purge uv cache before re-warm (vibranium stale-wheel trap)
  --agent) AGENT=1; shift;;           # also stage the AGENT toolchain (node20+claude+bd) + host ssh key
  --crew) CREW="$2"; shift 2;;        # --agent: also clone the crew repo to /opt/gastown/<crew> (F2 spawn)
  *) break;;
esac; done
NODE="${1:-}"; REPO_URL="${2:-https://github.com/jehalladay/ReactiveCLI.git}"; BRANCH="${3:-master}"
[ -z "$NODE" ] && { echo "usage: provision-node.sh [-f] [--agent [--crew <name>]] <instance-id> [repo-url] [branch]"; exit 2; }
# Dolt DB the crew clone's bd redirects to (matches the rig's host Dolt DB). reactivecli rig = "reactivecli".
BEADS_DB="${BEADS_DB:-reactivecli}"

# --agent: bake the host tunnel pubkey into the node's authorized_keys so eng_sr2's reverse tunnel
# (open-remote-tunnel.sh, canonical, node-side fwd port 13307) uses a PERSISTENT key — no 60s
# ephemeral-key TTL race. The key is .offload-tunnel-key, which eng_sr2's script auto-detects.
HOST_PUBKEY=""
if [ -n "$AGENT" ]; then
  KEYFILE="$STATE_DIR/.offload-tunnel-key.pub"
  [ -f "$KEYFILE" ] || ssh-keygen -t ed25519 -N "" -f "$STATE_DIR/.offload-tunnel-key" -C offload-tunnel >/dev/null 2>&1
  HOST_PUBKEY=$(cat "$KEYFILE")
fi

GH_URL=$(aws s3 presign "s3://$BUCKET/secrets/gh_token" --profile "$PROFILE" --region "$REGION" --expires-in 900) \
  || { echo "presign failed — run setup-secrets.sh first"; exit 1; }
# --agent fetches the published linux/amd64 gt + bd binaries (no per-node build). bd is pinned to
# the live town schema (v52 / commit 7f6752c8f); republish bin/bd-linux-amd64 when the town migrates.
GT_URL=""; BD_URL=""
if [ -n "${AGENT:-}" ]; then
  GT_URL=$(aws s3 presign "s3://$BUCKET/bin/gt-linux-amd64" --profile "$PROFILE" --region "$REGION" --expires-in 900 2>/dev/null)
  BD_URL=$(aws s3 presign "s3://$BUCKET/bin/bd-linux-amd64" --profile "$PROFILE" --region "$REGION" --expires-in 900 2>/dev/null)
fi

REPO_PATH=$(echo "$REPO_URL" | sed -E 's#^https?://[^/]+/##; s#/+$##'); case "$REPO_PATH" in *.git) :;; *) REPO_PATH="$REPO_PATH.git";; esac

JOB=$(mktemp)
cat > "$JOB" <<JOBEOF
# SSM runs under /bin/sh (dash); re-exec under bash for pipefail.
if [ -z "\${BASH_VERSION:-}" ]; then exec bash "\$0" "\$@"; fi
set -uo pipefail
# HOME gotcha: SSM runs as root with HOME='' on a 3.1G tmpfs. /opt/gastown is real disk (232G).
export HOME=/opt/gastown; mkdir -p \$HOME; cd \$HOME
export PATH="\$HOME/.local/bin:\$PATH"

# 1. uv — idempotent self-install to \$HOME/.local/bin (git+python3 already on the nodes).
command -v uv >/dev/null || curl -LsSf https://astral.sh/uv/install.sh | sh >/dev/null 2>&1
command -v uv >/dev/null || { echo "[fatal] uv install failed"; exit 1; }

# 2. Persistent global git config: the C-9 transport fix (HTTP/2+chunked corrupts real-commit
#    pushes) + identity. Persisting it helps manual/claude git use; offload.sh still sets its own
#    job-local copy, so this is belt-and-suspenders, not a dependency.
git config --global http.version HTTP/1.1
git config --global http.postBuffer 524288000
git config --global user.email "offload@gastown.local"
git config --global user.name "gastown-offload"

# 3. Pre-warm the uv cache: clone + uv sync once so sibling-SDK wheels + deps are cached at
#    \$HOME/.cache/uv. The token rewrite lives ONLY in a job-local gitconfig (discarded below),
#    so no secret persists — only the secret-free cache does.
GHT=\$(curl -s "$GH_URL" | tr -d '\r\n'); [ -n "\$GHT" ] || { echo "[fatal] no PAT"; exit 1; }
PREWARM=\$HOME/prewarm
export GIT_CONFIG_GLOBAL=\$HOME/.gitconfig-prewarm; : > \$GIT_CONFIG_GLOBAL
git config --global http.version HTTP/1.1
git config --global http.postBuffer 524288000
git config --global credential.helper ""
git config --global url."https://x-access-token:\${GHT}@github.com/".insteadOf "https://github.com/"
# -f: purge BOTH stale layers before re-warm so an SDK fix on an UNCHANGED version string
#     re-fetches+rebuilds — (1) uv's package cache, (2) hatch's persisted env (HOME survives jobs;
#     env hash doesn't bump on an SDK rev, so it'd serve a stale SDK — eng_sr2/offload.sh dd34be5).
${FRESH:+uv cache clean >/dev/null 2>&1; rm -rf "\$HOME/.local/share/hatch/env" >/dev/null 2>&1}
rm -rf "\$PREWARM"
git clone --quiet --depth 1 -b "$BRANCH" "https://x-access-token:\${GHT}@github.com/$REPO_PATH" "\$PREWARM" \
  || { echo "[fatal] prewarm clone failed"; exit 1; }
cd "\$PREWARM"
# Populate the cache: prefer 'uv sync', fall back to hatch env if that's the project's path.
uv sync >/dev/null 2>&1 || uv pip install -e . >/dev/null 2>&1 || echo "[warn] uv sync non-zero — cache partially warmed"
cd "\$HOME"
rm -f "\$GIT_CONFIG_GLOBAL"            # discard the token-bearing gitconfig
rm -rf "\$PREWARM"                      # the cache is what we keep, not the clone

# 4. Marker: proves offload-ready + records when/what was warmed (secret-free).
PREWARMED_SHA=\$(cd "\$HOME" 2>/dev/null; echo "$BRANCH")
{ echo "ready_repo=$REPO_PATH"; echo "ready_branch=$BRANCH"; echo "uv=\$(uv --version 2>/dev/null)"; \
  echo "host=\$(hostname)"; echo "cores=\$(nproc)"; } > \$HOME/.offload-ready
echo "===== OFFLOAD-READY \$(hostname) \$(nproc)c uv=\$(uv --version 2>/dev/null) ====="
cat \$HOME/.offload-ready
JOBEOF

# --agent: stage the AGENT toolchain (node20 + claude + bd) + bake the host tunnel pubkey. The base
# node ships node v12 (too old for claude-code, needs 18+), so we install standalone node 20 into
# \$HOME. bd installs via its official token-authed script. The persistent pubkey lets eng_sr2's
# reverse tunnel skip the ephemeral-key TTL race.
if [ -n "$AGENT" ]; then
  cat >> "$JOB" <<JOBEOF
echo "===== AGENT TOOLCHAIN ====="
# Modern node (standalone tarball — base node v12 is too old for claude-code).
NVER=v20.18.1
if [ ! -x "\$HOME/node/bin/node" ]; then
  curl -fsSL "https://nodejs.org/dist/\$NVER/node-\$NVER-linux-x64.tar.xz" -o /tmp/node.tar.xz \
    && mkdir -p \$HOME/node && tar -xJf /tmp/node.tar.xz -C \$HOME/node --strip-components=1 \
    || { echo "[fatal] node20 install failed"; exit 5; }
fi
export PATH="\$HOME/node/bin:\$HOME/.npm-global/bin:\$HOME/.local/bin:\$PATH"
npm config set prefix "\$HOME/.npm-global" >/dev/null 2>&1
# gt: published linux/amd64 static ELF (gastown_eng_lead). Presign-fetched, just download + chmod.
if [ -n "$GT_URL" ]; then
  curl -fsSL "$GT_URL" -o \$HOME/.local/bin/gt 2>/dev/null && chmod +x \$HOME/.local/bin/gt \
    && echo "gt staged: \$(\$HOME/.local/bin/gt --version 2>&1 | head -1)" || echo "[warn] gt fetch failed"
else
  echo "[warn] no gt binary published at s3://$BUCKET/bin/gt-linux-amd64 yet"
fi
# claude (public npm).
command -v claude >/dev/null || npm i -g @anthropic-ai/claude-code >/dev/null 2>&1
# bd: PREFER the published linux/amd64 binary (pinned to the live town schema v52 / commit
# 7f6752c8f). FALLBACK: source-build at that commit if the binary isn't published. Verified
# 2026-06-26: town DB = schema v52 = migration 0052; v1.0.5=0049 (3 behind) can't write, and main
# (0053) would try to migrate the live DB. bd@7f6752c8f read+wrote the host DB cleanly (e2e proof).
# Bump bin/bd-linux-amd64 (+ this commit) when the town migrates past v52. \$ escaped = node-side var.
BD_COMMIT=7f6752c8f
GHT=\$(curl -s "$GH_URL" | tr -d '\r\n')
# ALWAYS (re)fetch the published v52 binary, OVERWRITING any existing bd — the version string can't
# distinguish v49 from v52 (both "1.0.5 (dev)"), and a stale v49 silently half-writes (corruption).
# The curl is cheap; the published binary is the schema source-of-truth. Source-build only if no publish.
if [ -n "$BD_URL" ] && curl -fsSL "$BD_URL" -o \$HOME/.local/bin/bd 2>/dev/null && chmod +x \$HOME/.local/bin/bd; then
  echo "bd fetched (v52 binary, overwrote any stale): \$(\$HOME/.local/bin/bd --version 2>&1 | head -1)"
elif [ ! -x "\$HOME/.local/bin/bd" ]; then
    echo "[bd] no published binary — source-building @\$BD_COMMIT (slower)"
    if ! command -v go >/dev/null && [ ! -x "\$HOME/go-sdk/bin/go" ]; then
      curl -fsSL "https://go.dev/dl/go1.24.4.linux-amd64.tar.gz" -o /tmp/go.tgz \
        && mkdir -p \$HOME/go-sdk && tar -xzf /tmp/go.tgz -C \$HOME/go-sdk --strip-components=1 || echo "[warn] go install failed"
    fi
    export PATH="\$HOME/go-sdk/bin:\$PATH"
    export GIT_CONFIG_GLOBAL=\$HOME/.gitconfig-bd; : > \$GIT_CONFIG_GLOBAL
    git config --global url."https://x-access-token:\${GHT}@github.com/".insteadOf "https://github.com/"
    rm -rf /tmp/beads && git clone --quiet https://github.com/gastownhall/beads /tmp/beads 2>/dev/null \
      && (cd /tmp/beads && git checkout --quiet \$BD_COMMIT 2>/dev/null && go build -o \$HOME/.local/bin/bd ./cmd/bd 2>/dev/null) \
      && echo "bd built @\$BD_COMMIT (schema v52): \$(\$HOME/.local/bin/bd --version 2>&1 | head -1)" || echo "[warn] bd source build failed"
    rm -f \$GIT_CONFIG_GLOBAL; rm -rf /tmp/beads
fi
# Bake the host tunnel pubkey for the node's real login user. THE FLEET IS MIXED: some nodes have
# 'ubuntu' (uid 1000), some 'ec2-user' — root SSH is forced-command-blocked on both. Bake into
# whichever login user EXISTS, and RECORD it in the marker so the tunnel targets the right user
# (TUNNEL_SSH_USER) per node — a fixed default fails on the other AMI.
LOGIN_USER=""
for u in ubuntu ec2-user; do
  d=\$(getent passwd \$u | cut -d: -f6); [ -z "\$d" ] && continue
  mkdir -p \$d/.ssh && chmod 700 \$d/.ssh
  grep -qF "$HOST_PUBKEY" \$d/.ssh/authorized_keys 2>/dev/null || echo "$HOST_PUBKEY" >> \$d/.ssh/authorized_keys
  chmod 600 \$d/.ssh/authorized_keys 2>/dev/null
  chown -R \$u:\$u \$d/.ssh 2>/dev/null
  LOGIN_USER="\$u"
  echo "[agent] tunnel key baked for \$u (\$d)"
done
echo "login_user=\$LOGIN_USER" >> \$HOME/.offload-ready
# CLAUDE CONFIG PRE-SEED (bug #7): the agent runs as \$LOGIN_USER but /opt/gastown is root-owned, so
# claude can't mkdir its session-env NOR read/write .claude.json, AND the 3 first-run gates (welcome/
# trust/bypass) would block a non-interactive agent. Pre-seed .claude.json AS the agent's config with
# onboarding done + bypass-permissions accepted + the crew clone trusted, so claude boots to a ready REPL.
if [ -n "\$LOGIN_USER" ]; then
  cat > \$HOME/.claude.json <<CFG
{"hasCompletedOnboarding":true,"lastOnboardingVersion":"2.1.195","bypassPermissionsModeAccepted":true,"hasSeenAutoModeEntryWarning":true,"projects":{"/opt/gastown/$CREW":{"hasTrustDialogAccepted":true,"hasClaudeMdExternalIncludesApproved":true,"hasClaudeMdExternalIncludesWarningShown":true,"allowedTools":[]}}}
CFG
  echo "[agent] claude config pre-seeded (onboarding+bypass+trust /opt/gastown/$CREW)"
fi
JOBEOF
  # --crew: clone the crew repo to /opt/gastown/<crew> so the agent's cwd = its clone (F2 step b/i).
  # Token only in a job-local gitconfig; origin left token-free. gt passes repo+branch from rig config.
  if [ -n "$CREW" ]; then
    cat >> "$JOB" <<JOBEOF
CLONE=\$HOME/$CREW
export GIT_CONFIG_GLOBAL=\$HOME/.gitconfig-crew; : > \$GIT_CONFIG_GLOBAL
git config --global http.version HTTP/1.1
git config --global http.postBuffer 524288000
git config --global url."https://x-access-token:\${GHT}@github.com/".insteadOf "https://github.com/"
if [ ! -d "\$CLONE/.git" ]; then
  rm -rf "\$CLONE"
  git clone --quiet -b "$BRANCH" "https://x-access-token:\${GHT}@github.com/$REPO_PATH" "\$CLONE" \
    && (cd "\$CLONE" && git remote set-url origin "https://github.com/$REPO_PATH") \
    && echo "[agent] crew clone: \$CLONE @ $BRANCH" || echo "[warn] crew clone failed"
else
  (cd "\$CLONE" && git fetch --quiet origin "$BRANCH" && git checkout --quiet "$BRANCH" && git pull --quiet) \
    && echo "[agent] crew clone updated: \$CLONE @ $BRANCH" || echo "[warn] crew clone update failed"
fi
rm -f \$GIT_CONFIG_GLOBAL
# .beads REDIRECT: point the crew clone's bd at the TUNNELED host Dolt (127.0.0.1:13307 -> host:3307,
# db reactivecli) so 'bd' in the agent's cwd resolves the live town DB through the reverse tunnel.
# IMPORTANT: do NOT set the deprecated dolt_server_port/host in metadata — that triggers bd's
# local-Dolt MANAGEMENT (it writes dolt-server.port/.lock + may try to auto-start a local server) and
# warns "deprecated, port file is now primary". Instead set mode=server + db, and let the AGENT'S ENV
# (BEADS_DOLT_SERVER_HOST/BEADS_DOLT_PORT=13307, set by the spawn) drive the connection. Clear any
# stale port/lock files from a prior local-Dolt attempt so they can't override the env routing.
mkdir -p "\$CLONE/.beads" && chmod 700 "\$CLONE/.beads"
rm -f "\$CLONE/.beads/dolt-server.port" "\$CLONE/.beads/dolt-server.lock"
cat > "\$CLONE/.beads/metadata.json" <<'META'
{"backend":"dolt","database":"dolt","dolt_database":"$BEADS_DB","dolt_mode":"server"}
META
# dolt.auto-start: false — CRITICAL for a remote agent. Without it, when the tunnel is momentarily
# down bd FALLS BACK to auto-starting a LOCAL dolt (creating stale port/lock files + reading an empty
# node-local DB = the 401-vs-host mismatch). A remote agent must use the tunnel or FAIL CLOSED, never
# silently read a local DB. (dolt isn't even installed on the node, so auto-start errors anyway.)
cat > "\$CLONE/.beads/config.yaml" <<'CFG'
dolt:
  auto-start: false
CFG
echo "[agent] .beads redirect -> server mode db=$BEADS_DB (env BEADS_DOLT_PORT=13307; auto-start OFF; stale port/lock cleared)"
echo "crew_clone=\$CLONE" >> \$HOME/.offload-ready
JOBEOF
  fi
  cat >> "$JOB" <<JOBEOF
{ echo "node=\$(\$HOME/node/bin/node --version 2>/dev/null)"; echo "claude=\$(claude --version 2>/dev/null | head -1)"; \
  echo "bd=\$(command -v bd && bd --version 2>/dev/null | head -1)"; } >> \$HOME/.offload-ready
echo "agent-ready=1" >> \$HOME/.offload-ready
# OWNERSHIP (bug #7): the agent runs as \$LOGIN_USER but everything above was created as ROOT (SSM runs
# as root). chown the WHOLE HOME to the agent LAST, so it can mkdir session-env, read its config, write
# the clone. Must be last — after toolchain + clone + config are all in place.
if [ -n "\$LOGIN_USER" ]; then
  chown -R "\$LOGIN_USER":"\$LOGIN_USER" /opt/gastown 2>/dev/null \
    && echo "[agent] chown -R \$LOGIN_USER /opt/gastown (agent owns its HOME)" \
    || echo "[warn] chown /opt/gastown failed"
fi
echo "===== AGENT-READY: node=\$(\$HOME/node/bin/node --version) claude=\$(command -v claude) bd=\$(command -v bd) ====="
# SSM-AGENT PORT-PLUGIN refresh (gastown_eng_lead found it live): a long-up node's amazon-ssm-agent
# can WEDGE its Port (port-forwarding) plugin -> the reverse tunnel's ssh -R dies "Plugin with name
# Port not found", rc=255, while send-command still works (so it's silent until the tunnel fails).
# Restart the agent to refresh the plugin. DETACHED (setsid+sleep) so it returns THIS command first —
# a foreground restart kills the SSM channel mid-provision. Handles snap OR systemctl (fleet is mixed).
if snap list amazon-ssm-agent >/dev/null 2>&1; then
  setsid sh -c 'sleep 3; snap restart amazon-ssm-agent' >/dev/null 2>&1 &
  echo "[agent] scheduled ssm-agent Port-plugin refresh (snap, detached)"
elif systemctl list-unit-files amazon-ssm-agent.service >/dev/null 2>&1; then
  setsid sh -c 'sleep 3; systemctl restart amazon-ssm-agent' >/dev/null 2>&1 &
  echo "[agent] scheduled ssm-agent Port-plugin refresh (systemctl, detached)"
fi
JOBEOF
fi

echo "[provision] node=$NODE repo=$REPO_PATH branch=$BRANCH${AGENT:+ +agent-toolchain} (one-time warm)"
"$HERE/ssm-run.sh" "$NODE" "$JOB" "${PROVISION_TIMEOUT:-600}"
RC=$?
rm -f "$JOB"
exit $RC
