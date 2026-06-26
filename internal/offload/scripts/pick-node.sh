#!/bin/bash
# pick-node.sh        — print one online fat compute node id (first-online, fast path).
# pick-node.sh -i     — print the LEAST-LOADED fat node (probes 1-min load avg via SSM).
# Fat = c7i.* / c5a.16* / m7i.48* (48-64 vCPU). Same output contract either way: one id on stdout.
# ponytail: -i probes up to PROBE_CAP nodes (default 12) to bound fleet-scale latency; no caching.
set -uo pipefail
PROFILE="${AWS_PROFILE_SCIENCE:-science}"; REGION="${AWS_REGION:-us-west-2}"
PROBE_CAP="${PROBE_CAP:-12}"
IDLE=""; [ "${1:-}" = "-i" ] && IDLE=1

ONLINE=$(aws ssm describe-instance-information --profile "$PROFILE" --region "$REGION" \
  --query 'InstanceInformationList[?PingStatus==`Online`].InstanceId' --output text)
[ -z "$ONLINE" ] && exit 1
FAT=$(aws ec2 describe-instances --profile "$PROFILE" --region "$REGION" \
  --instance-ids $ONLINE \
  --query 'Reservations[].Instances[?starts_with(InstanceType,`c7i`)||starts_with(InstanceType,`c5a.16`)||starts_with(InstanceType,`m7i.48`)].InstanceId' \
  --output text | tr '\t' '\n' | grep .)
[ -z "$FAT" ] && exit 1

if [ -z "$IDLE" ]; then echo "$FAT" | head -1; exit 0; fi

# -i: SSM-probe 1-min load avg on the first PROBE_CAP fat nodes, print the lowest. ponytail: one
# send-command per node, polled briefly; load (not load/core) is fine — all fat nodes are 48-64c.
CANDIDATES=$(echo "$FAT" | head -n "$PROBE_CAP")
N=$(echo "$CANDIDATES" | wc -l | tr -d ' ')
[ "$N" -ge "$PROBE_CAP" ] && echo "[pick-node] probing first $PROBE_CAP online fat nodes (cap)" >&2
best=""; bestload=""
for id in $CANDIDATES; do
  cid=$(aws ssm send-command --profile "$PROFILE" --region "$REGION" --instance-ids "$id" \
    --document-name AWS-RunShellScript --timeout-seconds 30 \
    --parameters 'commands=["cut -d\" \" -f1 /proc/loadavg"]' \
    --query 'Command.CommandId' --output text 2>/dev/null) || continue
  load=""
  for _ in 1 2 3 4 5 6; do
    sleep 2
    out=$(aws ssm get-command-invocation --profile "$PROFILE" --region "$REGION" \
      --command-id "$cid" --instance-id "$id" --query 'StandardOutputContent' --output text 2>/dev/null)
    st=$(aws ssm get-command-invocation --profile "$PROFILE" --region "$REGION" \
      --command-id "$cid" --instance-id "$id" --query 'Status' --output text 2>/dev/null)
    case "$st" in Success) load=$(echo "$out" | tr -d '[:space:]'); break;; Failed|Cancelled|TimedOut) break;; esac
  done
  [ -z "$load" ] && continue
  echo "[pick-node] $id load=$load" >&2
  if [ -z "$bestload" ] || awk "BEGIN{exit !($load < $bestload)}"; then best="$id"; bestload="$load"; fi
done
[ -z "$best" ] && { echo "[pick-node] no node answered load probe; falling back to first-online" >&2; echo "$FAT" | head -1; exit 0; }
echo "[pick-node] least-loaded: $best (load=$bestload)" >&2
echo "$best"
