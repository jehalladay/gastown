# Part C — Testing-Policy Paper: gastown_eng_a

One testing-policy insight that's mine (the lead's Part C covers the F2 unit-vs-L1/L8 gap
+ the false-green; I don't repeat those). Mine is the **dogfood-binary-identity** failure
class — the gap my hq-wisp-pifgpn catch exposed.

## THE INSIGHT: a dogfood certifies the binary it RAN ON, not the code it will merge to

This session's near-miss (mail hq-wisp-pifgpn): the F2 verb was dogfooded on a binary built
from f2's own tree, which still pinned beads v49 + driver v1.88.1. The merge target (v52
main) carries a driver v1→v2 **major** bump. So the binary that passed the dogfood is NOT
the binary the merge produces — yet the dogfood "green" would have been read as certifying
the merged artifact. I caught it by hand; nothing in the test/merge tooling caught it.

This is a distinct failure CLASS, separate from the phantom-close / SDK-float-drift /
bd-v52-skew incidents the program names: those are "the code is wrong"; this is **"the test
ran against the right code but the wrong build"** — a binary-provenance gap. It's the same
shape as the lead's false-green (a test that built the SUT with `go build` and asserted on
the guard-error text): in both, the *test passed honestly* but against an artifact that
isn't the one that ships.

## WHAT A FORWARD-THINKING SUITE WOULD HAVE CAUGHT

- **A merge-gate predicate** (agentic-TDD framing: an L1-shell / pre-merge check): "the SHA
  of the binary the last dogfood ran on" vs "the most recent go.mod dependency-major change
  on the merge target." If the dogfood binary is older → FAIL the gate, demand a rebuild on
  the merged tree + re-smoke. This is the C-8 "predicate asserts it" upgrade of my mail.
  Where in the hierarchy: this is **L1-shell on the merge tree** (cheap, deterministic) +
  the re-smoke is the **L8-agent-journey** on the rebuilt binary — exactly the two layers
  the lead found missing for F2, applied to provenance instead of behavior.
- **A pure-Go-dep assertion** (Part B/D): `go list -deps ./cmd/gt | grep -qv go-icu-regex`
  as an L1 check would assert the CGo dep is provably out of the build graph, not just
  "the default build happened to work on this host." Test-first, this would have been the
  RED that the Makefile bake (`23c945e5`) turns GREEN — instead the change is currently
  only certified by a manual node build.

## GENERAL HEURISTICS (to avoid this failure class)

1. **Test the artifact you ship, built the way you ship it.** A green from a `go build`
   binary, a pre-merge binary, or a different-host binary is evidence about *that* artifact
   only. Bind every "PASS" to a build SHA + build flags, and re-verify when either changes.
2. **A dependency-major bump invalidates prior dogfoods on its merge target.** Make it a
   hard gate, not a remembered courtesy — a human reading a warning mail is not a control.
3. **Prefer asserting an ABSENCE over observing a SUCCESS.** "build works" is weaker than
   "the bad dep is provably not in the graph"; the negative assertion fails loudly on
   regression, the positive one passes silently on a host that happens to be fine.

## GROUNDING IN AGENTIC-TDD (C-13)

Both this provenance gap and the lead's false-green are cases where unit/L4 tests are
GREEN and honest, yet the shipped behavior is uncertified — precisely the "correctness
cannot be trusted just because tests pass" premise of the agentic-TDD protocol. The fix is
the same direction the lead reached for F2: push the gate down to **L1-shell** (a cheap
deterministic check on the real merge tree / real build flags) and up to **L8-agent
-journey** (the rebuilt binary actually spawns + does the thing), and stop treating L4 unit
green as a rollout gate.
