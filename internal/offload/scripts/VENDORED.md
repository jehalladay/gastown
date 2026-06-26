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

warm-pool.sh / provision-node.sh are deliberately NOT vendored — they're
offload_ops' lane and not needed for core dispatch.

## Flag contract (frozen by offload_eng — safe to wrap)
offload.sh: `-n <node>` `-p <suffix>` `-b` (bedrock) `-f` (fresh) + positional
`<repo-url> <branch> <command>`. New behavior goes behind new flags, never by
repurposing these. Fail-closed exit propagation is load-bearing.

## Re-vendor
When offload_eng changes a contract-affecting script (rare — they ping first),
re-copy the four files from the crew dir and bump the Rev above. A green
`go test ./internal/offload/` confirms the embed still extracts + the scripts
are present.
