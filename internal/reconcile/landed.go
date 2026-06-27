// Package reconcile implements the policy layer of `gt town sync` (phase1-reconcile-policy-spec):
// after the transport+structural-merge of a rig DB against the cluster hub, a row-level pass
// corrects what a blind 3-way merge gets wrong — most critically the LANDED status of merge-request
// beads, which a bead's own State lies about (phantom-close / ff-race / auto-close-on-probe).
//
// The landed-check here is the §3 NON-NEGOTIABLE CORE: landed-ness is computed from CONTENT on the
// authoritative master, NEVER from bead State and NEVER from SHA-ancestry (a SHA is rewritten under
// rebase AND combined under squash — the default engine path squashes). The squash-resilient method
// is `git cherry` over the SOURCE branch: it compares by PATCH-ID of each source commit against
// upstream, so a squash-merged MR (whose landed commit has a different SHA) is still detected as
// landed because its source commits' patch-ids are present upstream. This reuses gt's own
// restart-safety logic (witness `_verifyBranchAlreadyMerged` uses the identical cherry approach);
// we compose the public `git.Git.Cherry` + `CountCherryUnmergedCommits` rather than reach into
// witness's unexported function.
package reconcile

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/steveyegge/gastown/internal/git"
)

// LandedVerdict is the content-truth landed status of a merge-request bead's source branch.
type LandedVerdict struct {
	Landed bool   // the source branch's work is present on the authoritative upstream
	Method string // "ancestor" (ff/regular merge) or "cherry" (squash-resilient patch-id) — for the report
}

// IsBranchLanded reports whether sourceBranch's work is present on upstream (e.g. "origin/master"),
// squash-resiliently. It re-fetches first (rule 3: never trust a cached mirror's view of upstream),
// then: (1) FAST PATH — if upstream is an ancestor of the source branch's tip the work landed via a
// ff/regular merge; (2) SQUASH PATH — `git cherry upstream sourceBranch` patch-ids each source commit
// against upstream; if NO commit is still unmerged (no `+` lines) the work is fully landed even though
// a squash rewrote the landed SHA.
//
// remote is the upstream remote name (e.g. "origin"); upstreamRef is the ref to test against
// (e.g. "origin/master"). A non-nil error means the check itself failed (couldn't fetch / run git) —
// the caller must treat that as UNVERIFIABLE and NOT set a landed status from it (fail-closed: an
// unverifiable landed-check must never flip a bead to closed).
func IsBranchLanded(g *git.Git, workDir, remote, upstreamRef, sourceBranch string) (LandedVerdict, error) {
	// Rule 3: re-fetch so the upstream ref reflects origin NOW, not a stale local mirror.
	if err := g.Fetch(remote); err != nil {
		return LandedVerdict{}, fmt.Errorf("fetch %s before landed-check: %w", remote, err)
	}

	// (1) Fast path: upstream is an ancestor of the source branch tip => the branch's commits are all
	// on upstream via a ff/regular (non-squash) merge. IsAncestor(upstreamRef, sourceBranch) asks
	// "is upstreamRef reachable from sourceBranch" — true when sourceBranch is at-or-ahead of upstream
	// AND contains it, i.e. nothing diverged. For landed-detection we instead ask the cherry below;
	// the ancestor fast-path here is the cheap "regular-merge" catch matching witness's verifyCommitOnMain.
	if anc, err := g.IsAncestor(sourceBranch, upstreamRef); err == nil && anc {
		// sourceBranch is an ancestor of upstream => every source commit is on upstream (regular merge).
		return LandedVerdict{Landed: true, Method: "ancestor"}, nil
	}

	// (2) Cherry path: patch-ids each SOURCE commit against upstream. No unmerged commit => landed.
	// Catches single-commit squashes + patch-equivalent commits. (For a MULTI-commit squash, cherry
	// patch-ids the individual source commits, which the COMBINED squash commit doesn't match — that's
	// the known false-negative, handled by Layer 3 below.)
	out, err := g.Cherry(upstreamRef, sourceBranch)
	if err != nil {
		return LandedVerdict{}, fmt.Errorf("git cherry %s %s: %w", upstreamRef, sourceBranch, err)
	}
	if git.CountCherryUnmergedCommits(out) == 0 {
		return LandedVerdict{Landed: true, Method: "cherry"}, nil
	}

	// (3) Combined-diff patch-id (§6, distcompute-verified): a MULTI-commit MR squash-merged into ONE
	// commit produces EXACTLY the combined diff (merge-base..source), so the squashed commit's own
	// patch-id == the combined-diff patch-id. Scan upstream commits since the merge-base for a match.
	// This is the squash-of-multiple-commits case cherry (Layer 2) misses. Exact, not heuristic.
	landed, l3err := landedByCombinedPatchID(workDir, upstreamRef, sourceBranch)
	if l3err != nil {
		// Layer-3 couldn't run — fall back to the cherry verdict (not-landed), which fails SAFE
		// (re-queue, never phantom-close). Surface the method so the rebase-tax case is visible.
		return LandedVerdict{Landed: false, Method: "cherry-no-l3"}, nil
	}
	return LandedVerdict{Landed: landed, Method: "combined-patchid"}, nil
}

// gitOut runs a git command in workDir and returns trimmed stdout (Layer-3 local exec — the patch-id
// pipe + upstream scan are reconcile-specific; kept here rather than widening internal/git's API).
func gitOut(workDir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...) //nolint:gosec // fixed git subcommands, reconcile-internal
	cmd.Dir = workDir
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// patchIDOf returns the stable patch-id of a diff supplied on stdin (`git patch-id --stable`),
// which emits "<patch-id> <commit-or-zero>"; we take the first field.
func patchIDOf(workDir, diff string) (string, error) {
	cmd := exec.Command("git", "patch-id", "--stable") //nolint:gosec // fixed
	cmd.Dir = workDir
	cmd.Stdin = strings.NewReader(diff)
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return "", fmt.Errorf("empty patch-id (no diff?)")
	}
	return fields[0], nil
}

// landedByCombinedPatchID reports whether branch's whole change (merge-base..branch) is present on
// upstream as a single squashed commit, by patch-id equivalence. Returns false (not an error) when
// the branch has no diff vs the merge-base (nothing to land).
func landedByCombinedPatchID(workDir, upstreamRef, branch string) (bool, error) {
	base, err := gitOut(workDir, "merge-base", upstreamRef, branch)
	if err != nil || base == "" {
		return false, fmt.Errorf("merge-base %s %s: %w", upstreamRef, branch, err)
	}
	combinedDiff, err := gitOut(workDir, "diff", base+".."+branch)
	if err != nil {
		return false, fmt.Errorf("diff %s..%s: %w", base, branch, err)
	}
	if strings.TrimSpace(combinedDiff) == "" {
		return false, nil // branch introduces no change vs base — nothing to consider "landed"
	}
	want, err := patchIDOf(workDir, combinedDiff)
	if err != nil {
		return false, fmt.Errorf("combined patch-id: %w", err)
	}
	// Scan upstream commits since the merge-base; a squashed landing's commit patch-id == want.
	shas, err := gitOut(workDir, "rev-list", base+".."+upstreamRef)
	if err != nil {
		return false, fmt.Errorf("rev-list %s..%s: %w", base, upstreamRef, err)
	}
	for _, sha := range strings.Fields(shas) {
		// `git diff <sha>^..<sha>` is that commit's own diff; its stable patch-id vs want.
		commitDiff, derr := gitOut(workDir, "diff", sha+"^.."+sha)
		if derr != nil {
			continue // first commit (no parent) etc. — skip, not fatal
		}
		got, perr := patchIDOf(workDir, commitDiff)
		if perr == nil && got == want {
			return true, nil
		}
	}
	return false, nil
}
