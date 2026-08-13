package lifecycle

import (
	"context"
	"fmt"
	"strings"

	"github.com/johnrichter/claude-shared-tooling/go/git"
	"github.com/johnrichter/git-tools/internal/signing"
	"github.com/johnrichter/git-tools/internal/worktreeclean"
)

// CompleteOptions configures Complete.
type CompleteOptions struct {
	// BaseRef is the branch merge-back lands on. Empty means whatever
	// branch is currently checked out in repoRoot; a non-empty value must
	// match that branch — Complete never checks out a different branch on
	// the caller's behalf.
	BaseRef string
	// Force is retained for API compatibility but does nothing: removal
	// runs through internal/worktreeclean's shared rule set (SC-C4), the
	// same one the standalone `worktree remove` verb uses, and that rule
	// set has no override for a dirty tree. Setting Force no longer
	// discards uncommitted work.
	Force bool
	// KeepBranch leaves the worktree's branch in place after a successful
	// merge-back instead of deleting it.
	KeepBranch bool
}

// CompleteResult reports what Complete did.
type CompleteResult struct {
	// Branch is the worktree's branch that was merged.
	Branch string
	// Merged is false when the branch had no commits ahead of BaseRef —
	// the merge was a no-op, not skipped.
	Merged bool
	// NewHead is BaseRef's head commit after the merge.
	NewHead string
	// BranchDeleted is true only when KeepBranch was false and the branch
	// was successfully deleted after merging.
	BranchDeleted bool
}

// Complete merges id's worktree branch back into BaseRef, then removes the
// worktree and, by default, its branch. It is Ensure's counterpart: called
// once a task is done, it folds the isolated work back into history and
// frees the worktree slot.
//
// Complete is NOT a sanctioned landing channel — internal/cli's merge verb
// is. It still runs the same signing gate before its merge, because the
// worktree-isolation gate this package supports lives inside the repository
// it protects: any write path in this repository that lands a commit is
// bound by the repository's own no-unsigned-commits rule, whether or not
// that path is the one operators are told to use. Before repo.Merge runs,
// Complete calls internal/signing.Gate on the worktree's branch exactly as
// the merge verb does, and refuses in the same shape: a *signing.Refusal
// returned as a plain error, with the base ref and worktree both untouched.
// If the merge will mint a commit of its own, Complete proves the
// repository can sign it first and passes that request to repo.Merge, so
// the minted commit is signed (SC-B1) rather than landing bare.
//
// Removal goes through internal/worktreeclean's shared rule set (SC-C4), the
// same one the standalone `worktree remove` verb calls: a dirty worktree is
// refused unconditionally, naming every offending path and the same three
// remedies (commit it, ignore it, or delete it deliberately), and no option
// here waives that. A merge conflict aborts the merge and returns
// *git.ConflictError with the worktree left untouched, so the operator can
// resolve it by hand and retry.
//
// If the branch delete step fails after a successful merge and worktree
// removal, Complete still returns that error, but the work itself is
// already safe: it is merged into BaseRef and the worktree is gone: only
// the now-redundant branch ref remains, safe to delete by hand.
func Complete(ctx context.Context, repoRoot, id string, opts CompleteOptions) (CompleteResult, error) {
	if err := validateID(id); err != nil {
		return CompleteResult{}, err
	}

	repo, err := git.Open(ctx, repoRoot)
	if err != nil {
		return CompleteResult{}, fmt.Errorf("lifecycle: open repo %s: %w", repoRoot, err)
	}

	worktreesDir := WorktreesDir(repoRoot)
	path := WorktreePath(repoRoot, id)

	var result CompleteResult
	err = withRepoLock(ctx, worktreesDir, func() error {
		list, err := repo.WorktreeList(ctx)
		if err != nil {
			return fmt.Errorf("lifecycle: list worktrees: %w", err)
		}
		entry, found := findWorktree(list, path)
		if !found {
			return fmt.Errorf("lifecycle: no worktree registered for %q at %s", id, path)
		}
		branch := strings.TrimPrefix(entry.Branch, "refs/heads/")
		if branch == "" {
			return fmt.Errorf("lifecycle: worktree %s is on a detached HEAD, not a branch; cannot merge back", path)
		}
		result.Branch = branch

		current, err := currentBranchName(ctx, repoRoot)
		if err != nil {
			return fmt.Errorf("lifecycle: resolve current branch of %s: %w", repoRoot, err)
		}
		baseRef := opts.BaseRef
		if baseRef == "" {
			baseRef = current
		} else if baseRef != current {
			return fmt.Errorf("lifecycle: %s has %q checked out, not requested base %q; check out %q first", repoRoot, current, baseRef, baseRef)
		}

		preHead, err := currentHead(ctx, repoRoot)
		if err != nil {
			return fmt.Errorf("lifecycle: resolve HEAD of %s: %w", repoRoot, err)
		}

		// The gate runs before the merge, exactly as it does for the merge
		// verb: a refusal here leaves baseRef and the worktree exactly where
		// they were, never half-landing a source it could not sign.
		prober := signing.NewProber(repo)
		if _, refusal := signing.Gate(ctx, repo, baseRef, []string{branch}, false, prober); refusal != nil {
			return refusal
		}

		// A merge that will mint a commit of its own must sign it too (SC-B1).
		// Settle that up front, the same way the merge verb does, so a keyless
		// repository refuses here rather than failing mid-merge.
		willMint, err := signing.WillMintCommit(ctx, repo, baseRef, []string{branch}, git.FastForwardAllow)
		if err != nil {
			return fmt.Errorf("lifecycle: determine whether merging %s will mint a commit: %w", branch, err)
		}
		sign := willMint
		if sign {
			available, detail, err := prober.Available(ctx)
			if err != nil {
				return fmt.Errorf("lifecycle: test whether %s can sign the merge commit: %w", repoRoot, err)
			}
			if !available {
				return fmt.Errorf("no key resolved for commit signing, so the merge commit for %s would be unsigned: %s", branch, detail)
			}
		}

		mergeRes, err := repo.Merge(ctx, []string{branch}, git.MergeOptions{
			Message: fmt.Sprintf("Merge worktree %s (branch %s)", id, branch),
			Sign:    sign,
		})
		if err != nil {
			return fmt.Errorf("lifecycle: merge %s into %s: %w", branch, baseRef, err)
		}
		result.Merged = mergeRes.NewHead != preHead
		result.NewHead = mergeRes.NewHead

		// Cleanup is the one choke point both the standalone `worktree
		// remove` verb and this merge-back call go through: it re-checks the
		// branch is fully landed, refuses a dirty tree unconditionally, and
		// only then removes the worktree. That check runs after the merge
		// above, never before it, so a refusal here never leaves the merge
		// half-done — the merge has already landed by the time this can
		// refuse anything.
		cleaned, err := worktreeclean.Cleanup(ctx, repo, path, worktreeclean.Options{MergedBranches: []string{branch}})
		if err != nil {
			return fmt.Errorf("lifecycle: remove worktree %s: %w", path, err)
		}
		if cleaned.Refusal != "" {
			return fmt.Errorf("lifecycle: worktree %s: %s", path, cleaned.Refusal)
		}
		if err := removeActivity(worktreesDir, id); err != nil {
			return fmt.Errorf("lifecycle: remove activity marker for %s: %w", id, err)
		}

		if opts.KeepBranch {
			return nil
		}
		branchHead, err := resolveRefSHA(ctx, repoRoot, "refs/heads/"+branch)
		if err != nil {
			return fmt.Errorf("lifecycle: resolve %s after merge: %w", branch, err)
		}
		if _, err := repo.DeleteBranch(ctx, branch, branchHead, false); err != nil {
			return fmt.Errorf("lifecycle: delete merged branch %s: %w", branch, err)
		}
		result.BranchDeleted = true
		return nil
	})
	if err != nil {
		return CompleteResult{}, err
	}
	return result, nil
}
