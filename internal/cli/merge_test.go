// End-to-end tests for merge's opt-in worktree cleanup, driving the built
// binary against scratch repositories. They pin the three observable
// contracts: --cleanup removes the merged branch's worktree, omitting it
// removes nothing, and a cleanup that refuses is reported as a caveat on a
// merge that is never rolled back.
package cli_test

import (
	"os"
	"path/filepath"
	"testing"
)

// featureWorktree adds a linked worktree on a new branch off main, commits one
// file on it, and returns the worktree path and the branch's tip SHA.
func featureWorktree(t *testing.T, dir, branch string) (string, string) {
	t.Helper()
	wt := filepath.Join(t.TempDir(), branch)
	runGit(t, dir, "worktree", "add", "-b", branch, wt, "main")
	tip := commitFile(t, wt, branch+".txt", branch+"\n", branch+" work")
	return wt, tip
}

func TestMerge_CleanupRemovesMergedWorktree(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	wt, tip := featureWorktree(t, dir, "feature")

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature", "--cleanup")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	if got := runGit(t, dir, "rev-parse", "HEAD"); got != tip {
		t.Fatalf("main did not advance to feature tip: got %s want %s", got, tip)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("--cleanup left the merged worktree %s behind", wt)
	}
	if r.Data["cleaned_worktrees"] == nil {
		t.Fatalf("success result names no cleaned worktree: %+v", r.Data)
	}
}

func TestMerge_NoCleanupFlag_RemovesNothing(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	wt, _ := featureWorktree(t, dir, "feature")

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("merge without --cleanup disturbed the worktree %s: %v", wt, err)
	}
	if r.Data["cleaned_worktrees"] != nil {
		t.Fatalf("merge without --cleanup reported a removal: %+v", r.Data)
	}
}

// A cleanup that refuses (here: a live sub-worktree nests under the merged
// branch's worktree) must not unwind the merge. The command reports the merge
// as landed, names the unremoved worktree, and exits with the benign caveats
// code rather than an error.
func TestMerge_CleanupRefusal_ReportsCaveatAndKeepsMerge(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	wt, tip := featureWorktree(t, dir, "feature")
	nested := filepath.Join(wt, "task")
	runGit(t, dir, "worktree", "add", "-b", "task", nested, "main")

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature", "--cleanup")
	if r.Status != "caveats" || exit != 10 {
		t.Fatalf("status=%s exit=%d, want caveats/10: %+v", r.Status, exit, r)
	}
	if got := runGit(t, dir, "rev-parse", "HEAD"); got != tip {
		t.Fatalf("the merge was rolled back after a cleanup refusal: HEAD=%s want %s", got, tip)
	}
	if r.Data["new_head"] == nil {
		t.Fatalf("caveat result does not report the landed merge: %+v", r.Data)
	}
	if r.Data["unremoved_worktrees"] == nil {
		t.Fatalf("caveat result names no unremoved worktree: %+v", r.Data)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("a refusing cleanup still removed the worktree %s: %v", wt, err)
	}
}
