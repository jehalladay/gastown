#!/bin/bash
# open-remote-tunnel.sh — host-side reverse tunnel for remote crew (F2, host-UP tier).
#
#   ./open-remote-tunnel.sh <instance-id> [fwd-port]
#
# Opens a host-INITIATED reverse tunnel so an agent loop running ON a cluster node can reach the
# HOST's Dolt (127.0.0.1:3307) — the data plane for bd/gt-mail. phase0 proved node->host is NOT
# directly routable (host behind corp NAT, no inbound); a host-initiated `ssh -R` over SSM is the
# only path, and it lives only while the host is awake (host-DOWN persistence = the cluster Dolt hub,
# separate tier). The spawned agent on the node then exports:
#     GT_DOLT_HOST=127.0.0.1   GT_DOLT_PORT=<fwd-port>
# and its bd/gt-mail flow back through the tunnel to the host's Dolt (GT_DOLT_HOST routing is wired
# end-to-end in beads -> BEADS_DOLT_SERVER_HOST). NO gt-proxy on this path (the acp proxy is the
# exec-proxy, unrelated to Dolt).
#
# Transport: `ssh -R` with an SSM ProxyCommand (AWS-StartSSHSession) — same SSM channel offload.sh
# uses, + the SG already allows tcp/22 inbound. session-manager-plugin must be installed on the host.
#
# Keepalive: ServerAliveInterval re-detects a dead tunnel; the outer loop re-establishes it on drop
# (autossh-style). A drop is transient (SSM/network); the agent should treat Dolt-unreachable as
# PAUSE-and-retry, not a hard error. Ctrl-C / SIGTERM tears the tunnel down cleanly.
#
# ponytail: a keepalive loop around `ssh -R` over SSM — not a tunnel daemon. One node per invocation;
# the spawn caller runs one of these per remote agent node.
set -uo pipefail
PROFILE="${AWS_PROFILE_SCIENCE:-science}"; REGION="${AWS_REGION:-us-west-2}"
NODE="${1:?usage: open-remote-tunnel.sh <instance-id> [fwd-port]}"
FWD_PORT="${2:-13307}"               # node-side loopback port the agent points GT_DOLT_PORT at
DOLT_PORT="${GT_DOLT_PORT_LOCAL:-3307}"  # host's local Dolt port (the tunnel target)
# The science cluster nodes log in as 'ubuntu' (uid 1000); root SSH is forced-command-blocked + there
# is NO ec2-user (offload_ops verified live: provision-node.sh --agent bakes the key into ubuntu's
# authorized_keys, and the tunnel auths as ubuntu -> host Dolt). Override only for a different image.
SSH_USER="${TUNNEL_SSH_USER:-ubuntu}"

command -v session-manager-plugin >/dev/null || { echo "[tunnel] FATAL: session-manager-plugin not installed on host"; exit 2; }

# SSM ProxyCommand: ssh dials the node THROUGH an SSM session (no public IP / inbound needed host-side).
PROXY="aws ssm start-session --target %h --document-name AWS-StartSSHSession --parameters portNumber=%p --profile $PROFILE --region $REGION"

echo "[tunnel] host-initiated reverse tunnel -> node $NODE : agent reaches host Dolt via 127.0.0.1:$FWD_PORT"
echo "[tunnel] spawned agent on the node must export: GT_DOLT_HOST=127.0.0.1 GT_DOLT_PORT=$FWD_PORT"

# AUTH: ssh -R needs a key the node accepts. Key resolution, in order:
#   1. $TUNNEL_SSH_KEY (explicit) — use it, persistent (offload_ops bakes its .pub into the node).
#   2. The shared persistent key offload_ops provisions — .offload-tunnel-key (provision-node.sh --agent
#      bakes its .pub into UBUNTU's authorized_keys, 539c4dc). Auto-detected here so the common case is
#      zero-config. This is the ROBUST path (no TTL race) — offload_ops verified it auths live.
#   3. Ephemeral fallback (no persistent key found) — ec2-instance-connect push (60s TTL). KNOWN CEILING
#      (tested 2026-06-26): the TTL can race the SSM ProxyCommand handshake → 'Permission denied'. Only
#      hit if a node wasn't --agent-provisioned; provision it for the persistent path.
# ponytail: prefer the provisioned persistent key; ephemeral is the unprovisioned-node fallback.
KEY="${TUNNEL_SSH_KEY:-}"
if [ -z "$KEY" ]; then
  # Auto-detect offload_ops' shared persistent key (matches their host-tunnel.sh convention).
  for cand in "$HOME/gt/reactivecli/crew/offload_ops/.offload-tunnel-key" \
              "$HOME/gt/reactivecli/refinery/rig/.offload-tunnel-key" \
              "$(dirname "$0")/.offload-tunnel-key"; do
    [ -f "$cand" ] && { KEY="$cand"; break; }
  done
fi
if [ -n "$KEY" ]; then
  TUNNEL_AUTHORIZED_KEY=1   # a real key path was found/given → persistent path, skip ephemeral push
  echo "[tunnel] using persistent key: $KEY"
else
  KEY=$(mktemp -u "${TMPDIR:-/tmp}/gt-tunnel-key-XXXXXX")
  ssh-keygen -t ed25519 -f "$KEY" -N "" -q || { echo "[tunnel] FATAL: keygen failed"; exit 2; }
  trap 'rm -f "$KEY" "$KEY.pub"' EXIT   # ephemeral keypair never persists
  echo "[tunnel] WARN: no provisioned .offload-tunnel-key found — using ephemeral key (TTL-race risk; --agent-provision the node for the persistent path)"
fi
AZ=$(aws ec2 describe-instances --profile "$PROFILE" --region "$REGION" --instance-ids "$NODE" \
  --query 'Reservations[].Instances[].Placement.AvailabilityZone' --output text 2>/dev/null)

push_key() {  # refresh the ephemeral key (no-op if a persistent authorized key is configured)
  [ "${TUNNEL_AUTHORIZED_KEY:-}" = "1" ] && return 0
  aws ec2-instance-connect send-ssh-public-key --profile "$PROFILE" --region "$REGION" \
    --instance-id "$NODE" --availability-zone "$AZ" --instance-os-user "$SSH_USER" \
    --ssh-public-key "file://$KEY.pub" >/dev/null 2>&1 \
    || echo "[tunnel] WARN: ec2-instance-connect key push failed (perms? AZ=$AZ) — ssh may be denied"
}

# Clean teardown on signal so we don't leak SSM sessions / ssh procs.
TUNNEL_PID=""
cleanup() { [ -n "$TUNNEL_PID" ] && kill "$TUNNEL_PID" 2>/dev/null; echo "[tunnel] torn down"; exit 0; }
trap cleanup INT TERM

# Outer keepalive loop: re-establish on drop (autossh-style). ServerAlive* detects a dead peer fast.
while true; do
  push_key   # fresh 60s key right before ssh, to stay inside the ec2-instance-connect TTL window
  echo "[tunnel] establishing ssh -R $FWD_PORT:127.0.0.1:$DOLT_PORT @ $(date -u +%H:%M:%SZ)"
  # -N: no remote command (tunnel only). -R: expose host's Dolt on the node's loopback:FWD_PORT.
  ssh -N \
    -R "$FWD_PORT:127.0.0.1:$DOLT_PORT" \
    -i "$KEY" \
    -o "ProxyCommand=$PROXY" \
    -o ServerAliveInterval=30 -o ServerAliveCountMax=3 \
    -o ExitOnForwardFailure=yes \
    -o StrictHostKeyChecking=accept-new \
    "$SSH_USER@$NODE" &
  TUNNEL_PID=$!
  wait "$TUNNEL_PID"
  RC=$?
  # A clean exit via our trap won't reach here. Any other exit = the tunnel dropped; re-establish.
  echo "[tunnel] dropped (rc=$RC) — re-establishing in 5s (agent should be PAUSED on Dolt-unreachable, not erroring)"
  sleep 5
done
