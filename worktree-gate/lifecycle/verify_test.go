package lifecycle

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Independent verification pass: exercises the validateID fix on Complete's
// path (Ensure's side is already covered by TestEnsure_RejectsNonSegmentID)
// and a Reap-vs-active-worktree race where "active" means an uncommitted
// write is landing on disk concurrently with the sweep, not just a fresh
// activity marker.

func TestComplete_RejectsNonSegmentID_NoGitMutation(t *testing.T) {
	repo := newScratchRepo(t)
	ctx := context.Background()
	before := runGitT(t, repo, "worktree", "list")

	for _, id := range []string{"feature/task-1", "..", ".", "a/b/c", "/etc"} {
		if _, err := Complete(ctx, repo, id, CompleteOptions{}); err == nil {
			t.Errorf("Complete(id=%q) = nil error, want rejection", id)
		}
		if after := runGitT(t, repo, "worktree", "list"); after != before {
			t.Errorf("Complete(id=%q) mutated git state:\nbefore:\n%s\nafter:\n%s", id, before, after)
		}
	}
}

// TestReap_NeverRemovesWorktreeMidWrite races Reap (MaxAge=0, so every
// registered worktree looks maximally stale by the clock alone) against a
// goroutine that keeps an uncommitted file dirty in the worktree throughout
// the sweep. isDirty must observe the write and Reap must skip it on every
// iteration -- a single removal is a failure, since removing a worktree with
// live uncommitted changes is unrecoverable data loss.
func TestReap_NeverRemovesWorktreeMidWrite(t *testing.T) {
	repo := newScratchRepo(t)
	ctx := context.Background()

	result, err := Ensure(ctx, repo, "task-1")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	// Force the activity marker to look ancient so only dirtiness protects
	// the worktree from age-based reaping.
	marker := activityMarkerPath(WorktreesDir(repo), "task-1")
	ancient := time.Now().Add(-30 * 24 * time.Hour)
	if err := os.Chtimes(marker, ancient, ancient); err != nil {
		t.Fatalf("backdate marker: %v", err)
	}

	target := filepath.Join(result.Path, "work.txt")
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		i := 0
		for {
			select {
			case <-stop:
				return
			default:
			}
			i++
			if err := os.WriteFile(target, []byte(time.Now().String()), 0o644); err != nil {
				t.Errorf("background write: %v", err)
				return
			}
			time.Sleep(time.Millisecond)
		}
	}()

	for i := 0; i < 20; i++ {
		reaped, err := Reap(ctx, repo, ReapOptions{MaxAge: time.Nanosecond})
		if err != nil {
			t.Fatalf("Reap iteration %d: %v", i, err)
		}
		for _, r := range reaped {
			if r.Path == result.Path {
				close(stop)
				<-done
				t.Fatalf("Reap removed a worktree with an in-flight uncommitted write (reason=%s)", r.Reason)
			}
		}
		if _, err := os.Stat(result.Path); err != nil {
			close(stop)
			<-done
			t.Fatalf("worktree path vanished during sweep: %v", err)
		}
	}
	close(stop)
	<-done
}
