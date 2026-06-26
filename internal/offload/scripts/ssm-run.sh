#!/bin/bash
# ssm-run.sh <instance-id> <script-file> [timeout-secs]
# Runs a shell script on a node via SSM run-command, waits, prints status+output.
# ponytail: synchronous poll loop; fine for provisioning. Use sbatch/async for long jobs.
set -uo pipefail
PROFILE="${AWS_PROFILE_SCIENCE:-science}"; REGION="${AWS_REGION:-us-west-2}"
NODE="$1"; SCRIPT="$2"; TMO="${3:-120}"
CID=$(aws ssm send-command --profile "$PROFILE" --region "$REGION" \
  --instance-ids "$NODE" --document-name "AWS-RunShellScript" \
  --timeout-seconds "$TMO" \
  --cli-input-json "$(jq -n --rawfile s "$SCRIPT" '{Parameters:{commands:[$s]}}')" \
  --query 'Command.CommandId' --output text) || { echo "send failed"; exit 1; }
echo "[ssm] $NODE cmd=$CID"
for i in $(seq 1 "$((TMO/3 + 5))"); do
  sleep 3
  ST=$(aws ssm get-command-invocation --profile "$PROFILE" --region "$REGION" \
    --command-id "$CID" --instance-id "$NODE" --query 'Status' --output text 2>/dev/null)
  case "$ST" in Success|Failed|Cancelled|TimedOut) break;; esac
done
echo "[ssm] status=$ST"
aws ssm get-command-invocation --profile "$PROFILE" --region "$REGION" \
  --command-id "$CID" --instance-id "$NODE" \
  --query 'StandardOutputContent' --output text
ERR=$(aws ssm get-command-invocation --profile "$PROFILE" --region "$REGION" \
  --command-id "$CID" --instance-id "$NODE" --query 'StandardErrorContent' --output text)
[ -n "$ERR" ] && { echo "--- STDERR ---"; echo "$ERR"; }
[ "$ST" = "Success" ]
