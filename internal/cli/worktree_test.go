package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/johnrichter/claude-shared-tooling/go/git"
)

func TestWorktreeRegisteredAt_MatchesByResolvedPath(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "review")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}

	list := []git.WorktreeInfo{{Path: target, Branch: "review"}}

	if !worktreeRegisteredAt(list, dir, target) {
		t.Fatalf("worktreeRegisteredAt(%q) = false, want true: entry is in the list", target)
	}
	if !worktreeRegisteredAt(list, dir, target+string(os.PathSeparator)) {
		t.Fatalf("worktreeRegisteredAt with a trailing separator = false, want true: same resolved path")
	}
}

func TestWorktreeRegisteredAt_MissingEntryIsUnverified(t *testing.T) {
	dir := t.TempDir()
	registered := filepath.Join(dir, "other")
	claimed := filepath.Join(dir, "review")

	list := []git.WorktreeInfo{{Path: registered, Branch: "other"}}

	if worktreeRegisteredAt(list, dir, claimed) {
		t.Fatalf("worktreeRegisteredAt(%q) = true, want false: %s was never registered", claimed, claimed)
	}
}

func TestWorktreeRegisteredAt_EmptyListIsUnverified(t *testing.T) {
	dir := t.TempDir()
	if worktreeRegisteredAt(nil, dir, filepath.Join(dir, "review")) {
		t.Fatal("worktreeRegisteredAt(nil list) = true, want false")
	}
}

// TestWorktreeRegisteredAt_RelativePathResolvesAgainstRepoDir covers the
// case `worktree add` actually hits: a relative <path> argument is git's
// concept of "relative to the repository", not the calling process's
// current directory. A registered entry with an absolute path under
// repoDir must match a caller-supplied relative path resolved against
// repoDir, regardless of what the process's cwd happens to be.
func TestWorktreeRegisteredAt_RelativePathResolvesAgainstRepoDir(t *testing.T) {
	repoDir := t.TempDir()
	target := filepath.Join(repoDir, "review")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}

	list := []git.WorktreeInfo{{Path: target, Branch: "review"}}

	if !worktreeRegisteredAt(list, repoDir, "review") {
		t.Fatalf("worktreeRegisteredAt(%q, repoDir=%q) = false, want true: relative path should resolve against repoDir", "review", repoDir)
	}
}
