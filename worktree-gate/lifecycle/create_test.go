package lifecycle

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEnsure_CreatesOffCurrentHEAD(t *testing.T) {
	repo := newScratchRepo(t)
	head := runGitT(t, repo, "rev-parse", "HEAD")

	result, err := Ensure(context.Background(), repo, "task-1")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	wantPath := WorktreePath(repo, "task-1")
	if result.Path != wantPath || !result.Created {
		t.Fatalf("Ensure() = %+v, want Path=%s Created=true", result, wantPath)
	}
	if _, err := os.Stat(filepath.Join(wantPath, "README.md")); err != nil {
		t.Fatalf("worktree missing checked-out content: %v", err)
	}
	got := runGitT(t, wantPath, "rev-parse", "HEAD")
	if got != head {
		t.Errorf("worktree HEAD = %s, want %s (repo's HEAD at Ensure time)", got, head)
	}
	branch := runGitT(t, wantPath, "rev-parse", "--abbrev-ref", "HEAD")
	if branch != "task-1" {
		t.Errorf("worktree branch = %s, want task-1", branch)
	}
}

func TestEnsure_ReturnsExistingWithoutRecreating(t *testing.T) {
	repo := newScratchRepo(t)
	ctx := context.Background()

	first, err := Ensure(ctx, repo, "task-1")
	if err != nil {
		t.Fatalf("first Ensure: %v", err)
	}
	writeFileT(t, first.Path, "work.txt", "in progress\n")
	runGitT(t, first.Path, "add", "-A")
	runGitT(t, first.Path, "commit", "-q", "-m", "wip")

	second, err := Ensure(ctx, repo, "task-1")
	if err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	if second.Created {
		t.Error("second Ensure reported Created=true for an already-existing worktree")
	}
	if second.Path != first.Path {
		t.Errorf("second Ensure Path = %s, want %s", second.Path, first.Path)
	}
	if _, err := os.Stat(filepath.Join(second.Path, "work.txt")); err != nil {
		t.Fatalf("second Ensure lost the worktree's committed work: %v", err)
	}
}

func TestEnsure_RecoversFromBranchWithoutWorktree(t *testing.T) {
	repo := newScratchRepo(t)
	ctx := context.Background()

	// Simulate a crash between branch creation and worktree registration.
	runGitT(t, repo, "branch", "task-1")

	result, err := Ensure(ctx, repo, "task-1")
	if err != nil {
		t.Fatalf("Ensure did not recover from a pre-existing branch: %v", err)
	}
	if !result.Created {
		t.Error("Ensure() Created = false, want true")
	}
	branch := runGitT(t, result.Path, "rev-parse", "--abbrev-ref", "HEAD")
	if branch != "task-1" {
		t.Errorf("recovered worktree branch = %s, want task-1", branch)
	}
}

func TestEnsure_RejectsEmptyID(t *testing.T) {
	repo := newScratchRepo(t)
	if _, err := Ensure(context.Background(), repo, ""); err == nil {
		t.Error("Ensure(id=\"\") = nil error, want an error")
	}
}

func TestEnsure_RejectsNonSegmentID(t *testing.T) {
	repo := newScratchRepo(t)
	ctx := context.Background()
	before := runGitT(t, repo, "worktree", "list")

	for _, id := range []string{"feature/task-1", "..", ".", "a/b/c"} {
		if _, err := Ensure(ctx, repo, id); err == nil {
			t.Errorf("Ensure(id=%q) = nil error, want rejection", id)
		}
		// The write side must fail closed: no worktree registered, so a
		// retry (or Reap) is never left chasing an unmanaged checkout.
		if after := runGitT(t, repo, "worktree", "list"); after != before {
			t.Errorf("Ensure(id=%q) mutated git state:\nbefore:\n%s\nafter:\n%s", id, before, after)
		}
	}
}

func TestEnsure_TouchesActivityMarkerOnEachCall(t *testing.T) {
	repo := newScratchRepo(t)
	ctx := context.Background()

	if _, err := Ensure(ctx, repo, "task-1"); err != nil {
		t.Fatalf("first Ensure: %v", err)
	}
	marker := activityMarkerPath(WorktreesDir(repo), "task-1")
	firstInfo, err := os.Stat(marker)
	if err != nil {
		t.Fatalf("stat activity marker: %v", err)
	}
	past := firstInfo.ModTime().Add(-1 * time.Hour)
	if err := os.Chtimes(marker, past, past); err != nil {
		t.Fatalf("backdate marker: %v", err)
	}

	if _, err := Ensure(ctx, repo, "task-1"); err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	secondInfo, err := os.Stat(marker)
	if err != nil {
		t.Fatalf("stat activity marker after second Ensure: %v", err)
	}
	if !secondInfo.ModTime().After(past) {
		t.Error("second Ensure did not refresh the activity marker")
	}
}
