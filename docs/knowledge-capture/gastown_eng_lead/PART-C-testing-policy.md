# Part C — Testing-Policy Paper: gastown_eng_lead

**Thesis:** this session's failures cluster into ONE class — *integration facts that
unit tests structurally cannot see* — and agentic-TDD's L1-shell + L8-agent-journey
levels are the specific instrument that catches them test-first. Below, each named
incident with the test that would have caught it FIRST, plus general heuristics.

Evidence convention: each incident cites the artifact.

## Incidents → the test that would have caught it

### F2: 7 bugs unit tests missed (the headline case)
The F2 `gt crew start --remote` verb had 7 L4 unit tests (env overlay, tunnel-key map,
spawn-plan assembly, shell-quoting, etc.) — all green — yet the live dogfood found 7
bugs serially:
1. tunnel keepalive inherited the verb's stdout → command hung. CAUGHT BY: L1-shell
   (run the verb, assert it RETURNS).
2. v52-guard checked the version string (1.0.5 for both v49/v52) not the build commit.
   CAUGHT BY: L8 (the guard must pass on a real v52 node + fail on a v49 one).
3. startup embedded the HOST's claude abs path → exec 127 on the node. CAUGHT BY: L8
   (agent must actually boot on the node).
4. env double-set GT_DOLT_PORT=3307, defeating the 13307 tunnel overlay. CAUGHT BY: L8
   (agent's bd must reach the HOST Dolt through the tunnel — count increments host-side).
5. claude ran one-shot (prompt-arg) instead of the persistent REPL. CAUGHT BY: L8
   (agent must STAY ALIVE as a loop, not exit after one turn).
6. ran as root → claude refuses skip-perms. CAUGHT BY: L8 (boot-to-ready).
7. first-run interactive gates (welcome/trust/bypass) blocked readiness. CAUGHT BY: L8
   (discover/invoke without a human bridge).
LESSON: every one of these is an L1/L8 fact. A unit test asserts "the assembled command
string looks right"; only a shell/journey test asserts "the thing actually spawns,
boots, and does work through the real network." **For integration features, the unit
test is necessary but is NEVER the gate.**

### bd-v52 schema skew (town-wide corruption)
The town DB was v52, every installed bd v49 → bd WRITES half-wrote (partial beads).
CAUGHT BY: an L1-shell "schema-compat" smoke — `bd create` then read-back-by-content
(dolt sql against issues) on a representative DB, run in CI against the live schema
version. A forward-thinking suite asserts the embedded-lib schema version == the DB
migration version as a startup invariant (gt doctor could check this — it's a cheap
`bd version` commit-vs-DB-migration comparison).

### SDK-float-drift x4 (stale-wheel false PASS) [cross-crew: offload/eng]
Repeated false-greens from a stale SDK build. CAUGHT BY: a pre-run artifact-freshness
assertion (the `-f --verify-sdk <symbol>` idea — assert a known symbol/param is present
in the CONSUMED build before trusting the run). HEURISTIC: a test that runs against a
cached/built artifact must FIRST assert the artifact is the intended version (content,
not filename) — else "it passed" is unfalsifiable.

### jetsam near-misses (control plane + Dolt killed)
Memory exhaustion → jetsam SIGKILL, twice. CAUGHT BY: there was no observability at all
— F10 now adds the `memory-pressure` doctor check + util.CheckMemoryPressure with a
test-injection override (GT_MEMPRESSURE_TEST_SWAP_PCT) so the WARN/CRITICAL paths are
shell+unit testable WITHOUT inducing real OOM. HEURISTIC: a resource failure mode you've
hit once must get (a) a guard that's VISIBLE before the kill, and (b) an injectable test
that exercises the threshold deterministically. You can't test "jetsam fired" — but you
CAN test "the guard warns at 92% swap" via injection.

### phantom-close variants (mr0s lost 3x) [cross-crew: refinery/worker_d/me]
Two vectors: ff-race AHEAD-guard (gekp) + probe-only auto-close (vtzy). CAUGHT BY: an
L1/L7 merge-flow test — land a probe-only MR, assert the source bug bead stays OPEN;
land a real-code MR, assert it closes. worker_d's vtzy_dogfood_test.go (real-git, 5
scenarios) is exactly this shape and is the model. HEURISTIC: every "X auto-closes Y"
rule needs a test for the case where it should NOT close (the false-positive direction
is the dangerous one).

## General heuristics (to bake into the testing policy)

1. **The dogfood gate is L8, not L4.** A feature that spawns/crosses-network/drives-a-
   tool isn't done until an agent-journey test (discover→invoke→inspect→RECOVER) passes.
   Unit-green is table stakes.
2. **Test the false-positive direction.** Auto-close, auto-merge, guards — the costly bug
   is the one where the safe action DOESN'T fire (probe-only that closed; stale-clean that
   rejected). Assert the negative case.
3. **Assert artifact identity before trusting a run.** Version STRINGS lie (bd 1.0.5 =
   v49 AND v52). Check the build commit / a known symbol / a schema migration number.
4. **Injectable thresholds for un-inducible failures.** Memory pressure, disk full, jetsam
   — give the reader a test-override env so the WARN/CRITICAL logic is deterministically
   testable without real exhaustion.
5. **Startup invariants as doctor checks.** Embedded-lib-schema == DB-schema; http.version
   pinned; bd commit matches. A `gt doctor` check is a continuously-run L1 assertion.
6. **No false-green from the test harness itself.** My F2 shell test built a `go build`
   binary the darwin guard killed → it asserted on guard-error text. A test that builds the
   SUT must build it the way production runs it (BuiltProperly), or it tests nothing.

Grounding in C-13/agentic-TDD: the protocol's ordering (L0 matrix → L1 shell → L8 journey
→ L4 unit, tests FAIL-for-the-right-reason before impl) would have converted my 7-round
F2 dogfood into a test-first 1-round build. The L8 "recover without a human bridge" axis
maps directly onto the F2 tunnel-drop-recovery requirement the mayor flagged.
