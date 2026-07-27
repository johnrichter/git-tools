package lifecycle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/johnrichter/claude-shared-tooling/go/git"
)

func TestComplete_MergesBackAndCleansUp(t *testing.T) {
	repo := newScratchRepo(t)
	ctx := context.Background()

	wt, err := Ensure(ctx, repo, "task-1")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	writeFileT(t, wt.Path, "feature.txt", "done\n")
	runGitT(t, wt.Path, "add", "-A")
	runGitT(t, wt.Path, "commit", "-q", "-m", "add feature")

	result, err := Complete(ctx, repo, "task-1", CompleteOptions{})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if !result.Merged {
		t.Error("Complete() Merged = false, want true")
	}
	if !result.BranchDeleted {
		t.Error("Complete() BranchDeleted = false, want true")
	}
	if _, err := os.Stat(filepath.Join(repo, "feature.txt")); err != nil {
		t.Fatalf("merge-back did not land the worktree's commit: %v", err)
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree directory still exists after Complete: err=%v", err)
	}
	if branchExists(ctx, repo, "task-1") {
		t.Error("branch task-1 still exists after Complete with KeepBranch=false")
	}
	if _, err := os.Stat(activityMarkerPath(WorktreesDir(repo), "task-1")); !os.IsNotExist(err) {
		t.Error("activity marker not cleaned up after Complete")
	}
}

func TestComplete_KeepBranch(t *testing.T) {
	repo := newScratchRepo(t)
	ctx := context.Background()

	wt, err := Ensure(ctx, repo, "task-1")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	writeFileT(t, wt.Path, "feature.txt", "done\n")
	runGitT(t, wt.Path, "add", "-A")
	runGitT(t, wt.Path, "commit", "-q", "-m", "add feature")

	if _, err := Complete(ctx, repo, "task-1", CompleteOptions{KeepBranch: true}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if !branchExists(ctx, repo, "task-1") {
		t.Error("branch task-1 was deleted despite KeepBranch=true")
	}
}

func TestComplete_RefusesDirtyWorktree(t *testing.T) {
	repo := newScratchRepo(t)
	ctx := context.Background()

	wt, err := Ensure(ctx, repo, "task-1")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	writeFileT(t, wt.Path, "uncommitted.txt", "not staged\n")

	if _, err := Complete(ctx, repo, "task-1", CompleteOptions{}); err == nil {
		t.Fatal("Complete on a dirty worktree = nil error, want a refusal")
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("refused Complete must leave the worktree in place: %v", err)
	}
}

func TestComplete_ForceDiscardsUncommittedChanges(t *testing.T) {
	repo := newScratchRepo(t)
	ctx := context.Background()

	wt, err := Ensure(ctx, repo, "task-1")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	writeFileT(t, wt.Path, "uncommitted.txt", "not staged\n")

	if _, err := Complete(ctx, repo, "task-1", CompleteOptions{Force: true}); err != nil {
		t.Fatalf("Complete with Force: %v", err)
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatalf("worktree directory still exists after forced Complete: err=%v", err)
	}
}

func TestComplete_ConflictLeavesWorktreeIntact(t *testing.T) {
	repo := newScratchRepo(t)
	ctx := context.Background()

	wt, err := Ensure(ctx, repo, "task-1")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	writeFileT(t, wt.Path, "README.md", "worktree change\n")
	runGitT(t, wt.Path, "add", "-A")
	runGitT(t, wt.Path, "commit", "-q", "-m", "conflicting change")

	writeFileT(t, repo, "README.md", "base change\n")
	runGitT(t, repo, "add", "-A")
	runGitT(t, repo, "commit", "-q", "-m", "base moves on")

	_, err = Complete(ctx, repo, "task-1", CompleteOptions{})
	if err == nil {
		t.Fatal("Complete on a conflicting merge = nil error, want a conflict error")
	}
	var conflictErr *git.ConflictError
	if !errors.As(err, &conflictErr) {
		t.Errorf("Complete error = %v, want it to wrap *git.ConflictError", err)
	}
	if _, statErr := os.Stat(wt.Path); statErr != nil {
		t.Fatalf("a failed merge must leave the worktree in place: %v", statErr)
	}
}

func TestComplete_NoOpMergeStillCleansUp(t *testing.T) {
	repo := newScratchRepo(t)
	ctx := context.Background()

	wt, err := Ensure(ctx, repo, "task-1")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	// No commits made in the worktree: the branch is identical to base.

	result, err := Complete(ctx, repo, "task-1", CompleteOptions{})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if result.Merged {
		t.Error("Complete() Merged = true for a branch with no new commits, want false")
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatal("worktree not removed after a no-op merge")
	}
}

func TestComplete_UnknownIDFails(t *testing.T) {
	repo := newScratchRepo(t)
	if _, err := Complete(context.Background(), repo, "never-created", CompleteOptions{}); err == nil {
		t.Fatal("Complete on an unregistered id = nil error, want an error")
	}
}
