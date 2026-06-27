# Vendored offload scripts

These scripts are the **source of truth for offload mechanics**, owned by
`offload_eng` (rig reactivecli). They are vendored — copied verbatim — from
`reactivecli/crew/offload_eng/` and embedded into the `gt` binary so `gt offload`
runs host-independently (on a cluster node or any crew checkout) with no
dependency on offload_eng's crew directory.

`gt offload` is a thin wrapper: it extracts these to a tempdir and shells out.
Do NOT reimplement the SSM/presign/Bedrock logic in Go.

## Vendored from
- Repo: reactivecli (offload_eng crew dir)
- Rev: `dd34be5` (offload.sh; re-vendored 2026-06-26 after F4 co-verify — adds
  `-f` hatch-env-prune on top of the uv-cache bypass. Flag contract unchanged.
  pick-node/ssm-run/setup-secrets unchanged since 33bb5d6.)
- Files: offload.sh, pick-node.sh, ssm-run.sh, setup-secrets.sh

warm-pool.sh is deliberately NOT vendored — offload_ops' lane, not needed for
core dispatch.

provision-node.sh IS vendored (see the F2 section below) — `gt crew start
--remote` must run it as step 0 to clone+prime the crew workspace, so it needs
to ship in the embed for host-independence.

## Flag contract (frozen by offload_eng — safe to wrap)
offload.sh: `-n <node>` `-p <suffix>` `-b` (bedrock) `-f` (fresh) + positional
`<repo-url> <branch> <command>`. New behavior goes behind new flags, never by
repurposing these. Fail-closed exit propagation is load-bearing.

## Re-vendor
When offload_eng changes a contract-affecting script (rare — they ping first),
re-copy the four files from the crew dir and bump the Rev above. A green
`go test ./internal/offload/` confirms the embed still extracts + the scripts
are present.

---

# Vendored remote-spawn scripts (F2)

Source of truth for the F2 host-up remote-crew-spawn mechanics, owned by eng_sr2 +
offload_ops (rig reactivecli). Vendored — copied verbatim — and embedded into `gt`
so `gt crew start --remote` runs host-independently (any host with gt, no dependency
on a crew dir). Same pattern as the offload suite above in this file.

`gt crew start --remote` extracts these to a tempdir and drives them; it does NOT
reimplement the ssh-R/SSM mechanics in Go.

## Vendored from
- Repo: reactivecli (committed master copy at refinery/rig/scripts/)
- Rev: `55dcd9c` (open-remote-tunnel.sh; F2 host-side reverse tunnel, e2e-proven)
- provision-node.sh — **Source of truth: `reactivecli` repo, path
  `refinery/rig/provision-node.sh`** (offload_ops' committed master copy; the
  `crew/offload_ops/provision-node.sh` working copy is identical). Rev `a3cc9fc`
  (`fix(offload): --agent refreshes ssm-agent Port plugin`). It's the `gt crew
  start --remote` step-0 provisioner: `--agent --crew <name>` stages toolchain +
  clones /opt/gastown/<crew> + writes the .beads tunnel-redirect + pre-seeds claude
  config. Only sibling dep is ssm-run.sh, also vendored here, so `$HERE/ssm-run.sh`
  resolves in the extracted tempdir. All config is env-overridable: AWS_PROFILE_SCIENCE,
  AWS_REGION, OFFLOAD_BUCKET, OFFLOAD_STATE_DIR, BEADS_DB.
  - OWNED by offload_ops. **Re-vendor trigger:** any change to
    `refinery/rig/provision-node.sh` that touches the `--agent`/`--crew` clone+prime
    behavior, the .beads-redirect it writes, or the toolchain it stages. When that
    happens: re-copy the file here + bump this Rev. **Drift risk:** if the embed lags
    offload_ops' live script, a spawn provisions the OLD way and can pass dogfood once
    then rot — so the Rev above MUST track offload_ops' HEAD for this file.
    `go test ./internal/offload/` (TestExtractScriptsRemote) confirms the embed
    extracts + `bash -n` parses, but does NOT detect content drift — that's a
    human/offload_ops re-vendor discipline, same as the other 4 scripts.

## Key contract (eng_sr2-confirmed — no script change, gt drives it)
open-remote-tunnel.sh `<instance-id> [fwd-port]` opens a host-initiated
`ssh -R <fwd-port>:127.0.0.1:3307` over an SSM ProxyCommand (defaults: fwd-port
13307, SSH user ubuntu, keepalive/respawn loop). The spawned agent then exports
`GT_DOLT_HOST=127.0.0.1 GT_DOLT_PORT=13307`.

CRITICAL for embedded-extract hosts: the script auto-detects the persistent
`.offload-tunnel-key` at HARDCODED reactivecli crew paths — which DON'T resolve when
extracted to a tempdir, so it would fall to the fragile 60s-TTL ephemeral key. The
script honors `TUNNEL_SSH_KEY` as priority #1 (auto-sets TUNNEL_AUTHORIZED_KEY=1), so
gt MUST export `TUNNEL_SSH_KEY=<key-path>` when driving the extracted tunnel. gt reads
`GT_TUNNEL_KEY` (the spawn-host key path offload_ops stages) and maps it →
`TUNNEL_SSH_KEY`. See remoteTunnelEnv() in internal/cmd/crew_remote.go.

## Re-vendor
Re-copy from refinery/rig/scripts/ + bump Rev when eng_sr2 changes a contract-
affecting line (rare — the TUNNEL_SSH_KEY priority + 13307/ubuntu defaults are stable).
