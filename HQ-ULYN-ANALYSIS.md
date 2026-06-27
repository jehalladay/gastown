# hq-ulyn analysis (gastown_eng_a) — ONE root with hq-9y0x: rig-DB prefix-vs-data mismatch

**Bug (hq-ulyn):** `gt hook reactivecli-XXXX` fails "bead not found"; bd resolves from
workspace root, rig beads invisible.

## Resolution chain (traced, main @ b3230519)
`gt hook <bead>` → `verifyBeadExists` (hook.go:245) → `bdShowBeadOutput`
(sling_helpers.go:375) → `resolveBeadDir` (sling_helpers.go:45) →
`ResolveBeadsDirForID(townBeadsDir, beadID)` (routes.go:479) → `ExtractPrefix`
matched against the town routes table. No matching route → returns townBeadsDir →
`filepath.Dir` = townRoot. **That fallback IS the "runs from workspace root" symptom.**

## ROOT CAUSE (decisive evidence, live town) — corrected after empirical test
The rig's beads DB has a **prefix-config-vs-DATA mismatch**:
- `reactivecli/.beads/config.yaml`: `prefix: rc` / `issue-prefix: rc`
- `rigs.json`: reactivecli.beads.prefix = `rc`
- town `routes.jsonl`: `{"prefix":"rc-","path":"reactivecli"}` — only `rc-`, NO `reactivecli-`
- **BUT all 233 beads in the rig DB are minted `reactivecli-*`** (verified: `bd list` in
  the rig dir → 233 `reactivecli-` IDs, ZERO `rc-` IDs).

So config/routes say `rc`, data says `reactivecli`. Consequence:
- `gt hook reactivecli-4tla` → prefix `reactivecli-` → no route → townRoot fallback → NOT FOUND (hq-ulyn).
- `gt hook rc-4tla` → routes to reactivecli rig, but no `rc-4tla` exists (no rc- beads) → NOT FOUND.

## hq-ulyn and hq-9y0x are ONE root, two faces
- hq-9y0x (eng_sr2): `mq submit` MINTS the MR bead `reactivecli-*` not `rc-*`.
- hq-ulyn (me): `gt hook` can't RESOLVE `reactivecli-*` because routing knows only `rc-`.
Both are the same underlying fact: **the rig DB is populated with the rig-NAME prefix
(`reactivecli`) while all config/routes use the configured prefix (`rc`).** The hook code
is CORRECT for a correctly-prefixed bead; it only fails because the beads carry the wrong prefix.

## This is why eng_sr2 said "both your hypotheses are wrong"
My earlier guesses (GetPrefixForRig returns the name / bd-create re-derives via detectPrefix)
were mint-CODE guesses. The actual issue is the rig DB was bd-init'd or migrated such that
its bead IDs use `reactivecli`, diverging from its own config.yaml `rc`. The mint code may be
fine; the DB's actual ID prefix is the divergence.

## FIX OPTIONS (shared — must be decided WITH eng_sr2, not unilaterally)
1. **Routes alias**: add `{"prefix":"reactivecli-","path":"reactivecli"}` to town routes so
   existing 233 beads resolve. Cheapest; makes hook/sling/handoff work immediately. Doesn't
   fix new-mint divergence (eng_sr2's half) but unblocks resolution. ponytail-favored as the
   resolution-side fix IF we keep the reactivecli- prefix.
2. **Data migration**: re-prefix the 233 `reactivecli-*` beads to `rc-*` to match config.
   Correct but heavy + touches live rig DB (prod Dolt :3307 — caution, needs isolated env).
3. **Fix mint + reconcile**: eng_sr2 fixes mint to emit `rc-`, plus a migration/alias for the
   existing 233. Most complete.

## RECOMMENDATION
hq-ulyn is NOT a standalone hook-cwd code bug — the hook resolver is correct. The fix lives
at the prefix-reconciliation layer, SHARED with hq-9y0x. eng_sr2 owns the mint half; the
resolution half (routes alias, option 1) is a 1-line routes.jsonl add I can make IF the town
decides to keep `reactivecli-` as the prefix — but that's a town-data decision (rc vs
reactivecli), not mine to make unilaterally. Need eng_sr2 + likely mayor on the prefix
decision before any change. Do NOT migrate live prod-Dolt beads without an isolated env + explicit go.
