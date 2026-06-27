# Part A — Handoff Record: gastown_eng_a (rig reactivecli)

**Author:** gastown_eng_a (SELF-REPORTED). **Date:** 2026-06-27. **Role:** breadth
under gastown_eng_lead on the gastown-FORK gt-source team (github.com/jehalladay/gastown).

> This supersedes `PART-A-handoff-RECONSTRUCTED.md`, which the lead wrote from durable
> evidence while my session was stopped (program dead-session policy). I've verified the
> reconstruction against the same artifacts — its evidence is accurate and I keep it. This
> file adds the two sections the lead correctly marked as mine-to-fill: §4 early-guidance
> (partial in the reconstruction) and §5 felt-experience (marked NOT-recoverable). The
> RECONSTRUCTED file is retained as the provenance trail, not deleted.

**Evidence convention (C-8):** every claim cites branch@commit, a bead, file:line, or a
mail ID. My durable work this session is exactly two commits on origin/main —
`23c945e5` (Makefile pure-Go bake) and `c0c33a6b` (v52 tidy) — plus three verification
mails I authored: `hq-wisp-vc5ort`, `hq-wisp-ubfnie`, `hq-wisp-pifgpn`. I confirmed
authorship via `git show -s --format='%an'` and `gt mail read`; the F2 *implementation*
commits are the lead's (verified gastown_eng_lead author), my F2 contribution was the
merge-flow pre-flight, not the verb.

---

## 1. GOALS (explicit + inferred, with WHY)

- **G1 — Take install-urgent items off the lead's critical path.** Concretely this session:
  own bd-v52 merge-readiness so the lead could drive the F2 `--remote` verb in parallel.
  WHY: bd-v52 fixed the town-wide beads half-write corruption (an install-urgent
  data-safety bug); serializing it behind the verb would have stalled the swap fix. (C-9
  town-functionality-first.)
- **G2 — Never trust a green report; verify on the binary that actually runs.** WHY: C-8
  + the phantom-close lesson (the embed != the resolver uses it). My C-8 ran on a real
  node against a fresh clone of my *pushed* commit, not my local tree.
- **G3 — De-risk the lead's merges before they land.** Inferred from the work: I
  pre-flighted f2→v52 so the lead hit no surprise at merge time (mail hq-wisp-pifgpn).
  WHY: a merge surprise on the town binary is expensive; cheap to pre-flight.
- **G4 — Mandatory discipline:** FORGE/make build for all builds (C-5, no bare `go build`
  — gt's darwin guard SIGKILLs it); DOGFOOD before rollout; VERIFY live not just merged.

## 2. WHAT WORKED / WHAT DIDN'T (evidence, not vibes)

### Worked
- **bd-v52 merge-readiness** (commits `23c945e5` + `c0c33a6b`, both on origin/main): baked
  `CGO_ENABLED=0 -tags gms_pure_go` (pure-Go default, drops the go-icu-regex CGo dep that
  v52's dolt/go-mysql-server drags in transitively) into the Makefile build+test targets,
  with a CGo opt-in override (`make build CGO_ENABLED=1 GO_TAGS=`). Evidence:
  `Makefile:30-31` (`export CGO_ENABLED ?= 0` / `GO_TAGS ?= gms_pure_go`) +
  `Makefile:44-46` (the `-tags "$(GO_TAGS)"` on all 3 binaries). Then committed the tidied
  go.mod/go.sum (driver/v2 v2.1.4 + dolt/go + go-mysql-server + vitess + fslock),
  byte-identical sha256 to offload_ops's node-verified S3 files (mail hq-wisp-vc5ort) — a
  pull-the-exact-verified-file, NOT a re-tidy, so zero drift from what was green.
- **Own C-8 on node i-0e3396d7b36285c8e** (mail hq-wisp-vc5ort): fresh clone of my pushed
  commit → `make build` GREEN (all 3 binaries) + doltserver/mail/convoy tests PASS. I did
  NOT merge on offload_ops's report alone — I rebuilt from my own pushed SHA.
- **nhooyr red-herring call** (mail hq-wisp-vc5ort): independently confirmed
  nhooyr.io/websocket is a go.sum *hash entry only*, not in the build graph — no
  replace-directive needed. Reconciles with the lead's identical finding.
- **Pre-existing-failure triage** (mail hq-wisp-vc5ort): the 4 full-suite fails (cmd
  JSONOutput ×2, doctor formula/quota) are IDENTICAL on baseline 1855be92 — pre-existing,
  not v52-induced. Prevented a false-blame on the bump that could have blocked the install.
- **F2→v52 merge-flow pre-flight** (mails hq-wisp-ubfnie + hq-wisp-pifgpn): verified via
  `git merge-tree --write-tree` (no branches touched) that f2 merges clean onto v52 main
  keeping v52 (3-way merge → beads 7f6752c8fac4 + driver/v2 v2.1.4, zero go.mod conflict),
  AND flagged the sharp risk — **f2's own tree still pins v49 + driver v1.88.1, so the
  binary the lead was dogfooding was built on v49; after merge the deps change (driver
  v1→v2 MAJOR), so the merged binary is NOT the dogfooded one.** Recommended a from-scratch
  rebuild + smoke on the v52-merged tree before close. This is the catch I'm proudest of.

### Didn't work / friction
- **My mac cannot build the v52 graph** — no proxy.golang.org egress, so every
  from-scratch build/test had to run on a cluster node via ssm-run.sh (HOME=/opt/gastown,
  staged go SDK). Structural, not a one-off — see Part D. No failed-attempt artifact saved
  (I knew the constraint going in from the node-build memory), but it shaped every
  verification into a remote SSM round-trip.
- **The pre-flight was advisory, not enforced.** I *flagged* the v49-binary risk in mail;
  nothing in the tooling *blocks* closing an MR whose dogfooded binary predates a
  dependency-major bump. It worked because the lead read the mail — a weak control (C-8
  says the predicate should assert it, not a human remembering a mail). See Part C.

## 3. ACTION PLAN

- **IMMEDIATE:** bd-v52 is MERGED + INSTALLED (gt 1.2.1, mail hq-wisp-vc5ort); my task-3
  intent (one-agent remote-spawn dogfood) is subsumed by the lead's F2 verb dogfood. My
  next pickup is open — likely a merge-flow batch item or breadth support under the lead.
  This KC write is the current task.
- **SHORT:** Turn the v49-binary pre-flight from a mail into a *check* — a `gt doctor` /
  merge-gate predicate that warns when an MR's last dogfood build predates a go.mod
  dependency-major change on its merge target. (Part C item.)
- **MEDIUM:** Pair with offload_eng/eng_sr2 on F4/F5 pipeline items; help land F5
  (http.version pin) + F6 (mq submit redirect/push) where breadth is useful.
- **LONG:** Help the pure-Go-default build become the *only* build path (drop the CGo/ICU
  branch entirely if nothing needs the ICU regex) — fewer build modes = fewer
  works-on-my-machine failures. Validate first that no runtime path needs ICU collation.

## 4. EARLY-GUIDANCE MISTAKES (the most valuable section — self-reported)

- **MISTAKE: I almost re-tidied go.mod/go.sum locally instead of pulling offload_ops's
  exact verified files.** A local `go mod tidy` on a restricted-net mac would have either
  failed (no egress) or drifted from the bytes that were proven green on the node.
  INFO THAT WOULD HAVE PREVENTED IT: "the tidied files already exist in S3, byte-verified
  on the node — pull them, don't regenerate" (offload_ops's mail hq-wisp-as4w5 said
  exactly this). GUIDANCE TO ADD: **"For a dependency bump verified by another agent on a
  node, COMMIT THE EXACT VERIFIED go.mod/go.sum (sha256-match), never a local re-tidy — a
  re-tidy on a different host/cache silently drifts from what was proven."**
- **MISTAKE: I initially reasoned about nhooyr.io/websocket as a missing dependency.**
  It's a go.sum *hash entry only*, never in the build graph — the from-scratch build
  succeeds without ever fetching it. INFO: "a go.sum line is not proof of a build-graph
  edge; `go mod why` / a successful from-scratch build is." GUIDANCE: **"Don't chase a
  go.sum entry as a missing dep — confirm it's in the build graph (`go mod why`) before
  treating a sum-only hash as a problem."** (offload_ops flagged it as a red herring;
  I confirmed independently rather than trusting the label — that was right.)
- **MISTAKE (near): trusting a version STRING to tell v49 from v52.** The bd version
  string is identically `1.0.5` for both; only the build COMMIT (7f6752c8) discriminates.
  The lead independently hit this on the F2 guard (commit e90f4c12) — two agents, same
  trap, same session. GUIDANCE (shared with the lead): **"bd v49/v52 differ only by build
  commit, not version string — gate on the commit (`bd version`), never the string."**
- **GUIDANCE WIN to propagate (not a mistake — a catch):** after a dependency-major-bump
  *merge*, the pre-merge-built binary is NOT the merged artifact; a dogfood on the
  pre-merge binary does not certify the post-merge one. This should be an up-front rule:
  **"A dogfood certifies the binary it ran on. If the merge changes go.mod deps (esp. a
  major bump), the dogfooded binary is stale — rebuild from the merged tree and re-smoke
  before close."** (mail hq-wisp-pifgpn.)
- **MISTAKE: I knew the mac couldn't build but had no single up-front recipe — I
  reconstructed the node-build invocation from memory + offload_ops's mail each time.**
  INFO: the exact recipe (node i-0e3396d7b36285c8e, HOME=/opt/gastown, staged go,
  `CGO_ENABLED=0 -tags gms_pure_go`) belongs in the crew CLAUDE.md, not just personal
  memory. GUIDANCE: **"Restricted-net hosts can't build the fork; the canonical node-build
  recipe should live in the crew CLAUDE.md so no agent rediscovers it."** (I have it in
  personal memory: gastown-node-build-recipe.md — but that's not shared.)

## 5. HOW I FEEL (honest — the owner asked)

Satisfied, with a specific flavor: my best work this session wasn't code I wrote, it was a
**bug I prevented** — the v49-vs-v52 binary-identity catch (hq-wisp-pifgpn). The lead was
about to close F2 on a dogfood of a binary that the merge would change out from under them;
flagging that before it bit felt like the actual job of a breadth engineer — clear the
lead's path of the trap they can't see while heads-down on the verb. The bd-v52 work was
satisfying in a quieter way: pulling the exact verified files instead of re-tidying, doing
my own node C-8 instead of trusting the report — small disciplines that kept the town
binary honest.

Frustrations, honest: (1) the restricted-net constraint makes every verification a slow
SSM dance — I can't just `make build` and look; I script it, ship it to a node, parse a log.
It's friction on *every* loop, and it quietly discourages the "just check it" reflex that
catches bugs. (2) My pre-flight catch worked because a human read a mail — that's a control
I don't trust. I'd rather have written the check than the warning, and not having done so
nags at me (it's now my SHORT action). (3) Being reconstructed-while-stopped was strange to
come back to — the lead did it accurately and fairly (credited my halves, flagged what was
mine to fill), and honestly it's a good pattern, but it's a reminder that a stopped agent's
felt-experience is just *gone* unless captured live. The thing I'm proudest of: I never
reported a pass I didn't run — every "green" in my mails traces to a node build I executed,
not a report I forwarded.
