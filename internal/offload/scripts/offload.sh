#!/bin/bash
# offload.sh — run a self-contained command on the science-account cluster, off the local box.
#
#   ./offload.sh <repo-url> <branch> "<command>"        # run command in repo, return its output
#   ./offload.sh -n <instance-id> <repo-url> <branch> "<command>"   # pin a specific node
#   ./offload.sh -p <branch-suffix> <repo-url> <branch> "<command>" # also git-push results branch
#
# Mechanism: presigns short-TTL S3 URLs for the gh PAT (staged by setup-secrets.sh), then SSM
# run-commands a job that sets a real HOME, fetches the PAT, clones, runs uv, runs your command.
# Secrets travel as presigned URLs — never as plaintext in the SSM command log.
# Results: command stdout/stderr returned inline; with -p, a results branch is pushed to origin.
#
# Production-safety: describe + run-command ONLY. Never terminates/modifies instances.
set -uo pipefail
PROFILE="${AWS_PROFILE_SCIENCE:-science}"; REGION="${AWS_REGION:-us-west-2}"
BUCKET="${OFFLOAD_BUCKET:-gastown-offload-221082188800}"
HERE="$(cd "$(dirname "$0")" && pwd)"

NODE=""; PUSH=""; BEDROCK=""; FRESH=""
while getopts "n:p:bf" o; do case $o in n) NODE="$OPTARG";; p) PUSH="$OPTARG";; b) BEDROCK=1;; f) FRESH=1;; esac; done
shift $((OPTIND-1))
REPO_URL="$1"; BRANCH="$2"; CMD="$3"
[ -z "${CMD:-}" ] && { echo "usage: offload.sh [-n node] [-p suffix] [-b] [-f] <repo-url> <branch> <command>  (-b=Bedrock bearer; -f=fresh, bypass uv cache for SDK-fix certs)"; exit 2; }

# Pick an idle big node (c7i.12xlarge = 48 vCPU) if none pinned. ponytail: first-online, not load-aware.
if [ -z "$NODE" ]; then
  NODE=$("$HERE/pick-node.sh") || { echo "no node available"; exit 1; }
fi
echo "[offload] node=$NODE branch=$BRANCH"

GH_URL=$(aws s3 presign "s3://$BUCKET/secrets/gh_token" --profile "$PROFILE" --region "$REGION" --expires-in 900) \
  || { echo "presign failed — run setup-secrets.sh first"; exit 1; }
# -b: presign the Bedrock bearer too. Nodes reach Converse ONLY via this bearer (Mantle path);
# the node instance role alone is NOT authorized to invoke. Verified id: global.anthropic.claude-sonnet-4-6.
BEARER_URL=""
if [ -n "$BEDROCK" ]; then
  BEARER_URL=$(aws s3 presign "s3://$BUCKET/secrets/bearer" --profile "$PROFILE" --region "$REGION" --expires-in 900) \
    || { echo "bearer presign failed — run setup-secrets.sh first"; exit 1; }
fi

# Derive owner/repo from the URL for clean (token-free) origin.
REPO_PATH=$(echo "$REPO_URL" | sed -E 's#^https?://[^/]+/##; s#/+$##'); case "$REPO_PATH" in *.git) :;; *) REPO_PATH="$REPO_PATH.git";; esac
WORK="rp-$(echo "$BRANCH" | tr -c 'A-Za-z0-9_.-' '-')"

JOB=$(mktemp)
cat > "$JOB" <<JOBEOF
# SSM runs this under /bin/sh (dash); re-exec under bash so pipefail/[[ ]] work.
if [ -z "\${BASH_VERSION:-}" ]; then exec bash "\$0" "\$@"; fi
set -uo pipefail
export HOME=/opt/gastown; mkdir -p \$HOME; cd \$HOME
export PATH="\$HOME/.local/bin:\$PATH"
${FRESH:+export UV_NO_CACHE=1   # -f: bypass uv cache so git SDK deps re-fetch+rebuild — defeats the vibranium repeated-version stale-wheel trap (qa_load SDK-fix certs)}
GHT=\$(curl -s "$GH_URL" | tr -d '\r\n'); [ -n "\$GHT" ] || { echo "[fatal] no PAT"; exit 1; }
command -v uv >/dev/null || curl -LsSf https://astral.sh/uv/install.sh | sh >/dev/null 2>&1
export GIT_CONFIG_GLOBAL=\$HOME/.gitconfig-$WORK; : > \$GIT_CONFIG_GLOBAL
git config --global credential.helper ""
git config --global user.email "offload@gastown.local"; git config --global user.name "gastown-offload"
# CRITICAL transport fix (town-host-setup §3): HTTP/2 + chunked-transfer corrupts git PUSH packs on
# this network path — a real-commit pack 400s / false "up-to-date" while a tiny ref push succeeds.
# The -p results-branch push sends real commits, so this MUST be set or pushes silently fail.
git config --global http.version HTTP/1.1
git config --global http.postBuffer 524288000
# Let uv/pip clone PRIVATE git deps without a helper: rewrite github https -> embedded-token https.
# Lives only in this job-local gitconfig on the ephemeral node; never in SSM logs or the repo remote.
git config --global url."https://x-access-token:\${GHT}@github.com/".insteadOf "https://github.com/"
rm -rf "\$HOME/$WORK"
git clone --quiet "https://x-access-token:\${GHT}@github.com/$REPO_PATH" "\$HOME/$WORK" || { echo "[fatal] clone failed"; exit 1; }
cd "\$HOME/$WORK"
git remote set-url origin "https://github.com/$REPO_PATH"
# Fail CLOSED: never silently test the default branch — gating the wrong code reads as a false PASS.
git checkout "$BRANCH" 2>/dev/null || { echo "[fatal] branch $BRANCH not found — refusing to run on default"; exit 3; }
JOBEOF

# -b: stage the Bedrock bearer (Mantle path) — nodes reach Converse ONLY via this, not the node role.
# URL single-quoted (it has & and % that would otherwise break the shell). Appended host-side.
if [ -n "$BEDROCK" ]; then
  {
    printf "BEARER=\$(curl -s '%s' | tr -d '\\\\r\\\\n')\n" "$BEARER_URL"
    echo 'export AWS_BEARER_TOKEN_BEDROCK="$BEARER" CLAUDE_CODE_USE_BEDROCK=1 AWS_REGION=us-west-2'
    echo '[ -n "$BEARER" ] && echo "[bedrock] bearer staged (len ${#BEARER})" || { echo "[fatal] empty bearer"; exit 4; }'
  } >> "$JOB"
fi

cat >> "$JOB" <<JOBEOF
echo "===== RUN (\$(hostname) \$(nproc)c) ====="
set +e
( $CMD ); RC=\$?
set -e
echo "===== EXIT \$RC ====="
JOBEOF

# Optional: push a results branch so artifacts return via git (not just stdout).
if [ -n "$PUSH" ]; then
  RB="offload-$PUSH"
  cat >> "$JOB" <<JOBEOF
git checkout -b "$RB" 2>/dev/null || git checkout "$RB"
git add -A && git commit -q -m "offload result: $PUSH [\$(hostname)]" 2>/dev/null || echo "(no changes to push)"
git push "https://x-access-token:\${GHT}@github.com/$REPO_PATH" "$RB" 2>&1 | tail -1
echo "===== RESULTS BRANCH $RB ====="
JOBEOF
fi

# Fail CLOSED: the job's final exit IS the command's exit code, so a test failure (or a killed/
# auth-lapsed/timed-out job that never reaches here) surfaces as a non-zero SSM status, which
# ssm-run.sh and this script propagate. A remote gate can never return a false green.
echo 'exit ${RC:-1}' >> "$JOB"

"$HERE/ssm-run.sh" "$NODE" "$JOB" "${OFFLOAD_TIMEOUT:-1800}"
RC=$?
rm -f "$JOB"
exit $RC
