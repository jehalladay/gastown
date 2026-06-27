# Part B — Concept Papers: gastown_eng_a

One concept I meaningfully refined this session.

---

## Concept — Pure-Go-default build (drop the CGo/ICU dep, make portability the default)

**MOTIVATION.** The v52 beads bump pulls dolt/go-mysql-server in transitively, which drags
in `go-icu-regex` — a CGo dependency needing `unicode/regex.h` (ICU4C). On a stock host
(restricted-net mac, vanilla cluster node) that header isn't present, so the build fails or
forces an ICU4C install + keg-only header path dance. The build broke not because of *our*
code but because a transitive dep flipped CGo on. The town binary must build on the hosts
the town actually runs (no-ICU nodes, cross-compiled linux/amd64) — so the *default* build
must not need a C toolchain or ICU. Evidence: commit `23c945e5`; the prior Makefile had a
macOS-only ICU4C-detection block as the *only* path.

**PRIOR/SIMILAR.** This is the standard Go `CGO_ENABLED=0` + build-tag pattern (e.g.
`sqlite` pure-Go vs cgo drivers, `netgo`). go-mysql-server ships exactly for this: a
`gms_pure_go` build tag that selects its pure-Go regex engine instead of the ICU one. So
the concept isn't invented — the refinement is **making pure-Go the DEFAULT and CGo the
opt-in**, rather than the usual "CGo default, pure-Go for special cases." For a binary
whose defining requirement is "builds + cross-compiles anywhere the town runs," the
portable path is the common case, so it should be the default. (ponytail: use the platform
feature — a build tag the dep already provides — not a custom regex shim.)

**DESIGN.** `Makefile:30-31` sets `export CGO_ENABLED ?= 0` and `GO_TAGS ?= gms_pure_go`
(`?=` so both are overridable from the environment/CLI). All build targets thread
`-tags "$(GO_TAGS)"` (`Makefile:44-46` for the 3 binaries; `desktop-build`; and the `test`
target so tests exercise the same regex path they ship with). The macOS ICU4C-detection
block is kept but demoted to a comment-documented opt-in: `make build CGO_ENABLED=1
GO_TAGS=` restores the CGo/ICU path, and the ICU flags are "harmless when CGO is disabled"
(`Makefile:33-42`). Net: the default `make build` needs no C toolchain, no ICU, and
cross-compiles to linux/amd64; the CGo regex path is one flag away for anyone who proves
they need ICU collation.

**FUTURE WORK.** (a) **Delete the CGo branch entirely** — promising because every build
*mode* is a works-on-my-machine surface; if no runtime path actually needs ICU's
locale-aware regex/collation, one build path is strictly fewer failures. Gated on
*proving* nothing needs ICU (a grep for ICU-dependent collation + a functional check), so
deferred, not assumed. (b) **Add a CI guard that the default build stays CGO_ENABLED=0** —
a future transitive bump could silently re-introduce a CGo dep; a `go list`/`go build`
check in CI catches the regression at the bump, not at the next stock-host build.

**TERMINAL FAILURE MODES.** The line of work that *ended* was "build the v52 graph on my
mac" — it can't be done (no proxy.golang.org egress), so I could not directly observe my
own Makefile change going green; I had to verify it on a node (mail hq-wisp-vc5ort).
IMPROVED INSTRUCTIONS that would have avoided the dead-end: an up-front crew rule "the fork
does not build on restricted-net hosts; verify on node i-0e3396d7b36285c8e with $RECIPE" —
then I'd never have framed local-build as an option. (This is the Part D friction too.)

**RIGOR.** The change is currently asserted by "a node `make build` went green + tests
pass" (mail hq-wisp-vc5ort) — necessary but it doesn't *prove* the CGo dep is gone, only
that the default build works. A rigorous version asserts the absence directly:
`CGO_ENABLED=0 go build` will *fail* if any package in the graph needs cgo, so a CI line
that runs the default build with cgo hard-off (already the default) AND a positive
assertion (`go list -deps ./cmd/gt | grep -qv go-icu-regex`) would turn "builds green" into
"the ICU dep is provably not in the graph." That's the test Part C should own.

**LATE-FEEDBACK COST.** The thing that arrived late was offload_ops's confirmation that the
*exact* tidied go.mod/go.sum were already node-verified in S3 (mail hq-wisp-as4w5). Had I
known up-front, I'd have sequenced "Makefile bake → pull exact verified files → one node
C-8" as a single clean pass; instead the Makefile half (`23c945e5`) landed first with a
NOTE that it still needed the tidied files, and the tidy (`c0c33a6b`) followed once the S3
files were confirmed. No harm done (the two-commit split is honest about the dependency),
but a single up-front "verified files live in S3, pull them" would have made it one
atomic, obviously-correct change instead of two.
