# Part B — Concept Papers: gastown_eng_lead

Two concepts I meaningfully refined this session.

---

## Concept 1 — Embedded-script remote-spawn (gt drives proven bash, host-independent)

**MOTIVATION.** F2 needs gt to spawn a crew agent on a cluster node (tunnel + provision
+ launch). The mechanics (SSM, presign, ssh -R, systemd/tmux) already existed as
offload_ops'/eng_sr2's battle-proven bash. Reimplementing them in Go would be slow,
risky, and diverge from the source of truth that's actively maintained.

**PRIOR/SIMILAR.** F4's `gt offload` did the same: `go:embed` the offload script suite +
shell out. The Agent tool's `isolation: worktree` is a sibling idea (reuse a proven
mechanism rather than rebuild). Difference: here the embedded scripts have STATEFUL
dependencies (a persistent SSH key, warm-pool state) that naive embed+extract would lose.

**DESIGN.** gt `go:embed`s the suite into `internal/offload/scripts/`, extracts to a
tempdir at runtime, and DRIVES them (computes the env/identity/command in Go, shells to
the scripts). The stateful-key problem was solved by offload_ops moving key+state to a
stable host path (OFFLOAD_STATE_DIR) that the extracted scripts read via env
(TUNNEL_SSH_KEY) — so the binary stays portable while the secret stays external. gt owns:
flag parsing, env overlay (tunnel Dolt endpoint wins over local default), node-safe
command synthesis (bare `claude` via node PATH, no host abs path), fail-loud guards
(GT_TUNNEL_KEY required; v52-bd-commit required before spawn).

**FUTURE WORK.** (a) Generalize "embed + extract + drive, secret external" as a reusable
pattern — promising because multiple gt features (offload, remote-spawn, future cluster
ops) share it. (b) A re-vendor check (the embedded script's pinned rev vs the source
crew dir) as a doctor check — promising because vendored drift is silent today.

**TERMINAL FAILURE MODES (+ instruction fixes).** The pattern nearly died on the
stateful-key issue (embed+extract loses the key → tunnel auth fails). I caught it by
READING the scripts' key-resolution before vendoring, not by assuming embed worked like
F4's. INSTRUCTION FIX: "Before embedding a script suite, audit its filesystem/state
dependencies ($HERE-relative files, keys, state) — embed-to-tempdir breaks $HERE-relative
state; route it through a stable external path."

**RIGOR.** Make it more rigorous with the L8 agent-journey test as the gate (it is now)
+ a contract test that the embedded scripts' interface matches what gt drives (today the
coupling is by convention).

**LATE-FEEDBACK COST.** The "persistent REPL not one-shot" requirement (claude needs an
interactive tmux session, not a prompt-arg invocation) arrived only via the live dogfood
(bug #5). Had the "mirror the LOCAL crew model exactly (interactive claude in tmux +
beacon via send-keys)" guidance been up-front, I'd have built the tmux launch first and
saved 3 dogfood rounds.

---

## Concept 2 — Injectable resource-pressure guard (detect → guide → shed)

**MOTIVATION.** Jetsam (macOS OOM killer) twice SIGKILLed the control plane / Dolt because
nothing made memory pressure VISIBLE before the kill. Disk had a guard; memory didn't.

**PRIOR/SIMILAR.** gt's existing DiskSpaceCheck (util.GetDiskSpace + CheckDiskSpace +
doctor check). F10 mirrors it on the memory axis. The novel bit vs disk: memory pressure
can't be safely INDUCED in a test (you can't fill RAM in CI without risking the box).

**DESIGN.** util.CheckMemoryPressure (WARN 85% swap, CRITICAL 95% or <512MB reclaimable),
per-OS readers (darwin sysctl vm.swapusage + vm_stat; linux /proc/meminfo), AND a
test-injection override (GT_MEMPRESSURE_TEST_SWAP_PCT) so the threshold logic is
deterministically testable without real exhaustion. The doctor check escalates: WARN =
reduce load; CRITICAL = shed/park idle crew before the kernel picks the victim.

**FUTURE WORK.** The auto-SHED action (gt crew stop idle crew at 95%) is the high-value
phase-2 — promising because proactively parking the idle agent beats jetsam picking Dolt.
It needs idle-detection (which crew are safe to park) + a safe-park (graceful, resumable),
each its own test-first cycle. Also: watch the .dolt-data volume specifically (today it's
general free-mem + disk separately).

**TERMINAL FAILURE MODES (+ instruction fixes).** The injection-override pattern was the
unlock — without it, an honest test of "the guard fires at 92% swap" is impossible
(can't induce it). INSTRUCTION FIX (now in Part C heuristics): "For un-inducible failure
modes, the reader takes a test-injection env override so WARN/CRITICAL logic is
deterministically testable."

**RIGOR.** More rigorous: validate the live readers against a known state (e.g. a unit
test that parses a fixed vm_stat/meminfo fixture → exact MB), and a calibration knob (the
thresholds are heuristic; the hardware's real jetsam point drifts — leave the threshold
tunable, don't hard-code as if the model is exact).

**LATE-FEEDBACK COST.** The owner emphasized the SHED action is the valuable part. I shipped
detection+guidance first (the right increment) but had I known up-front that shed-vs-detect
was the priority split, I'd have scoped the idle-detection design alongside detection so
phase-2 starts from a fuller picture.
