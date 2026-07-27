package detect

import (
	"errors"
	"testing"
)

func TestFindRepoRoot_PrimaryCheckout(t *testing.T) {
	fs := newFakeFS().dir("/repo/.git")
	root, gitEntry, found, err := FindRepoRoot(fs.lstat, "/repo/a/b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found || root != "/repo" || gitEntry != "/repo/.git" {
		t.Errorf("FindRepoRoot() = root=%q entry=%q found=%v, want /repo, /repo/.git, true", root, gitEntry, found)
	}
}

func TestFindRepoRoot_NotYetCreatedDirectory(t *testing.T) {
	// The target directory doesn't exist yet; the walk must still resolve
	// against the nearest existing ancestor's repo.
	fs := newFakeFS().dir("/repo/.git")
	root, _, found, err := FindRepoRoot(fs.lstat, "/repo/new/nested/dir")
	if err != nil || !found || root != "/repo" {
		t.Fatalf("FindRepoRoot() over an uncreated dir = root=%q found=%v err=%v", root, found, err)
	}
}

func TestFindRepoRoot_NoRepo(t *testing.T) {
	fs := newFakeFS()
	_, _, found, err := FindRepoRoot(fs.lstat, "/tmp/scratch")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Error("expected no repo found")
	}
}

func TestFindRepoRoot_Indeterminate(t *testing.T) {
	permErr := errors.New("permission denied")
	fs := newFakeFS().errAt("/repo/sub/.git", permErr)
	_, _, found, err := FindRepoRoot(fs.lstat, "/repo/sub")
	if found {
		t.Error("expected found=false on an indeterminate error")
	}
	if !errors.Is(err, ErrIndeterminate) {
		t.Errorf("err = %v, want wrapping ErrIndeterminate", err)
	}
}

func TestClassifyGitEntry_Primary(t *testing.T) {
	fs := newFakeFS().dir("/repo/.git")
	if got := ClassifyGitEntry(fs.lstat, fs.readFile, "/repo/.git"); got != KindPrimary {
		t.Errorf("ClassifyGitEntry() = %v, want KindPrimary", got)
	}
}

func TestClassifyGitEntry_Worktree(t *testing.T) {
	fs := newFakeFS().file("/repo/.claude/worktrees/slug/.git", "gitdir: /repo/.git/worktrees/slug\n")
	got := ClassifyGitEntry(fs.lstat, fs.readFile, "/repo/.claude/worktrees/slug/.git")
	if got != KindWorktree {
		t.Errorf("ClassifyGitEntry() = %v, want KindWorktree", got)
	}
}

func TestClassifyGitEntry_UnrecognizedContent(t *testing.T) {
	fs := newFakeFS().file("/repo/.git", "not a gitdir line\n")
	got := ClassifyGitEntry(fs.lstat, fs.readFile, "/repo/.git")
	if got != KindIndeterminate {
		t.Errorf("ClassifyGitEntry() = %v, want KindIndeterminate on unrecognized content", got)
	}
}

func TestClassifyGitEntry_UnreadableFile(t *testing.T) {
	fs := newFakeFS().file("/repo/.git", "gitdir: /repo/.git/worktrees/slug\n")
	fs.nodes["/repo/.git"] = fakeNode{statErr: errors.New("boom")}
	got := ClassifyGitEntry(fs.lstat, fs.readFile, "/repo/.git")
	if got != KindIndeterminate {
		t.Errorf("ClassifyGitEntry() = %v, want KindIndeterminate on a stat error", got)
	}
}
