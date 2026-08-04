package lifecycle

import (
	"os"
	"path/filepath"
	"time"
)

// activityMarkerPath is where Ensure records id's last-used time: a file
// beside the worktree, not inside it, so it never appears as untracked
// noise in that worktree's own git status.
func activityMarkerPath(worktreesDir, id string) string {
	return filepath.Join(worktreesDir, activityDirName, id)
}

// touchActivity records "now" as id's last-used time.
func touchActivity(worktreesDir, id string) error {
	dir := filepath.Join(worktreesDir, activityDirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := activityMarkerPath(worktreesDir, id)
	now := time.Now()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Chtimes(path, now, now)
}

// removeActivity discards id's last-used marker. Called once its worktree
// is gone, so a future, unrelated reuse of the same id starts with no
// borrowed activity history.
func removeActivity(worktreesDir, id string) error {
	err := os.Remove(activityMarkerPath(worktreesDir, id))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

// lastActivity reports id's last-used time: the activity marker's mtime
// when Ensure has ever touched it, falling back to worktreePath's own
// mtime for a worktree that was never routed through Ensure (e.g. created
// directly with the plain worktree-add CLI).
func lastActivity(worktreesDir, id, worktreePath string) (time.Time, error) {
	info, err := os.Stat(activityMarkerPath(worktreesDir, id))
	if err == nil {
		return info.ModTime(), nil
	}
	if !os.IsNotExist(err) {
		return time.Time{}, err
	}
	info, err = os.Stat(worktreePath)
	if err != nil {
		return time.Time{}, err
	}
	return info.ModTime(), nil
}
