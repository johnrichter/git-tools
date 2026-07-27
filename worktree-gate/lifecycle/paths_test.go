package lifecycle

import (
	"path/filepath"
	"testing"
)

func TestWorktreesDir_IsSiblingOfRepoRoot(t *testing.T) {
	got := WorktreesDir("/home/user/code/myrepo")
	want := "/home/user/code/.worktrees"
	if got != want {
		t.Errorf("WorktreesDir() = %s, want %s", got, want)
	}
}

func TestWorktreePath_JoinsIDUnderWorktreesDir(t *testing.T) {
	got := WorktreePath("/home/user/code/myrepo", "task-42")
	want := filepath.Join("/home/user/code/.worktrees", "task-42")
	if got != want {
		t.Errorf("WorktreePath() = %s, want %s", got, want)
	}
}
