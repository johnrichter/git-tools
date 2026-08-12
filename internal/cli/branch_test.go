// Tests for branch delete's reachability guard: it must refuse -- leaving
// the ref untouched -- when the branch carries commits unreachable from its
// landing target or when that target cannot be resolved from local refs, and
// otherwise stay out of the way of the compare-and-swap DeleteBranch already
// performs.
package cli_test

import (
	"os/exec"
	"strings"
	"testing"
)

// branchWithCommit creates branch off main carrying one commit beyond it,
// leaving main checked out afterward, and returns the branch's tip SHA.
func branchWithCommit(t *testing.T, dir, branch string) string {
	t.Helper()
	runGit(t, dir, "checkout", "-q", "-b", branch, "main")
	tip := commitFile(t, dir, branch+".txt", branch+"\n", branch+" work")
	runGit(t, dir, "checkout", "-q", "main")
	return tip
}

func TestBranchDelete_MergedBranch_Succeeds_BackupTagPresent(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	head := runGit(t, dir, "rev-parse", "HEAD")
	runGit(t, dir, "branch", "feature") // no commit beyond main: already landed

	r, exit := runCLI(t, bin, "--repo", dir, "branch", "delete", "feature", head, "--landing-target", "main")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	if r.Data["backup_tag"] == nil || r.Data["backup_tag"] == "" {
		t.Fatalf("success result carries no backup_tag: %+v", r.Data)
	}
	if err := runGitErr(t, dir, "show-ref", "--verify", "refs/heads/feature"); err == nil {
		t.Fatal("branch still exists after a successful delete")
	}
}

func TestBranchDelete_UnmergedBranch_RefusesAndLeavesRefUnmoved(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	tip := branchWithCommit(t, dir, "feature")

	r, exit := runCLI(t, bin, "--repo", dir, "branch", "delete", "feature", tip, "--landing-target", "main")
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("status=%s exit=%d, want precondition_unmet/30: %+v", r.Status, exit, r)
	}
	if len(r.Errors) == 0 {
		t.Fatalf("refusal carries no error diagnostic: %+v", r)
	}
	msg, _ := r.Errors[0]["message"].(string)
	if !strings.Contains(msg, "feature") || !strings.Contains(msg, "1") || !strings.Contains(msg, "main") {
		t.Fatalf("refusal does not name the branch, the unmerged count, and the landing target: %q", msg)
	}
	if got := runGit(t, dir, "rev-parse", "feature"); got != tip {
		t.Fatalf("the ref moved despite the refusal: got %s want %s", got, tip)
	}
}

func TestBranchDelete_UnresolvableLandingTarget_Refuses(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	tip := branchWithCommit(t, dir, "feature") // no upstream, no remote configured

	r, exit := runCLI(t, bin, "--repo", dir, "branch", "delete", "feature", tip)
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("status=%s exit=%d, want precondition_unmet/30: %+v", r.Status, exit, r)
	}
	if got := runGit(t, dir, "rev-parse", "feature"); got != tip {
		t.Fatalf("the ref moved despite the refusal: got %s want %s", got, tip)
	}
}

func TestBranchDelete_OmittedExpectedHead_ResolvesCurrentHead(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	runGit(t, dir, "branch", "feature") // no commit beyond main: already landed

	r, exit := runCLI(t, bin, "--repo", dir, "branch", "delete", "feature", "--landing-target", "main")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	if err := runGitErr(t, dir, "show-ref", "--verify", "refs/heads/feature"); err == nil {
		t.Fatal("branch still exists after a successful delete with expected-head omitted")
	}
}

func TestBranchDelete_StaleExplicitExpectedHead_StillConflicts(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	runGit(t, dir, "branch", "feature") // no commit beyond main: the guard passes

	r, exit := runCLI(t, bin, "--repo", dir, "branch", "delete", "feature",
		"0000000000000000000000000000000000000000", "--landing-target", "main")
	if r.Status != "conflict" || exit != 41 {
		t.Fatalf("status=%s exit=%d, want conflict/41: %+v", r.Status, exit, r)
	}
	if err := runGitErr(t, dir, "show-ref", "--verify", "refs/heads/feature"); err != nil {
		t.Fatal("branch was deleted despite a stale expected-head")
	}
}

// runGitErr runs a git command rooted at dir and returns its error, for
// assertions that only care whether a ref still resolves.
func runGitErr(t *testing.T, dir string, args ...string) error {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	return cmd.Run()
}
