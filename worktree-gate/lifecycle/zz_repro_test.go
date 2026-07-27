package lifecycle

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepro_SlashID(t *testing.T) {
	repo := newScratchRepo(t)
	_, err := Ensure(context.Background(), repo, "feature/task-1")
	t.Logf("slash id err=%v", err)
	// show git still registered it
	out := runGitT(t, repo, "worktree", "list")
	t.Logf("worktree list:\n%s", out)
}

func TestRepro_Traversal(t *testing.T) {
	repo := newScratchRepo(t)
	res, err := Ensure(context.Background(), repo, "../evil")
	t.Logf("traversal id err=%v path=%s", err, res.Path)
	wd := WorktreesDir(repo)
	t.Logf("worktreesDir=%s escaped=%v", wd, !strings.HasPrefix(filepath.Clean(res.Path), filepath.Clean(wd)))
}
