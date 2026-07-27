package lifecycle

import (
	"context"
	"fmt"
	"os"

	"github.com/johnrichter/claude-shared-tooling/go/git"
)

// EnsureResult reports what Ensure did for one call.
type EnsureResult struct {
	// Path is the worktree's absolute path, whether or not this call
	// created it.
	Path string
	// Created is true only when this call created the worktree; false
	// means one already existed at Path.
	Created bool
	// Branch is the local branch checked out in the worktree.
	Branch string
}

// Ensure returns the linked worktree for id under repoRoot's worktrees
// directory (WorktreePath), creating it off repoRoot's current HEAD on a
// new branch named id when none exists yet. It is meant to run before every
// gated write: an existing worktree is returned as-is, with its activity
// marker refreshed so Reap never mistakes an in-use worktree for an
// abandoned one; a missing one is created, serialized against any
// concurrent Ensure/Complete/Reap on the same repository.
//
// A crash between creating id's branch and completing `git worktree add`
// can leave the branch behind with no worktree at Path. Ensure recovers
// from exactly that case by re-running worktree-add against the existing
// branch instead of trying to recreate it.
func Ensure(ctx context.Context, repoRoot, id string) (EnsureResult, error) {
	if err := validateID(id); err != nil {
		return EnsureResult{}, err
	}

	repo, err := git.Open(ctx, repoRoot)
	if err != nil {
		return EnsureResult{}, fmt.Errorf("lifecycle: open repo %s: %w", repoRoot, err)
	}

	worktreesDir := WorktreesDir(repoRoot)
	path := WorktreePath(repoRoot, id)

	var result EnsureResult
	err = withRepoLock(ctx, worktreesDir, func() error {
		if info, statErr := os.Stat(path); statErr == nil {
			if !info.IsDir() {
				return fmt.Errorf("lifecycle: %s exists and is not a directory", path)
			}
			result = EnsureResult{Path: path, Created: false, Branch: id}
			return touchActivity(worktreesDir, id)
		} else if !os.IsNotExist(statErr) {
			return fmt.Errorf("lifecycle: stat %s: %w", path, statErr)
		}

		head, err := currentHead(ctx, repoRoot)
		if err != nil {
			return fmt.Errorf("lifecycle: resolve HEAD of %s: %w", repoRoot, err)
		}

		addErr := repo.WorktreeAdd(ctx, path, head, git.WorktreeAddOptions{NewBranch: id})
		if addErr != nil {
			if !branchExists(ctx, repoRoot, id) {
				return fmt.Errorf("lifecycle: create worktree %s off %s: %w", path, head, addErr)
			}
			// A prior attempt created the branch but not the worktree
			// (e.g. a crash between the two). Reuse the branch rather
			// than fail on "branch already exists".
			if err := repo.WorktreeAdd(ctx, path, id, git.WorktreeAddOptions{}); err != nil {
				return fmt.Errorf("lifecycle: create worktree %s reusing existing branch %s: %w", path, id, err)
			}
		}

		result = EnsureResult{Path: path, Created: true, Branch: id}
		return touchActivity(worktreesDir, id)
	})
	if err != nil {
		return EnsureResult{}, err
	}
	return result, nil
}
