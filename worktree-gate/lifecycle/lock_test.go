package lifecycle

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestWithRepoLock_SerializesConcurrentCallers(t *testing.T) {
	dir := t.TempDir()
	var inside int32
	var maxObserved int32
	var wg sync.WaitGroup

	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := withRepoLock(context.Background(), dir, func() error {
				n := atomic.AddInt32(&inside, 1)
				for {
					cur := atomic.LoadInt32(&maxObserved)
					if n <= cur || atomic.CompareAndSwapInt32(&maxObserved, cur, n) {
						break
					}
				}
				time.Sleep(5 * time.Millisecond)
				atomic.AddInt32(&inside, -1)
				return nil
			})
			if err != nil {
				t.Errorf("withRepoLock: %v", err)
			}
		}()
	}
	wg.Wait()

	if maxObserved != 1 {
		t.Errorf("max concurrent holders observed = %d, want 1 (lock did not serialize callers)", maxObserved)
	}
}

func TestEnsure_ConcurrentCallsCreateExactlyOnce(t *testing.T) {
	repo := newScratchRepo(t)
	var createdCount int32
	var wg sync.WaitGroup

	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, err := Ensure(context.Background(), repo, "shared-task")
			if err != nil {
				t.Errorf("Ensure: %v", err)
				return
			}
			if result.Created {
				atomic.AddInt32(&createdCount, 1)
			}
		}()
	}
	wg.Wait()

	if createdCount != 1 {
		t.Errorf("worktree created %d times concurrently, want exactly 1", createdCount)
	}
}
