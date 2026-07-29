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

	if !worktreeRegisteredAt(list, target) {
		t.Fatalf("worktreeRegisteredAt(%q) = false, want true: entry is in the list", target)
	}
	if !worktreeRegisteredAt(list, target+string(os.PathSeparator)) {
		t.Fatalf("worktreeRegisteredAt with a trailing separator = false, want true: same resolved path")
	}
}

func TestWorktreeRegisteredAt_MissingEntryIsUnverified(t *testing.T) {
	dir := t.TempDir()
	registered := filepath.Join(dir, "other")
	claimed := filepath.Join(dir, "review")

	list := []git.WorktreeInfo{{Path: registered, Branch: "other"}}

	if worktreeRegisteredAt(list, claimed) {
		t.Fatalf("worktreeRegisteredAt(%q) = true, want false: %s was never registered", claimed, claimed)
	}
}

func TestWorktreeRegisteredAt_EmptyListIsUnverified(t *testing.T) {
	if worktreeRegisteredAt(nil, filepath.Join(t.TempDir(), "review")) {
		t.Fatal("worktreeRegisteredAt(nil list) = true, want false")
	}
}
