#!/bin/bash
# setup-secrets.sh — stage the gh PAT (and Bedrock bearer, for claude jobs) into the private
# offload bucket, SSE-encrypted. Run once per session, or whenever the gh token rotates.
# offload.sh presigns short-TTL URLs against these objects so nodes fetch them without node IAM.
set -uo pipefail
PROFILE="${AWS_PROFILE_SCIENCE:-science}"; REGION="${AWS_REGION:-us-west-2}"
BUCKET="${OFFLOAD_BUCKET:-gastown-offload-221082188800}"

T=$(mktemp); gh auth token > "$T" || { echo "gh auth token failed"; exit 1; }
aws s3 cp "$T" "s3://$BUCKET/secrets/gh_token" --profile "$PROFILE" --region "$REGION" --sse AES256 >/dev/null
rm -f "$T"; echo "[secrets] gh_token staged"

if [ -n "${AWS_BEARER_TOKEN_BEDROCK:-}" ]; then
  T=$(mktemp); printf '%s' "$AWS_BEARER_TOKEN_BEDROCK" > "$T"
  aws s3 cp "$T" "s3://$BUCKET/secrets/bearer" --profile "$PROFILE" --region "$REGION" --sse AES256 >/dev/null
  rm -f "$T"; echo "[secrets] bedrock bearer staged (for claude jobs)"
fi
