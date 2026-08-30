package lifecycle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnrichter/claude-shared-tooling/go/git"
	"github.com/johnrichter/git-tools/internal/signing"
)

func TestComplete_MergesBackAndCleansUp(t *testing.T) {
	// Complete's signing gate re-signs the source branch's unsigned commits
	// before landing them, so this fixture carries a resolvable key but
	// leaves commit.gpgsign off: that unsigned-source state is what exercises
	// the rewrite. signableScratchRepo would pre-sign the commits and skip it.
	repo := newScratchRepo(t)
	configureSigningKeyT(t, repo)
	ctx := context.Background()

	wt, err := Ensure(ctx, repo, "task-1")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	commitFileT(t, wt.Path, "feature.txt", "done\n")

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
	// Complete mints a merge commit and must sign it, so this fixture needs a
	// resolvable key under isolation from the host's own config.
	repo := signableScratchRepo(t)
	ctx := context.Background()

	wt, err := Ensure(ctx, repo, "task-1")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	commitFileT(t, wt.Path, "feature.txt", "done\n")

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

// TestComplete_ForceNoLongerWaivesDirtyRefusal is SC-C4's guard against a
// parallel override: removal runs through internal/worktreeclean, whose
// Options carries no Force, so CompleteOptions.Force (kept only for API
// compatibility) cannot waive the refusal the way it used to.
func TestComplete_ForceNoLongerWaivesDirtyRefusal(t *testing.T) {
	repo := newScratchRepo(t)
	ctx := context.Background()

	wt, err := Ensure(ctx, repo, "task-1")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	writeFileT(t, wt.Path, "uncommitted.txt", "not staged\n")

	if _, err := Complete(ctx, repo, "task-1", CompleteOptions{Force: true}); err == nil {
		t.Fatal("Complete with Force on a dirty worktree = nil error, want a refusal: Force no longer waives it")
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("refused Complete must leave the worktree in place even with Force set: %v", err)
	}
}

// TestComplete_DirtyRefusalMatchesSharedRuleText proves the refusal Complete
// raises is worktreeclean's own text, not a parallel message: it names the
// same offending path and the same three remedies (SC-C5) the standalone
// `worktree remove` verb reports for the identical condition, and never
// mentions the removed --force override.
func TestComplete_DirtyRefusalMatchesSharedRuleText(t *testing.T) {
	repo := newScratchRepo(t)
	ctx := context.Background()

	wt, err := Ensure(ctx, repo, "task-1")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	writeFileT(t, wt.Path, "uncommitted.txt", "not staged\n")

	_, err = Complete(ctx, repo, "task-1", CompleteOptions{})
	if err == nil {
		t.Fatal("Complete on a dirty worktree = nil error, want a refusal")
	}
	for _, want := range []string{"uncommitted.txt", "commit it", "ignore it", "delete it deliberately"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %q; want the same shared refusal internal/cli's worktree remove reports", err.Error(), want)
		}
	}
	if strings.Contains(err.Error(), "--force") {
		t.Errorf("refusal %q still references the removed --force override", err.Error())
	}
}

// TestComplete_DirtyRefusalDoesNotUndoTheMerge proves the merge-before-remove
// order: even when the removal step refuses on a dirty tree, the branch's
// commits already landed on BaseRef by the time that refusal fires, and the
// branch itself is left intact rather than deleted out from under a merge
// that never fully completed.
func TestComplete_DirtyRefusalDoesNotUndoTheMerge(t *testing.T) {
	// Complete mints a merge commit and must sign it, so this fixture needs a
	// resolvable key under isolation from the host's own config.
	repo := signableScratchRepo(t)
	ctx := context.Background()

	wt, err := Ensure(ctx, repo, "task-1")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	commitFileT(t, wt.Path, "feature.txt", "done\n")
	writeFileT(t, wt.Path, "uncommitted.txt", "not staged\n")

	if _, err := Complete(ctx, repo, "task-1", CompleteOptions{}); err == nil {
		t.Fatal("Complete on a dirty worktree = nil error, want a refusal")
	}
	if _, err := os.Stat(filepath.Join(repo, "feature.txt")); err != nil {
		t.Fatalf("a dirty-tree refusal must not undo the merge that already landed: %v", err)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("refused Complete must leave the worktree in place: %v", err)
	}
	if !branchExists(ctx, repo, "task-1") {
		t.Error("a refused cleanup must not delete the branch")
	}
}

func TestComplete_ConflictLeavesWorktreeIntact(t *testing.T) {
	// Complete's signing gate runs ahead of the conflict this test provokes,
	// so the fixture needs a resolvable key under isolation from the host's
	// own config.
	repo := signableScratchRepo(t)
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

// TestComplete_SignsMintedMergeCommit proves SC-B1's parity for the
// lifecycle merge-back path: in a signable repository, a merge that mints a
// commit of its own (forced here by diverging base so no fast-forward is
// possible) is signed, exactly like the merge verb's own forced-merge-commit
// case.
func TestComplete_SignsMintedMergeCommit(t *testing.T) {
	repo := signableScratchRepo(t)
	ctx := context.Background()

	wt, err := Ensure(ctx, repo, "task-1")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	commitFileT(t, wt.Path, "feature.txt", "done\n")
	commitFileT(t, repo, "base.txt", "diverge\n") // base is no longer feature's ancestor

	if _, err := Complete(ctx, repo, "task-1", CompleteOptions{}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if parents := runGitT(t, repo, "rev-list", "--parents", "-1", "HEAD"); len(strings.Fields(parents)) != 3 {
		t.Fatalf("HEAD is not a two-parent merge commit: %q", parents)
	}
	if state := runGitT(t, repo, "log", "-1", "--format=%G?", "HEAD"); state != "G" && state != "U" {
		t.Fatalf("minted merge commit signature state = %q, want G or U", state)
	}
}

// TestComplete_KeylessRepoRefusesBeforeMergeAndRemoval proves: the signing
// gate runs before repo.Merge (K8, SC-F3) and returns its *signing.Refusal
// as a plain error (never a diagnostic type); the base ref never moves and
// the worktree is never removed; and the refusal text is the signing
// package's own — the one gate, SIGN-CONTRACT, that the merge verb's CLI
// path reports for the identical unresolved-key condition.
func TestComplete_KeylessRepoRefusesBeforeMergeAndRemoval(t *testing.T) {
	repo := newScratchRepo(t)
	ctx := context.Background()

	wt, err := Ensure(ctx, repo, "task-1")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	commitFileT(t, wt.Path, "feature.txt", "done\n") // unsigned: commit.gpgsign is false
	breakSigningKeyT(t, repo)
	preHead := runGitT(t, repo, "rev-parse", "HEAD")

	_, err = Complete(ctx, repo, "task-1", CompleteOptions{})
	if err == nil {
		t.Fatal("Complete in a keyless repository = nil error, want the signing gate's refusal")
	}
	var refusal *signing.Refusal
	if !errors.As(err, &refusal) {
		t.Fatalf("Complete error = %v (%T), want it to wrap *signing.Refusal", err, err)
	}
	if got := refusal.Code(); got != "precondition_unmet.git.signing_key_unresolved" {
		t.Errorf("refusal code = %q, want precondition_unmet.git.signing_key_unresolved", got)
	}
	// This is the exact text internal/signing.Gate raises for this condition —
	// the same text the merge verb's CLI reports for a merge of the identical
	// shape, since both call the one gate.
	if got, want := err.Error(), "no key resolved for commit signing, so merging task-1 would land unsigned commits: "; !strings.HasPrefix(got, want) {
		t.Errorf("refusal message = %q, want it to start with %q", got, want)
	}
	if got := runGitT(t, repo, "rev-parse", "HEAD"); got != preHead {
		t.Errorf("base ref moved despite the refusal: HEAD = %s, want %s", got, preHead)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("a gate refusal must run before removal and leave the worktree in place: %v", err)
	}
	if !branchExists(ctx, repo, "task-1") {
		t.Error("a gate refusal must not delete the branch")
	}
}

// TestComplete_MintedCommitUnsignable_RefusesBeforeMergeAndRemoval covers the
// branch TestComplete_KeylessRepoRefusesBeforeMergeAndRemoval does not reach:
// the source's own commits already verify (Gate's "already_signed" path, no
// re-signing needed), so the gate itself raises no refusal, but the base has
// diverged so the merge cannot fast-forward and must mint a merge commit of
// its own (SC-B1). With no key available for that commit, Complete must
// refuse at the merge-commit check — the same one internal/cli's merge verb
// hits in TestMerge_MintedCommitUnsignable_Refuses30NotInternal — not attempt
// an unsigned `git merge`. The base ref must not move and the worktree must
// still be present, exactly as any other pre-merge refusal.
func TestComplete_MintedCommitUnsignable_RefusesBeforeMergeAndRemoval(t *testing.T) {
	repo := signableScratchRepo(t)
	ctx := context.Background()

	wt, err := Ensure(ctx, repo, "task-1")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	commitFileT(t, wt.Path, "feature.txt", "done\n") // signed: commit.gpgsign is true
	commitFileT(t, repo, "base.txt", "diverge\n")    // base is no longer feature's ancestor
	breakSigningKeyT(t, repo)                        // break the key only after both commits verify
	preHead := runGitT(t, repo, "rev-parse", "HEAD")

	_, err = Complete(ctx, repo, "task-1", CompleteOptions{})
	if err == nil {
		t.Fatal("Complete with an unsignable minted merge commit = nil error, want a refusal")
	}
	var refusal *signing.Refusal
	if errors.As(err, &refusal) {
		t.Fatalf("Complete error = %v (%T), want a plain error from the merge-commit check, not a *signing.Refusal from Gate (Gate must have passed: the source already verifies)", err, err)
	}
	if got, want := err.Error(), "no key resolved for commit signing, so the merge commit for task-1 would be unsigned: "; !strings.HasPrefix(got, want) {
		t.Errorf("refusal message = %q, want it to start with %q (matching internal/cli's merge verb for the identical minted-commit-unsignable condition)", got, want)
	}
	if got := runGitT(t, repo, "rev-parse", "HEAD"); got != preHead {
		t.Errorf("base ref moved despite the refusal: HEAD = %s, want %s", got, preHead)
	}
	if _, err := os.Stat(wt.Path); err != nil {
		t.Fatalf("a merge-commit-signing refusal must run before removal and leave the worktree in place: %v", err)
	}
	if !branchExists(ctx, repo, "task-1") {
		t.Error("a merge-commit-signing refusal must not delete the branch")
	}
}

func TestComplete_UnknownIDFails(t *testing.T) {
	repo := newScratchRepo(t)
	if _, err := Complete(context.Background(), repo, "never-created", CompleteOptions{}); err == nil {
		t.Fatal("Complete on an unregistered id = nil error, want an error")
	}
}
