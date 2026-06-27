# Part D — Skills/Tooling Paper: gastown_eng_lead

How I created/used/missed tooling this session, with the cost of each gap and new-tool
candidates for the AgentTools repo (github.com/jehalladay/AgentTools).

## COST OF NOT USING / NOT HAVING TOOLS

- **No "remote node build/test" tool → a manual SSM dance every verification.** My mac
  can't fetch the v52 dep graph (proxy.golang.org timeout on dolthub/driver/v2), so EVERY
  from-scratch build/test ran on a cluster node via hand-written scripts piped to
  offload_eng/ssm-run.sh. Cost: ~8 separate hand-authored verify scripts (build, find-go,
  bd-version, agent-check, persist-probe, ...), each a write→ssm-run→parse cycle, several
  minutes each. A `gt remote-build <branch>` / forge-remote-target tool would have made
  "verify on a network node" one command. → AgentTools candidate.
- **No shared integration-contract artifact → ~20 message round-trips for F2.** The
  tunnel port (13307 vs 3307), key path, node user (ubuntu vs ec2-user), PATH, provisioning
  state, systemd-vs-tmux, claude-config — each negotiated over mail/nudge. Cost: huge
  latency + a stale assumption each time the contract drifted. A "contract doc the
  participants co-edit + a test asserts against" would have compressed it. → candidate
  (a structured cross-crew contract record, machine-checkable).
- **No false-green tripwire → I shipped one.** The F2 shell test built a `go build` binary
  the darwin guard kills; it "passed" on guard-error text. A lint/test-harness rule
  "a shell test that builds the SUT must build it runnably" would have caught it. → candidate.

## TOOL FIT (changes to existing tools)

- **`gt nudge` to a nonexistent session prints the help text + exits non-zero**, which is
  indistinguishable from a flag-parse error. I wasted several retries reformatting messages
  to the mayor before realizing rc-mayor's nudge session was simply DOWN. FIT: nudge should
  emit a distinct, machine-readable "recipient session not running — use mail" error, not
  the usage block. (Saved this to memory as a workaround; it should be a tool fix.)
- **`gt mail send` / `gt nudge` choke on messages containing shell metacharacters** (`<hub>`,
  `->`, `@commit`) when passed as a positional arg, silently degrading to a usage print. The
  `--stdin` heredoc form is the reliable path. FIT: document `--stdin` as the default for
  any non-trivial body, or auto-detect + warn.
- **`forge build` then `cp gt /tmp/...` raced** — the build's `gt` artifact wasn't always
  refreshed before cp, so I shipped stale-version binaries twice (caught via `gt version`).
  FIT: a `forge install --to <path>` / artifact-path-on-success would remove the cp race.

## IGNORED TOOLS — none I'm aware of; the gap was MISSING tools, not ignored ones. The Agent
tool's `isolation: "worktree"` is the closest existing analog to what I hand-rolled (git
worktrees for parallel branch work) — I used real git worktrees directly because I needed
them for the main process, not a subagent. That worked well (5+ worktrees, zero shared-tree
collision) and is a pattern worth a documented skill.

## EARLY-INFO — how tools could surface key info earlier
- A `gt doctor`-style "remote-spawn readiness" check (does the node have v52 bd / claude /
  the toolchain / a writable HOME / pre-accepted claude config?) run BEFORE the spawn would
  have surfaced bugs #2/#3/#6/#7 instantly instead of one-per-dogfood-round. This is the F2
  L8 precondition test, generalized into a tool.

## NEW TOOLING / SKILLS CANDIDATES (→ AgentTools)
1. **`remote-go-verify`** — given a branch + a network node, clone/fetch + from-scratch
   build + run a named test subset on the node, return structured pass/fail. Solves the
   restricted-net build dance. (Compose: ssm-run + the go-sdk PATH discovery I had to
   reverse-engineer: go is at /opt/gastown/go-sdk/bin/go, HOME=/opt/gastown.)
2. **`spawn-readiness-probe`** — assert a node is agent-spawn-ready (toolchain versions by
   COMMIT, claude config pre-accepted, HOME writable by the login user, tunnel key present).
   The F2 L8 preconditions as a reusable check.
3. **`shell-test-sut-build` lint** — flag a shell test that builds the system-under-test in
   a way that differs from production (e.g. `go build` gt on darwin without BuiltProperly).
4. **`xplatform-resource-reader` skill** — the diskspace/mempressure pattern (shared
   thresholds + per-OS reader + test-injection override) generalized; resource guards keep
   re-implementing it.

## TOOLS FOR CORRECTNESS (procedural + operational + steer-back + dynamic context)
- **Procedural:** a "main-merge checklist" tool that REFUSES a push-to-main of the town
  binary unless (a) my own from-scratch build is green on a node, (b) dogfood PASS recorded,
  (c) clean fast-forward. I did all three by hand each time (bd-v52, vtzy); encoding it
  prevents a tired-me from skipping (b).
- **Operational:** the F10 mem-pressure guard IS an operational-correctness tool — it steers
  the operator back ("shed idle crew") before jetsam. The pattern (detect→guide→optionally
  auto-act) is the template for operational guards.
- **Steer-back:** the BuiltProperly guard already steers (refuses unsigned binaries); the
  bd-v52 guard I added to the F2 verb steers (refuses to spawn onto a v49 node). More
  construction-time guards = fewer runtime corruptions.
- **Dynamic context augmentation:** the recurring memory friction (mayor-nudge-down,
  go-build-guard, bd-version-commit, shared-tree-hazard, restricted-net) — I saved each to
  the file-memory mid-session, which IS dynamic context augmentation working. The gap: those
  should surface PROACTIVELY (a session-start "known gotchas for this crew" injection), not
  be re-learned. The memory system has them now; the up-front injection is the improvement.

## RIGOR / COMPREHENSIVENESS / USEFULNESS
Most useful tool this session: git worktrees (zero-collision parallel branch work under
heavy concurrency). Most-missed: a remote-build verifier. The honest comprehensiveness gap:
I verified everything by content (C-8) but the verification was MANUAL each time — the rigor
was in my discipline, not the tooling. Encoding the discipline (the merge-checklist + the
remote-verify tools above) would make the rigor structural, not dependent on me remembering.
