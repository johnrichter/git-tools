package lifecycle

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// backdateActivity makes id look idle for at least age by rewinding its
// activity marker's mtime, so age-based Reap tests don't depend on real
// wall-clock waits.
func backdateActivity(t *testing.T, worktreesDir, id string, age time.Duration) {
	t.Helper()
	marker := activityMarkerPath(worktreesDir, id)
	past := time.Now().Add(-age)
	if err := os.Chtimes(marker, past, past); err != nil {
		t.Fatalf("backdate activity marker for %s: %v", id, err)
	}
}

func TestReap_RemovesAgedCleanWorktree(t *testing.T) {
	repo := newScratchRepo(t)
	ctx := context.Background()
	worktreesDir := WorktreesDir(repo)

	wt, err := Ensure(ctx, repo, "old-task")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	backdateActivity(t, worktreesDir, "old-task", 48*time.Hour)

	reaped, err := Reap(ctx, repo, ReapOptions{MaxAge: 24 * time.Hour})
	if err != nil {
		t.Fatalf("Reap: %v", err)
	}
	if len(reaped) != 1 || reaped[0].Path != wt.Path || reaped[0].Reason != "age" {
		t.Fatalf("Reap() = %+v, want one age-reaped entry for %s", reaped, wt.Path)
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatal("aged worktree directory still exists after Reap")
	}
	if !branchExists(ctx, repo, "old-task") {
		t.Error("Reap deleted the branch for an age-reaped worktree; it must only remove the checkout")
	}
}

func TestReap_NeverRemovesDirtyWorktree(t *testing.T) {
	repo := newScratchRepo(t)
	ctx := context.Background()
	worktreesDir := WorktreesDir(repo)

	wt, err := Ensure(ctx, repo, "dirty-task")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	writeFileT(t, wt.Path, "wip.txt", "not committed\n")
	backdateActivity(t, worktreesDir, "dirty-task", 48*time.Hour)

	reaped, err := Reap(ctx, repo, ReapOptions{MaxAge: 24 * time.Hour})
	if err != nil {
		t.Fatalf("Reap: %v", err)
	}
	if len(reaped) != 0 {
		t.Fatalf("Reap() = %+v, want nothing removed for a dirty worktree", reaped)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("dirty worktree must survive Reap: %v", err)
	}
}

func TestReap_NeverRemovesRecentlyUsedWorktree(t *testing.T) {
	repo := newScratchRepo(t)
	ctx := context.Background()

	wt, err := Ensure(ctx, repo, "fresh-task")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}

	reaped, err := Reap(ctx, repo, ReapOptions{MaxAge: 24 * time.Hour})
	if err != nil {
		t.Fatalf("Reap: %v", err)
	}
	if len(reaped) != 0 {
		t.Fatalf("Reap() = %+v, want nothing removed for a freshly-created worktree", reaped)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("fresh worktree must survive Reap: %v", err)
	}
}

func TestReap_RemovesOrphanedWorktreeDirectory(t *testing.T) {
	repo := newScratchRepo(t)
	ctx := context.Background()

	wt, err := Ensure(ctx, repo, "orphan-task")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	// Simulate the administrative half of the worktree vanishing (e.g. a
	// crash mid-remove) while the working directory survives: git no
	// longer lists it, but the .git file inside it remains.
	if err := os.RemoveAll(filepath.Join(repo, ".git", "worktrees", "orphan-task")); err != nil {
		t.Fatalf("remove worktree admin dir: %v", err)
	}

	reaped, err := Reap(ctx, repo, ReapOptions{MaxAge: 24 * time.Hour})
	if err != nil {
		t.Fatalf("Reap: %v", err)
	}
	if len(reaped) != 1 || reaped[0].Path != wt.Path || reaped[0].Reason != "orphan" {
		t.Fatalf("Reap() = %+v, want one orphan-reaped entry for %s", reaped, wt.Path)
	}
	if _, err := os.Stat(wt.Path); !os.IsNotExist(err) {
		t.Fatal("orphaned worktree directory still exists after Reap")
	}
}

func TestReap_NeverTouchesUnrelatedDirectory(t *testing.T) {
	repo := newScratchRepo(t)
	ctx := context.Background()
	worktreesDir := WorktreesDir(repo)

	stray := filepath.Join(worktreesDir, "not-ours")
	if err := os.MkdirAll(stray, 0o755); err != nil {
		t.Fatalf("mkdir stray dir: %v", err)
	}
	writeFileT(t, stray, "note.txt", "an operator left this here\n")

	reaped, err := Reap(ctx, repo, ReapOptions{MaxAge: 0})
	if err != nil {
		t.Fatalf("Reap: %v", err)
	}
	for _, r := range reaped {
		if r.Path == stray {
			t.Fatal("Reap removed a directory with no git state; it must never touch a non-worktree artifact")
		}
	}
	if _, err := os.Stat(stray); err != nil {
		t.Fatalf("unrelated directory must survive Reap: %v", err)
	}
}

func TestReap_DryRunReportsWithoutRemoving(t *testing.T) {
	repo := newScratchRepo(t)
	ctx := context.Background()
	worktreesDir := WorktreesDir(repo)

	wt, err := Ensure(ctx, repo, "old-task")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	backdateActivity(t, worktreesDir, "old-task", 48*time.Hour)

	reaped, err := Reap(ctx, repo, ReapOptions{MaxAge: 24 * time.Hour, DryRun: true})
	if err != nil {
		t.Fatalf("Reap: %v", err)
	}
	if len(reaped) != 1 || reaped[0].Reason != "age" {
		t.Fatalf("Reap(DryRun) = %+v, want one reported age-eligible entry", reaped)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("DryRun must not remove anything: %v", err)
	}
}

func TestReap_NoWorktreesDirIsANoOp(t *testing.T) {
	repo := newScratchRepo(t)
	reaped, err := Reap(context.Background(), repo, ReapOptions{})
	if err != nil {
		t.Fatalf("Reap on a repo with no worktrees dir: %v", err)
	}
	if len(reaped) != 0 {
		t.Fatalf("Reap() = %+v, want none", reaped)
	}
}
