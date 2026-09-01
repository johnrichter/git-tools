package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/johnrichter/claude-shared-tooling/go/git"
)

// prospectiveMergeScanDir prepares the tree merging sources into target
// would actually produce, not target's current, pre-merge tree, so the
// caller's own scanGate call can scan that result instead. This is what
// makes it possible for a merge to land a remediation into a target whose
// current tree already carries pre-existing findings: today's naive "scan
// whatever main currently has checked out" approach can never distinguish
// that remediating merge from one that makes things worse, because it never
// looks at either merge's actual result.
//
// It works by performing a real trial merge of sources into a disposable,
// detached scratch worktree checked out at target's current tip (oldHead) --
// a linked worktree shares this repository's own object and ref database, so
// every named source resolves exactly as it would for the real merge.
//
// A trial merge that does not succeed cleanly -- a real conflict, a source
// that is not a mergeable branch, a commit-msg hook rejecting the trial's own
// unspecified message, or a signing side effect this repository's config
// forces on every commit -- leaves nothing to scan, so dir returns "": the
// signing gate and the real merge that run afterward already carry the
// established, more precise handling for every one of those cases, and this
// gate adds a new refusal only when it can compute a clean prospective tree
// and finds a guardrail violation in it.
//
// No -S and no -m: the trial commit, if one is minted, is never kept, signed,
// or shown to the caller -- only its resulting tree content is scanned.
//
// cleanup removes the scratch worktree and must be called exactly once by
// the caller (typically deferred), regardless of dir or err -- it is always
// non-nil, and safe to call even when this function itself failed early.
func prospectiveMergeScanDir(cmd *cobra.Command, repo *git.Repo, oldHead string, sources []string, ff git.FastForward, data map[string]any) (dir string, cleanup func(), err error) {
	noop := func() {}

	scratchDir, mkErr := os.MkdirTemp("", "git-tools-merge-scan-")
	if mkErr != nil {
		return "", noop, finishErr(cmd, data, "internal.git.merge_scan_worktree_failed", "prepare a scratch worktree to test the merge result", mkErr)
	}
	os.RemoveAll(scratchDir)

	cleanup = func() {
		if rmErr := repo.WorktreeRemove(cmd.Context(), scratchDir, git.WorktreeRemoveOptions{}); rmErr != nil {
			fmt.Fprintf(os.Stderr, "warning: could not remove scratch merge-scan worktree %s: %v\n", scratchDir, rmErr)
		}
		os.RemoveAll(scratchDir)
	}

	if err := repo.WorktreeAdd(cmd.Context(), scratchDir, oldHead, git.WorktreeAddOptions{}); err != nil {
		return "", noop, finishErr(cmd, data, "internal.git.merge_scan_worktree_failed", "prepare a scratch worktree to test the merge result", err)
	}

	scratch, err := git.Open(cmd.Context(), scratchDir)
	if err != nil {
		return "", cleanup, finishErr(cmd, data, "internal.git.merge_scan_worktree_failed", "prepare a scratch worktree to test the merge result", err)
	}

	if _, mergeErr := scratch.Merge(cmd.Context(), sources, git.MergeOptions{FastForward: ff}); mergeErr != nil {
		// The trial merge itself did not produce a tree -- whatever went
		// wrong (conflict, invalid source, hook rejection, forced signing
		// with no key) is exactly what the signing gate and the real merge
		// below are already responsible for diagnosing precisely. Returning
		// "" defers to them unchanged rather than reporting a second,
		// less-informed verdict on the same failure.
		return "", cleanup, nil
	}

	return scratchDir, cleanup, nil
}
