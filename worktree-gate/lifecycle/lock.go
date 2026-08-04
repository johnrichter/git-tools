package lifecycle

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gofrs/flock"
)

// defaultLockTimeout bounds how long withRepoLock waits for the repo lock
// when the caller's context carries no earlier deadline of its own. It is
// generous enough to sit behind a slow git operation but short enough that
// a genuinely stuck holder surfaces as an error rather than a silent hang.
const defaultLockTimeout = 30 * time.Second

// lockRetryDelay is how often withRepoLock polls for the lock while
// waiting.
const lockRetryDelay = 50 * time.Millisecond

// withRepoLock serializes every lifecycle mutation (Ensure, Complete, Reap)
// against one repository's worktrees directory through a single OS-level
// advisory file lock. The lock is held by the kernel against an open file
// descriptor, so it is released automatically if the holding process dies —
// a crash mid-operation can never wedge it permanently the way a hand-rolled
// PID lock file could.
func withRepoLock(ctx context.Context, worktreesDir string, fn func() error) error {
	if err := os.MkdirAll(worktreesDir, 0o755); err != nil {
		return fmt.Errorf("lifecycle: create %s: %w", worktreesDir, err)
	}

	lockCtx := ctx
	if _, hasDeadline := ctx.Deadline(); !hasDeadline {
		var cancel context.CancelFunc
		lockCtx, cancel = context.WithTimeout(ctx, defaultLockTimeout)
		defer cancel()
	}

	lockPath := filepath.Join(worktreesDir, lockFileName)
	fl := flock.New(lockPath)
	locked, err := fl.TryLockContext(lockCtx, lockRetryDelay)
	if err != nil {
		return fmt.Errorf("lifecycle: acquire lock %s: %w", lockPath, err)
	}
	if !locked {
		return fmt.Errorf("lifecycle: timed out waiting for lock %s", lockPath)
	}
	defer func() { _ = fl.Unlock() }()

	return fn()
}
