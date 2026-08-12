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

func TestBranchCreate_NoForce_NewBranch_Succeeds(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	head := runGit(t, dir, "rev-parse", "HEAD")

	r, exit := runCLI(t, bin, "--repo", dir, "branch", "create", "feature", "HEAD")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	if got := runGit(t, dir, "rev-parse", "feature"); got != head {
		t.Fatalf("feature = %s, want %s", got, head)
	}
}

func TestBranchCreate_Force_MoveForwardAlongOwnHistory_Succeeds(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	oldTip := runGit(t, dir, "rev-parse", "HEAD")
	runGit(t, dir, "branch", "feature", oldTip)
	newTip := commitFile(t, dir, "advance.txt", "advance\n", "advance main")

	r, exit := runCLI(t, bin, "--repo", dir, "branch", "create", "feature", "main", "--force")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	if got := runGit(t, dir, "rev-parse", "feature"); got != newTip {
		t.Fatalf("feature = %s, want %s (moved forward)", got, newTip)
	}
}

func TestBranchCreate_Force_UnrelatedCommit_RefusesAndLeavesRefUnmoved(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	oldTip := branchWithCommit(t, dir, "feature")
	unrelated := commitFile(t, dir, "sibling.txt", "sibling\n", "unrelated work on main")

	r, exit := runCLI(t, bin, "--repo", dir, "branch", "create", "feature", unrelated, "--force")
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("status=%s exit=%d, want precondition_unmet/30: %+v", r.Status, exit, r)
	}
	if len(r.Errors) == 0 {
		t.Fatalf("refusal carries no error diagnostic: %+v", r)
	}
	msg, _ := r.Errors[0]["message"].(string)
	if !strings.Contains(msg, "feature") || !strings.Contains(msg, oldTip) || !strings.Contains(msg, unrelated) || !strings.Contains(msg, "1") {
		t.Fatalf("refusal does not name the branch, its current tip, the requested start point, and the orphaned count: %q", msg)
	}
	if got := runGit(t, dir, "rev-parse", "feature"); got != oldTip {
		t.Fatalf("the ref moved despite the refusal: got %s want %s", got, oldTip)
	}
}

func TestBranchCreate_Force_BackwardCommit_RefusesAndLeavesRefUnmoved(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	base := runGit(t, dir, "rev-parse", "HEAD")
	tip := commitFile(t, dir, "one.txt", "one\n", "one")
	runGit(t, dir, "branch", "feature", tip)

	r, exit := runCLI(t, bin, "--repo", dir, "branch", "create", "feature", base, "--force")
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("status=%s exit=%d, want precondition_unmet/30: %+v", r.Status, exit, r)
	}
	if got := runGit(t, dir, "rev-parse", "feature"); got != tip {
		t.Fatalf("the ref moved despite the refusal: got %s want %s", got, tip)
	}
}

func TestBranchCreate_Force_NonExistentBranch_IsPlainCreation(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	head := runGit(t, dir, "rev-parse", "HEAD")

	r, exit := runCLI(t, bin, "--repo", dir, "branch", "create", "feature", "HEAD", "--force")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	if got := runGit(t, dir, "rev-parse", "feature"); got != head {
		t.Fatalf("feature = %s, want %s", got, head)
	}
}

func TestBranchCreate_Force_DryRun_OrphaningMove_ReportsVerdictWithoutMoving(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	oldTip := branchWithCommit(t, dir, "feature")
	unrelated := commitFile(t, dir, "sibling.txt", "sibling\n", "unrelated work on main")

	r, exit := runCLI(t, bin, "--repo", dir, "branch", "create", "feature", unrelated, "--force", "--dry-run")
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("status=%s exit=%d, want precondition_unmet/30: %+v", r.Status, exit, r)
	}
	if got := runGit(t, dir, "rev-parse", "feature"); got != oldTip {
		t.Fatalf("the ref moved despite --dry-run: got %s want %s", got, oldTip)
	}
}

// TestBranchCreate_Force_SameTip_Succeeds probes the boundary of the
// reachability guard: moving a branch to a start-point that IS its own
// current tip orphans zero commits (a tip is reachable from itself), so the
// guard must permit it rather than treating "no movement" as suspicious.
func TestBranchCreate_Force_SameTip_Succeeds(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	tip := runGit(t, dir, "rev-parse", "HEAD")
	runGit(t, dir, "branch", "feature", tip)

	r, exit := runCLI(t, bin, "--repo", dir, "branch", "create", "feature", tip, "--force")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	if got := runGit(t, dir, "rev-parse", "feature"); got != tip {
		t.Fatalf("feature = %s, want %s (unchanged)", got, tip)
	}
}

// TestBranchCreate_Force_UnresolvableStartPoint_FallsThroughToGitError checks
// that an unresolvable start-point on an existing branch is left to
// CreateBranch's own error path (git's message), not swallowed or
// reinterpreted by the reachability guard -- unchanged from today per AC3.
func TestBranchCreate_Force_UnresolvableStartPoint_FallsThroughToGitError(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	tip := branchWithCommit(t, dir, "feature")

	r, exit := runCLI(t, bin, "--repo", dir, "branch", "create", "feature", "no-such-ref-xyz", "--force")
	if r.Status == "success" || exit == 0 {
		t.Fatalf("status=%s exit=%d, want a failure for an unresolvable start-point: %+v", r.Status, exit, r)
	}
	if r.Status == "precondition_unmet" {
		t.Fatalf("unresolvable start-point was reported as the reachability guard's own refusal, not git's error: %+v", r)
	}
	if got := runGit(t, dir, "rev-parse", "feature"); got != tip {
		t.Fatalf("the ref moved despite the failure: got %s want %s", got, tip)
	}
}

// TestBranchCreate_NoForce_ExistingBranch_RefusesLikeToday checks that
// omitting --force on an existing branch still fails via git's own
// already-exists error, untouched by the reachability guard, per AC3.
func TestBranchCreate_NoForce_ExistingBranch_RefusesLikeToday(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	tip := branchWithCommit(t, dir, "feature")
	// A start-point the guard, if wrongly applied, would happily allow (the
	// existing tip itself) -- proving the failure is "already exists", not
	// a reachability refusal that happens to fire only when --force is used.
	r, exit := runCLI(t, bin, "--repo", dir, "branch", "create", "feature", tip)
	if r.Status == "success" || exit == 0 {
		t.Fatalf("status=%s exit=%d, want a failure: creating an existing branch without --force must still refuse: %+v", r.Status, exit, r)
	}
	if r.Status == "precondition_unmet" {
		t.Fatalf("no-force refusal was reported through the reachability guard's precondition_unmet path instead of git's own already-exists error: %+v", r)
	}
	if got := runGit(t, dir, "rev-parse", "feature"); got != tip {
		t.Fatalf("the ref moved despite the failure: got %s want %s", got, tip)
	}
}

func TestBranchCreate_Force_DryRun_ReachableMove_SucceedsWithoutMoving(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	oldTip := runGit(t, dir, "rev-parse", "HEAD")
	runGit(t, dir, "branch", "feature", oldTip)
	commitFile(t, dir, "advance.txt", "advance\n", "advance main")

	r, exit := runCLI(t, bin, "--repo", dir, "branch", "create", "feature", "main", "--force", "--dry-run")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	if got := runGit(t, dir, "rev-parse", "feature"); got != oldTip {
		t.Fatalf("the ref moved despite --dry-run: got %s want %s", got, oldTip)
	}
}
