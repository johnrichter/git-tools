package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/johnrichter/claude-shared-tooling/go/git"
)

// DefaultMaxAge is how long a clean, idle worktree may sit before Reap
// treats it as abandoned.
const DefaultMaxAge = 7 * 24 * time.Hour

// ReapOptions configures Reap.
type ReapOptions struct {
	// MaxAge is the idle threshold for age-based reaping. Zero uses
	// DefaultMaxAge.
	MaxAge time.Duration
	// DryRun reports what Reap would remove without removing anything.
	DryRun bool
}

// ReapedWorktree is one worktree Reap removed, or, under DryRun, would
// remove.
type ReapedWorktree struct {
	Path   string
	Reason string // "age" or "orphan"
}

// Reap sweeps repoRoot's worktrees directory for abandoned linked
// worktrees. Two conditions make a worktree eligible, and nothing else
// does:
//
//   - orphan: the directory carries worktree state (a `.git` entry) but
//     git no longer recognizes it as one of this repository's linked
//     worktrees — left behind by a crash mid-create or mid-remove.
//   - age: git recognizes it, but it has sat idle past MaxAge (per Ensure's
//     activity marker) AND has no uncommitted changes.
//
// A recognized worktree that is dirty or was used within MaxAge is left
// alone, and so is a directory under the worktrees dir that carries no git
// state at all — this sweep only ever touches its own artifacts. Age-based
// reaping removes only the checkout (`git worktree remove`); it never
// deletes the branch, so no commit a worktree ever held becomes unreachable
// by name. Orphan removal deletes the directory outright, since git already
// considers it not a worktree.
//
// Every entry is evaluated independently: one failure is recorded and
// evaluation continues, so a single stuck worktree cannot block the rest of
// the sweep. The returned slice lists what succeeded (or, under DryRun,
// what would be removed); a non-nil error reports what didn't.
func Reap(ctx context.Context, repoRoot string, opts ReapOptions) ([]ReapedWorktree, error) {
	maxAge := opts.MaxAge
	if maxAge <= 0 {
		maxAge = DefaultMaxAge
	}

	worktreesDir := WorktreesDir(repoRoot)
	if _, err := os.Stat(worktreesDir); os.IsNotExist(err) {
		return nil, nil
	}

	repo, err := git.Open(ctx, repoRoot)
	if err != nil {
		return nil, fmt.Errorf("lifecycle: open repo %s: %w", repoRoot, err)
	}

	var reaped []ReapedWorktree
	err = withRepoLock(ctx, worktreesDir, func() error {
		// Best-effort: let git reconcile its own registry against
		// worktree directories that vanished without going through
		// WorktreeRemove. A failure here doesn't change what this sweep
		// does with the .worktrees directory itself.
		_, _ = runGit(ctx, repoRoot, "worktree", "prune")

		list, err := repo.WorktreeList(ctx)
		if err != nil {
			return fmt.Errorf("lifecycle: list worktrees: %w", err)
		}
		registered := make(map[string]struct{}, len(list))
		for _, wt := range list {
			registered[canonPath(wt.Path)] = struct{}{}
		}

		entries, err := os.ReadDir(worktreesDir)
		if err != nil {
			return fmt.Errorf("lifecycle: read %s: %w", worktreesDir, err)
		}

		var errs []error
		now := time.Now()
		for _, entry := range entries {
			if !entry.IsDir() || entry.Name() == activityDirName {
				continue // lock file, activity dir, or other non-worktree entry
			}
			id := entry.Name()
			path := filepath.Join(worktreesDir, id)

			if _, ok := registered[canonPath(path)]; !ok {
				looksLikeWorktree, err := hasGitEntry(path)
				if err != nil {
					errs = append(errs, fmt.Errorf("lifecycle: inspect %s: %w", path, err))
					continue
				}
				if !looksLikeWorktree {
					continue // not a worktree artifact; never touch it
				}
				if !opts.DryRun {
					if err := os.RemoveAll(path); err != nil {
						errs = append(errs, fmt.Errorf("lifecycle: remove orphan %s: %w", path, err))
						continue
					}
					_ = removeActivity(worktreesDir, id)
				}
				reaped = append(reaped, ReapedWorktree{Path: path, Reason: "orphan"})
				continue
			}

			dirty, err := isDirty(ctx, path)
			if err != nil {
				errs = append(errs, fmt.Errorf("lifecycle: check %s for uncommitted changes: %w", path, err))
				continue
			}
			if dirty {
				continue // active: never remove
			}
			last, err := lastActivity(worktreesDir, id, path)
			if err != nil {
				errs = append(errs, fmt.Errorf("lifecycle: read last activity for %s: %w", path, err))
				continue
			}
			if now.Sub(last) < maxAge {
				continue // active: recently used
			}

			if !opts.DryRun {
				if err := repo.WorktreeRemove(ctx, path, git.WorktreeRemoveOptions{}); err != nil {
					errs = append(errs, fmt.Errorf("lifecycle: remove aged worktree %s: %w", path, err))
					continue
				}
				_ = removeActivity(worktreesDir, id)
			}
			reaped = append(reaped, ReapedWorktree{Path: path, Reason: "age"})
		}
		return errors.Join(errs...)
	})
	return reaped, err
}

// hasGitEntry reports whether path contains a `.git` file or directory —
// the marker that path was, at some point, a git working tree rather than
// an unrelated directory a caller happens to have placed under the
// worktrees dir.
func hasGitEntry(path string) (bool, error) {
	_, err := os.Lstat(filepath.Join(path, ".git"))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}
