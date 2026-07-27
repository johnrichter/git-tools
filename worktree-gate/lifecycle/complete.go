package lifecycle

import (
	"context"
	"fmt"
	"strings"

	"github.com/johnrichter/claude-shared-tooling/go/git"
)

// CompleteOptions configures Complete.
type CompleteOptions struct {
	// BaseRef is the branch merge-back lands on. Empty means whatever
	// branch is currently checked out in repoRoot; a non-empty value must
	// match that branch — Complete never checks out a different branch on
	// the caller's behalf.
	BaseRef string
	// Force merges and removes the worktree even if it still has
	// uncommitted changes, discarding them. Default (false) refuses.
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
// A dirty worktree is refused unless Force is set — Complete never
// discards uncommitted work silently. A merge conflict aborts the merge
// and returns *git.ConflictError with the worktree left untouched, so the
// operator can resolve it by hand and retry.
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

		if !opts.Force {
			dirty, err := isDirty(ctx, path)
			if err != nil {
				return fmt.Errorf("lifecycle: check %s for uncommitted changes: %w", path, err)
			}
			if dirty {
				return fmt.Errorf("lifecycle: worktree %s has uncommitted changes; refusing to complete (set Force to discard)", path)
			}
		}

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

		mergeRes, err := repo.Merge(ctx, []string{branch}, git.MergeOptions{
			Message: fmt.Sprintf("Merge worktree %s (branch %s)", id, branch),
		})
		if err != nil {
			return fmt.Errorf("lifecycle: merge %s into %s: %w", branch, baseRef, err)
		}
		result.Merged = mergeRes.NewHead != preHead
		result.NewHead = mergeRes.NewHead

		if err := repo.WorktreeRemove(ctx, path, git.WorktreeRemoveOptions{Force: opts.Force}); err != nil {
			return fmt.Errorf("lifecycle: remove worktree %s: %w", path, err)
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
