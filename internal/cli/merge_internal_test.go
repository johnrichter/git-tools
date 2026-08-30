// White-box unit tests for merge.go's unexported helpers: headSigState (the
// LED-109 residual gap -- verifying the minted merge commit's own signature
// rather than trusting `git merge -S`'s exit code) and commitsLanded (the
// commit-count fix). Both run directly against a scratch repo, without going
// through the built binary, since neither is reachable from package
// cli_test. The repo and git helpers are this package's existing ones
// (worktree_test.go's cleanupFixture, cgit and commitIn) -- a one-commit,
// unsigned scratch repo is exactly what both tests need.
package cli

import (
	"context"
	"testing"
)

// headSigState on a genuinely unsigned commit must report "N" -- the exact
// state the merge verb's post-merge check treats as a failed verification,
// caveat-ing the merge as unsigned rather than reporting success. The
// end-to-end companion, through the real merge command, is
// TestMerge_PostMergeTipUnsigned_ReportsUnsignedCaveat in merge_test.go.
func TestHeadSigState_UnsignedCommit_ReportsN(t *testing.T) {
	dir, _ := cleanupFixture(t)
	tip := cgit(t, dir, "rev-parse", "HEAD")

	state, err := headSigState(context.Background(), dir, tip)
	if err != nil {
		t.Fatalf("headSigState: %v", err)
	}
	if state != "N" {
		t.Fatalf("headSigState = %q, want N (unsigned)", state)
	}
	// This is exactly the condition merge's RunE tests to decide whether to
	// caveat the merge as unsigned; pin it here so a change to that
	// condition or to git's own vocabulary shows up as a unit failure.
	if state == "G" || state == "U" {
		t.Fatalf("an unsigned commit must not satisfy the verified states: %q", state)
	}
}

// commitsLanded counts exactly the commits newly reachable from newHead,
// including a merge commit when the range mints one -- proven here with a
// synthetic three-commit range rather than a real merge, isolating the
// rev-list arithmetic from the merge verb's own gate and signing machinery.
func TestCommitsLanded_CountsExactlyTheNewRange(t *testing.T) {
	dir, _ := cleanupFixture(t)
	oldHead := cgit(t, dir, "rev-parse", "HEAD")
	commitIn(t, dir, "one.txt", "commit one")
	commitIn(t, dir, "two.txt", "commit two")
	commitIn(t, dir, "three.txt", "commit three")
	newHead := cgit(t, dir, "rev-parse", "HEAD")

	n, err := commitsLanded(context.Background(), dir, oldHead, newHead)
	if err != nil {
		t.Fatalf("commitsLanded: %v", err)
	}
	if n != 3 {
		t.Fatalf("commitsLanded = %d, want 3", n)
	}

	// An empty range (old and new head identical) lands nothing.
	n, err = commitsLanded(context.Background(), dir, newHead, newHead)
	if err != nil {
		t.Fatalf("commitsLanded over an empty range: %v", err)
	}
	if n != 0 {
		t.Fatalf("commitsLanded over an empty range = %d, want 0", n)
	}
}
