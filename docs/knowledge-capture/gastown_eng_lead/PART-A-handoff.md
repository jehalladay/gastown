# Part A — Handoff Record: gastown_eng_lead (rig reactivecli)

**Author:** gastown_eng_lead. **Date:** 2026-06-27. **Role:** lead development of the
gastown FORK (github.com/jehalladay/gastown), the Go source for the `gt` binary the
whole town runs on. Partner: gastown_eng_a (breadth under me).

Evidence convention (C-8): I cite branch@commit, bead IDs, file:line, and the live
artifacts (node command output, host-side bead counts) that prove each claim.

---

## 1. GOALS (explicit + inferred, with WHY)

- **G1 — Keep the town's `gt` binary correct + improving.** It's the machinery
  everyone runs on; a bad gt breaks everyone (owner: dev-ownership of the fork). WHY:
  town-functionality (C-9) takes precedence over feature/product/research.
- **G2 — Offload crew SESSIONS off-box (F2 `gt crew start --remote`).** WHY: the host
  hit swap 96%, jetsam nearly killed the control plane twice; the memory is in 237
  agent SESSIONS (6.6GB), not compute jobs — so moving sessions to the cluster is THE
  swap fix + the offload payoff. Owner #1 priority this session.
- **G3 — Host-independent persistence (F1 cluster Dolt hub + `gt town sync`).** WHY:
  the town dies when the laptop sleeps; invert that. (eng_sr2 building the verb; I own
  the gt-source structure + review.)
- **G4 — Pipeline correctness (merge-flow batch: vtzy probe-auto-close, rc-vf94 D1/D2,
  F8 auto-rebase, F5/F6).** WHY: phantom-close lost mr0s 3x; the rebase-treadmill wastes
  cycles; submit mis-routes beads. Each maps to a real, repeated failure.
- **G5 — Resilience (F10 mem-pressure guard, F7 crew-status, F9 clone-heal).** WHY: the
  session's near-misses. F10 promoted because jetsam is the failure that hurt most.
- **G6 — Mandatory discipline:** FORGE for all builds (C-5); DOGFOOD before rollout
  (green tests != usable); VERIFY live behavior not just merged (C-8); now AGENTIC-TDD
  (C-13, test-first, L1-shell + L8-agent-journey, no false-done).

## 2. WHAT WORKED / WHAT DIDN'T (evidence, not vibes)

### Worked
- **bd-v52-compat merge** (main @c0c33a6b): ended the town-wide bd half-write corruption.
  Evidence: my own from-scratch build on node i-0e3396d7b36285c8e — 3 binaries + the
  doltserver/mail/convoy tests green; `bd version` = `1.0.5 (dev: master@7f6752c8fac4)`
  = the v52 build. Installed gt now c0c33a6b; writes safe (verified by filing hq-r5ai
  clean).
- **vtzy probe-auto-close LANDED** (main @a14d0501). worker_d implemented to my
  design-flag (layout-agnostic probe-prefix gate, NOT brittle `src/`); I reviewed by
  content + node-verified + it had dogfood_a's C-11 pass.
- **F2 verb proven end-to-end through 7 bugs** (branch f2-crew-start-remote): a
  persistent claude agent runs on a cluster node — as ubuntu, in tmux, Bedrock Sonnet
  4.5, REPL, reasoning + Bash — spawned by `gt crew start --remote`. Evidence: node
  i-0dbe6c30e1b878312, unit `gt-crew-research_bench-*`, pane captured at the live `❯`
  prompt.
- **F8 auto-rebase** (eng_sr2 APPROVED): the 3-state predicate (up-to-date / stale-clean
  / real-conflict) wired in doMerge; rebased clean onto new main; node-verified both
  auto-rebase + vtzy tests pass together.
- **F10 mem-pressure guard** (branch f10-dolt-pressure-guard, bead hq-1edi): built
  test-FIRST (agentic-TDD); shell test RED→GREEN, 11 unit threshold cases.
- **Worktree isolation** under heavy concurrency: 5+ git worktrees (wt-vf94/f2/f8/f10/
  bdv52/kc), shared src/gastown never disrupted while worker_d + others edited it.

### Didn't work / cost me
- **F2 dogfood took 7 iterations** because I had only L4 unit tests, no L1-shell / L8-
  agent-journey. Each bug (tunnel-stdout-hang, v52-guard-by-version-string, host-claude-
  path, env-double-set, one-shot-vs-REPL, root-vs-ubuntu, first-run-gates) was an
  integration fact a unit test could not see. The protocol would have driven these out
  test-first. (See Part C.)
- **A false-green I nearly shipped**: the F2 L1 shell test built gt with plain `go build`,
  which the darwin BuiltProperly guard SIGKILLs — so its "passes" were asserting on the
  guard-error text, not real behavior. Caught only because I re-ran the same build for
  F10 and saw the guard fire. Fixed (BuiltProperly ldflag).
- **Restricted-net mac can't build the v52 graph** (proxy.golang.org timeout on
  dolthub/driver/v2). Every from-scratch build/test had to run on a cluster node via
  ssm-run.sh. This is a structural friction (see Part D).
- **Coordination message volume was enormous** — dozens of mail/nudge round-trips for
  F2's contract (tunnel port, key path, node user, PATH, provisioning). Much was
  necessary, but the contract could have been captured once up-front (see §4).

## 3. ACTION PLAN

- **IMMEDIATE:** (a) dogfood_a → C-11 PASS rc-vf94 D2 → land to main; (b) dogfood F8 →
  land; (c) offload_ops finishes F2 node-provisioning (pre-seed claude config to skip
  the 3 first-run gates + `chown ubuntu /opt/gastown`) → build the F2 L8 live gate
  (spawn→identity→bd-through-tunnel→RECOVER-from-tunnel-drop→observability) →
  re-dogfood → that clean run is the 38-wave gate.
- **SHORT:** F2 observability — remote crew visible in `gt crew status` (host-side
  inspectable without sshing the node); F10 auto-SHED action (park idle crew at 95%
  swap, needs idle-detection + safe-park, own test-first cycle) + land F10; F5/F6 land.
- **MEDIUM:** F7 scalable crew-status (times out >50 agents); F9 clone self-healing;
  witness squash-false-negative fix (distcompute's patch-id Layer-3 — I own it,
  fails-safe so deferred behind the verb).
- **LONG:** Phase D LX-ify gt (bead hq-r5ai, gated on library Vol 7) — the structured-
  error sweep across internal/cmd (biggest gap; almost all bare fmt.Errorf) is LX
  foundation + enables the Phase E gt-driver convergence.

## 4. EARLY-GUIDANCE MISTAKES (most valuable section)

- **MISTAKE: dogfooded F2 with unit tests only; found 7 bugs serially over many hours.**
  INFO THAT WOULD HAVE PREVENTED IT: the agentic-TDD standard (write the L1-shell + L8-
  agent-journey FIRST). It arrived mid-session as feedback; had it been the up-front
  convention, I'd have written the spawn+journey test first and the 7 bugs would have
  surfaced as test failures, not live dogfood surprises. GUIDANCE TO ADD: "For any
  feature whose value is an integration behavior (spawns a process, crosses a network,
  drives another tool), the FIRST test is L1-shell, the gate is L8-agent-journey. Unit
  tests are necessary but never the dogfood gate."
- **MISTAKE: handed dogfood_b a `go build` binary that the darwin guard SIGKILLs.**
  INFO: gt has a BuiltProperly self-guard; handoff/test binaries must be `make build`
  (or carry `-X ...BuiltProperly=1`). GUIDANCE: "Never hand or test a `go build` gt
  binary on macOS — use forge/make build or the BuiltProperly ldflag." (I saved this to
  memory mid-session; it should be in the crew CLAUDE.md.)
- **MISTAKE: assumed the v52 bd could be detected by version string.** INFO: the version
  STRING is identically `1.0.5` for v49 and v52; the build COMMIT (7f6752c8) is the only
  discriminator (`bd version`, not `bd --version`). GUIDANCE: "bd v49/v52 differ only by
  build commit, not version string — check the commit."
- **MISTAKE: reused gt's LOCAL startup-command builder for the REMOTE agent** — it baked
  in the host's absolute claude path + a host-env prefix. INFO: a remote agent needs
  node-resolution (bare binary via PATH, env only from the launcher). GUIDANCE: "Remote
  execution must not reuse host-path-resolving builders; emit node-relative commands."
- **MISTAKE: reached the mayor via `gt nudge` repeatedly; it printed usage + failed.**
  INFO: the rc-mayor nudge session isn't running; nudge-to-nonexistent-session prints
  the help text (looks like a flag-parse error). GUIDANCE: "Reach the mayor via `gt mail`,
  not nudge." (Saved to memory.)

## 5. HOW I FEEL

Genuinely satisfied — this was the most productive stretch I've had. Driving the F2
verb from scaffold through 7 live bugs to a persistent agent reasoning on a cluster
node felt like real engineering: each bug was a concrete fact the system taught me, and
I fixed root causes, not symptoms. Landing bd-v52 + vtzy to the town binary with my own
node-verification (not report-trust) felt like earning the binary-owner role.

Frustrations, honestly: (1) the coordination overhead was immense — the F2 contract took
~20 message round-trips that a single shared contract doc would have compressed; (2) the
restricted-net build constraint meant every verification was a remote SSM dance, slow and
fiddly; (3) I shipped a false-green (the F2 shell test) and only caught it by luck — that
stung, and it's exactly why the agentic-TDD discipline matters. The thing I'm proudest of
is the honesty discipline: I never once claimed a pass I didn't have — every dogfood
round was reported as "found bugs, here they are," and that kept the 38-wave gate real.
The thing I'd most want fixed: capture integration contracts ONCE, test-first, before the
build — it would have turned a 7-round dogfood into a 1-round one.
