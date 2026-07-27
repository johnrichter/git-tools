package lifecycle

import (
	"fmt"
	"path/filepath"
	"strings"
)

// activityDirName holds one mtime-bearing marker file per worktree id,
// tracking Ensure-driven activity without writing anything into a
// worktree's own tracked working tree (which would show up as untracked
// noise in every git-status check this package relies on).
const activityDirName = ".activity"

// lockFileName is the advisory lock every mutating call in this package
// acquires before touching a repository's worktrees directory.
const lockFileName = ".lifecycle.lock"

// WorktreesDir is the directory this package creates linked worktrees
// under: a sibling of repoRoot, never nested inside it, so a worktree's
// tracked-file surface can never overlap the primary checkout's.
func WorktreesDir(repoRoot string) string {
	return filepath.Join(filepath.Dir(filepath.Clean(repoRoot)), ".worktrees")
}

// WorktreePath is where id's linked worktree lives under WorktreesDir.
func WorktreePath(repoRoot, id string) string {
	return filepath.Join(WorktreesDir(repoRoot), id)
}

// validateID rejects any id that is not a single, self-contained path
// segment. The worktrees directory is flat — Reap sweeps it with a
// one-level ReadDir and matches directory names to git's worktree list — so
// an id carrying a path separator would nest the checkout where Reap can
// never find it, and "." / ".." (or a leading separator) would let the
// worktree escape the worktrees directory entirely. Rejecting them fail-
// closed, before any git mutation, keeps every worktree inside the single
// directory this package manages.
func validateID(id string) error {
	if id == "" {
		return fmt.Errorf("lifecycle: worktree id must not be empty")
	}
	if id == "." || id == ".." || strings.ContainsRune(id, '/') || strings.ContainsRune(id, filepath.Separator) {
		return fmt.Errorf("lifecycle: worktree id %q must be a single path segment (no path separators, %q, or %q)", id, ".", "..")
	}
	return nil
}
