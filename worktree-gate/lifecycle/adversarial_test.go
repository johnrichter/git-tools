package lifecycle

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestComplete_RejectsEmptyID mirrors Ensure's empty-id guard: an empty id
// can never name a real worktree, so Complete must fail fast instead of
// resolving it to some ambient path.
func TestComplete_RejectsEmptyID(t *testing.T) {
	repo := newScratchRepo(t)
	if _, err := Complete(context.Background(), repo, "", CompleteOptions{}); err == nil {
		t.Fatal("Complete(id=\"\") = nil error, want an error")
	}
}

// TestComplete_RefusesMismatchedBaseRef verifies Complete never checks out
// a different branch on the caller's behalf: asking to land on a BaseRef
// other than what's currently checked out must fail closed, not silently
// merge onto the wrong branch or force a checkout.
func TestComplete_RefusesMismatchedBaseRef(t *testing.T) {
	repo := newScratchRepo(t)
	ctx := context.Background()

	if _, err := Ensure(ctx, repo, "task-1"); err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	runGitT(t, repo, "checkout", "-q", "-b", "other-base")

	_, err := Complete(ctx, repo, "task-1", CompleteOptions{BaseRef: "main"})
	if err == nil {
		t.Fatal("Complete with BaseRef != checked-out branch = nil error, want a refusal")
	}
	if !branchExists(ctx, repo, "task-1") {
		t.Error("refused Complete must leave the worktree's branch intact")
	}
	wtPath := WorktreePath(repo, "task-1")
	if _, statErr := os.Stat(wtPath); statErr != nil {
		t.Fatalf("refused Complete must leave the worktree in place: %v", statErr)
	}
}

// TestReap_UsesDirectoryMtimeWhenNoActivityMarker exercises the fallback
// path in lastActivity: a worktree created without ever going through
// Ensure (e.g. by a plain `git worktree add`) has no activity marker at
// all. Reap must still be able to age it out using the directory's own
// mtime, not crash or treat it as eternally fresh.
func TestReap_UsesDirectoryMtimeWhenNoActivityMarker(t *testing.T) {
	repo := newScratchRepo(t)
	ctx := context.Background()
	worktreesDir := WorktreesDir(repo)

	path := WorktreePath(repo, "manual-task")
	if err := os.MkdirAll(worktreesDir, 0o755); err != nil {
		t.Fatalf("mkdir worktrees dir: %v", err)
	}
	runGitT(t, repo, "worktree", "add", "-q", "-b", "manual-task", path, "HEAD")

	past := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(path, past, past); err != nil {
		t.Fatalf("backdate worktree dir mtime: %v", err)
	}

	reaped, err := Reap(ctx, repo, ReapOptions{MaxAge: 24 * time.Hour})
	if err != nil {
		t.Fatalf("Reap: %v", err)
	}
	if len(reaped) != 1 || reaped[0].Path != path || reaped[0].Reason != "age" {
		t.Fatalf("Reap() = %+v, want one age-reaped entry for %s (mtime fallback)", reaped, path)
	}
}

// TestReap_ConcurrentWithEnsure_NeverRemovesEnsuredWorktree is the direct
// adversarial test for SC-WORKTREE's concurrency-safety requirement: fire
// Ensure and Reap at the same repository from concurrent goroutines,
// repeatedly, and confirm the shared repo lock fully serializes them so
// Reap can never observe (and remove) a worktree mid-creation, and an
// worktree Ensure just refreshed is never reaped out from under it.
func TestReap_ConcurrentWithEnsure_NeverRemovesEnsuredWorktree(t *testing.T) {
	repo := newScratchRepo(t)
	ctx := context.Background()

	const rounds = 20
	var wg sync.WaitGroup
	errs := make(chan error, rounds*2)

	for i := range rounds {
		wg.Add(2)
		go func(n int) {
			defer wg.Done()
			if _, err := Ensure(ctx, repo, "task-1"); err != nil {
				errs <- err
			}
		}(i)
		go func(n int) {
			defer wg.Done()
			// MaxAge=0 with DefaultMaxAge substitution keeps this a
			// realistic reap window (7 days), so nothing this test just
			// created is ever old enough to qualify on age; only a bug
			// that lets Reap observe an inconsistent registry could
			// remove it.
			if _, err := Reap(ctx, repo, ReapOptions{}); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent Ensure/Reap: %v", err)
	}

	path := WorktreePath(repo, "task-1")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("worktree removed by concurrent Reap despite being actively Ensure'd: %v", err)
	}
	if !branchExists(ctx, repo, "task-1") {
		t.Error("branch removed by concurrent Reap despite the worktree being active")
	}
}

// TestReap_AgedWorktreeSurvivesRaceWithConcurrentEnsure pins down the
// ordering guarantee the shared lock exists to provide: an Ensure call
// that refreshes an aged-but-clean worktree's activity marker, racing
// against a Reap sweep, must never leave the worktree removed while its
// activity marker says "just used" -- the two states can't get out of
// sync because both paths hold the same lock for their full duration.
func TestReap_AgedWorktreeSurvivesRaceWithConcurrentEnsure(t *testing.T) {
	repo := newScratchRepo(t)
	ctx := context.Background()
	worktreesDir := WorktreesDir(repo)

	if _, err := Ensure(ctx, repo, "task-1"); err != nil {
		t.Fatalf("seed Ensure: %v", err)
	}
	backdateActivity(t, worktreesDir, "task-1", 48*time.Hour)

	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		if _, err := Ensure(ctx, repo, "task-1"); err != nil {
			errs <- err
		}
	}()
	go func() {
		defer wg.Done()
		if _, err := Reap(ctx, repo, ReapOptions{MaxAge: 24 * time.Hour}); err != nil {
			errs <- err
		}
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent Ensure/Reap: %v", err)
	}

	path := WorktreePath(repo, "task-1")
	marker := activityMarkerPath(worktreesDir, "task-1")
	_, statErr := os.Stat(path)
	_, markerErr := os.Stat(marker)
	// Either outcome is a legitimate race resolution -- Ensure ran first
	// (worktree stays, marker fresh) or Reap ran first (worktree and
	// marker both gone). The only inconsistent, lock-violating outcome is
	// the worktree missing while a fresh marker exists, or vice versa in a
	// way that would let a *third* observer see a half-updated state.
	if statErr == nil && markerErr != nil {
		t.Error("worktree survived but its activity marker vanished: lock did not serialize Ensure/Reap")
	}
	if statErr != nil && markerErr == nil {
		t.Error("worktree removed but its activity marker survived: lock did not serialize Ensure/Reap")
	}
}

// TestReap_DoesNotDeleteBranchStillCheckedOutElsewhere_NoOtherWorktree is a
// boundary check on age-reap's "never touch the branch" guarantee across
// repeated cycles: after an age-reap, the branch must remain resolvable
// and re-checkoutable via a fresh Ensure using the *same* id, proving no
// commit the worktree held became unreachable by name.
func TestReap_AgedWorktreeBranchStaysUsableAfterReap(t *testing.T) {
	repo := newScratchRepo(t)
	ctx := context.Background()
	worktreesDir := WorktreesDir(repo)

	wt, err := Ensure(ctx, repo, "old-task")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	writeFileT(t, wt.Path, "work.txt", "kept\n")
	runGitT(t, wt.Path, "add", "-A")
	runGitT(t, wt.Path, "commit", "-q", "-m", "wip")
	backdateActivity(t, worktreesDir, "old-task", 48*time.Hour)

	reaped, err := Reap(ctx, repo, ReapOptions{MaxAge: 24 * time.Hour})
	if err != nil {
		t.Fatalf("Reap: %v", err)
	}
	if len(reaped) != 1 {
		t.Fatalf("Reap() = %+v, want exactly one reaped entry", reaped)
	}

	second, err := Ensure(ctx, repo, "old-task")
	if err != nil {
		t.Fatalf("re-Ensure after age-reap must succeed since the branch survives: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(second.Path, "work.txt")); statErr != nil {
		t.Fatalf("re-Ensure lost the commit the reaped worktree held: %v", statErr)
	}
}

// TestWorktreesDir_SiblingNotNestedUnderRepoRoot pins the isolation
// guarantee WorktreesDir's doc comment claims: a linked worktree's tracked
// surface must never end up nested inside the primary checkout's own
// working tree, or every gated write inside it would start showing up as
// untracked noise (or worse, get committed) in the primary repo.
func TestWorktreesDir_SiblingNotNestedUnderRepoRoot(t *testing.T) {
	repo := newScratchRepo(t)
	dir := WorktreesDir(repo)
	rel, err := filepath.Rel(repo, dir)
	if err != nil {
		t.Fatalf("filepath.Rel: %v", err)
	}
	if rel == "." || !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		t.Errorf("WorktreesDir(%q) = %q, want a path outside repoRoot (rel=%q)", repo, dir, rel)
	}
}
