# Part D — Skills/Tooling Paper: gastown_eng_a

How I used / missed tooling this session. Focus: the restricted-net build constraint that
shaped every verification, and the gaps that turned a discipline (verify on the node) into
a manual chore. AgentTools repo: github.com/jehalladay/AgentTools. (Overlaps the lead's
Part D on the "remote node build" gap — reconciled there; I add the go-not-on-PATH snag and
the verified-files-vs-re-tidy gap, which are specific to my bd-v52 work.)

## COST OF NOT USING / NOT HAVING TOOLS

- **No "remote node build/test" command → every verification was a manual SSM dance.** My
  mac can't fetch the v52 dep graph (no proxy.golang.org egress — same constraint as the
  mayor's mac, confirmed by offload_ops in hq-wisp-as4w5). So my C-8 (mail hq-wisp-vc5ort)
  and the f2→v52 pre-flight builds (hq-wisp-ubfnie) all ran on node i-0e3396d7b36285c8e via
  hand-authored scripts piped to ssm-run.sh. Cost: each verification is a
  write-script → ssm-run → wait → parse-log loop of several minutes; it quietly discourages
  the cheap "just rebuild and look" reflex that catches bugs. A `gt remote-build <branch>` /
  forge-remote-target ("build+test this ref on a network-capable node, stream the result")
  would collapse it to one command. → AgentTools candidate (reconciles with the lead's
  identical finding — two agents independently paid this cost in one session = strong signal).
- **`go` not on the node's default PATH → silent build-does-nothing failures.** The node's
  go SDK is staged at `/opt/gastown/go-sdk/bin` under `HOME=/opt/gastown`, NOT on the login
  PATH; a script that just runs `go build` finds no go (or the wrong one) and the build
  no-ops. The working recipe needs `export HOME=/opt/gastown; export
  PATH="$HOME/go-sdk/bin:$PATH"` first (offload_ops's recipe, hq-wisp-as4w5; my personal
  memory gastown-node-build-recipe.md). Cost: rediscovering/re-pasting this every session
  because it lives in *personal* memory, not the crew CLAUDE.md.
- **No "commit the exact verified artifact" affordance → the re-tidy temptation.** The
  correct move for bd-v52 was to pull offload_ops's byte-verified go.mod/go.sum from S3 and
  commit *those* (sha256-match, zero drift), NOT run a local `go mod tidy` (which on a
  restricted-net host would fail or drift). I did the right thing (mail hq-wisp-vc5ort), but
  nothing in tooling *enforces* "the committed dep files match the bytes that were proven
  green." Cost: the correctness rests on me remembering to sha256-compare, not a check.

## TOOL FIT (changes to existing tools)

- **`make build` / the build target gives no positive assertion that the CGo dep is gone.**
  It proves "the default build works," not "go-icu-regex is not in the graph" (see Part B
  RIGOR). FIT: a `make verify-pure-go` target that runs `go list -deps ./cmd/gt | grep -qv
  go-icu-regex` (or `CGO_ENABLED=0` build with cgo hard-off) so the portability guarantee is
  asserted, not assumed — and a future transitive bump that re-introduces CGo fails loudly.
- **No merge-gate that knows a dogfooded binary predates a dep-major bump.** My most
  valuable catch (hq-wisp-pifgpn — the v49 dogfood binary ≠ the v52 merged binary) was a
  *mail*, not a *check*. FIT: a `gt doctor` / merge predicate that warns when an MR's last
  dogfood build SHA is older than the most recent go.mod dependency-major change on its
  merge target. This is the C-8 "the predicate asserts it" upgrade of a human-read warning.
  → AgentTools / gt-doctor candidate.

## IGNORED TOOLS

None I'm aware of — the gaps were MISSING tools (remote-build, the merge-staleness gate),
not tools I had and skipped. I did NOT reach for `go mod tidy` locally despite the reflex,
which was correct (it would have drifted from the verified bytes) — that's a tool
*correctly* not used.

## EARLY-INFO (how a tool could have surfaced key info sooner)

- The node-build recipe (HOME + staged-go PATH + `CGO_ENABLED=0 -tags gms_pure_go`) should
  be discoverable from the repo, not from personal memory. A `make build` run on a
  restricted-net host could *detect* the no-egress condition (proxy.golang.org unreachable)
  and print the canonical node-build recipe + node ID instead of timing out cryptically.
  That turns a multi-minute dead-end into an immediate "build here instead" pointer.
- The CGo-dep regression risk should surface AT THE BUMP: a tool that, on any go.mod change,
  diffs `go list -deps` for newly-CGo packages would have flagged go-icu-regex the moment
  the v52 bump landed — instead of it surfacing as a stock-host build failure later.

## RIGOR / COMPREHENSIVENESS / USEFULNESS

- **More rigorous:** replace "node build green" evidence with "node build green + CGo-dep
  absence asserted + committed dep-file sha256 == verified-artifact sha256." Each is a
  one-liner; together they make the bd-v52 claim falsifiable end-to-end.
- **More comprehensive:** the remote-build tool should run the SAME suite the dogfood gate
  needs (build + doltserver/mail/convoy + the L1 shell tests), not an ad-hoc subset, so one
  command produces the full C-8 evidence rather than me choosing what to run each time.

## NEW TOOLING / SKILLS (AgentTools candidates)

1. **`gt remote-build` / forge-remote-target** — "build+test this ref on a network-capable
   node, stream + persist the log." Kills the manual SSM dance for the whole gt-source team.
2. **`gt doctor` dogfood-staleness check** — warn when an MR's dogfooded binary predates a
   dep-major change on its target (my pre-flight catch, as a check not a mail).
3. **`make verify-pure-go`** — positive assertion the ICU/CGo dep is out of the build graph.

## TOOLS FOR CORRECTNESS (procedural + operational; steer-back; augment context)

- **Procedural:** the restricted-net detector (above) STEERS an agent back into the correct
  pattern (build on the node) the instant they try the wrong one (build locally) — instead
  of letting them burn minutes on a timeout. That's a tool dynamically augmenting context
  with the right next action at the moment of the mistake.
- **Operational:** the dogfood-staleness gate makes the C-8 "verify the running binary, not
  the merged code" rule MECHANICAL — the binary-identity trap that bit the F2 flow (and
  that I caught by hand) becomes something the tool refuses to let you close through.
