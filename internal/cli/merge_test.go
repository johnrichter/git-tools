// End-to-end tests for the merge verb's signing gate and its opt-in worktree
// cleanup, driving the built binary against scratch repositories. Every merge
// runs the gate, so every test here uses the signing fixture: --cleanup
// removes the merged branch's worktree, omitting it removes nothing, a cleanup
// that refuses is reported as a caveat on a merge that is never rolled back,
// and an unsigned source is re-signed or refused before anything lands.
package cli_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// signingRepo is initRepo plus a working commit-signing setup, so commits made
// after it returns are signed and verify.
//
// The format is ssh, chosen deliberately: it is what this environment signs
// with in production, and it needs no signing binary beyond ssh-keygen, which
// git's ssh signing already requires. The key pair is generated per test under
// the test's own tempdir and never leaves it, and the allowed-signers file is
// what makes git report the resulting signatures as verifying (%G? = G) rather
// than unverifiable. Nothing here reaches the network or the host's keys.
//
// There is no skip path: a host without ssh-keygen fails these tests rather
// than silently dropping the gate's coverage.
//
// initRepo's own root commit stays unsigned, which is deliberate — it is every
// gated range's fork point, and the gate must not care what sits below it.
func signingRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Fatalf("ssh-keygen is required to sign this fixture's commits: %v", err)
	}
	dir := initRepo(t)
	keyDir := t.TempDir()
	key := filepath.Join(keyDir, "signing-key")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "git-tools signing fixture", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	allowed := filepath.Join(keyDir, "allowed_signers")
	if err := os.WriteFile(allowed, []byte(`test@example.com namespaces="git" `+string(pub)), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "config", "gpg.format", "ssh")
	runGit(t, dir, "config", "user.signingkey", key+".pub")
	runGit(t, dir, "config", "gpg.ssh.allowedSignersFile", allowed)
	runGit(t, dir, "config", "commit.gpgsign", "true")
	return dir
}

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
	dir := signingRepo(t)
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
	dir := signingRepo(t)
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
	dir := signingRepo(t)
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

// unsignedBranch creates branch off main carrying one unsigned commit, in a
// repository that otherwise signs, and returns the branch's tip.
func unsignedBranch(t *testing.T, dir, branch string) string {
	t.Helper()
	runGit(t, dir, "checkout", "-q", "-b", branch, "main")
	runGit(t, dir, "config", "commit.gpgsign", "false")
	tip := commitFile(t, dir, branch+".txt", branch+"\n", branch+" work")
	runGit(t, dir, "config", "commit.gpgsign", "true")
	runGit(t, dir, "checkout", "-q", "main")
	return tip
}

// gateRecord returns the signing_gate entry the result carries for source.
func gateRecord(t *testing.T, r wireResult, source string) map[string]any {
	t.Helper()
	entries, _ := r.Data["signing_gate"].([]any)
	for _, entry := range entries {
		record, _ := entry.(map[string]any)
		if record["source"] == source {
			return record
		}
	}
	t.Fatalf("result carries no signing_gate entry for %s: %+v", source, r.Data)
	return nil
}

func TestMerge_UnsignedSource_IsResignedBeforeLanding(t *testing.T) {
	bin := buildCLI(t)
	dir := signingRepo(t)
	unsignedTip := unsignedBranch(t, dir, "feature")

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	record := gateRecord(t, r, "feature")
	if record["action"] != "resigned" {
		t.Fatalf("gate action=%v, want resigned: %+v", record["action"], record)
	}
	if record["backup_tag"] == "" || record["backup_tag"] == nil {
		t.Fatalf("a rewrite reported no backup tag: %+v", record)
	}
	if got := runGit(t, dir, "rev-parse", "feature"); got == unsignedTip {
		t.Fatalf("feature still points at the unsigned commit %s", got)
	}
	runGit(t, dir, "verify-commit", "HEAD")
}

// A dry-run merge runs the gate's dry run and stops there: it reports the
// rewrite it would apply and moves no ref.
func TestMerge_DryRun_ReportsTheRewriteWithoutApplyingIt(t *testing.T) {
	bin := buildCLI(t)
	dir := signingRepo(t)
	unsignedTip := unsignedBranch(t, dir, "feature")
	head := runGit(t, dir, "rev-parse", "HEAD")

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature", "--dry-run")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	if record := gateRecord(t, r, "feature"); record["action"] != "would_resign" {
		t.Fatalf("gate action=%v, want would_resign: %+v", record["action"], record)
	}
	if got := runGit(t, dir, "rev-parse", "feature"); got != unsignedTip {
		t.Fatalf("a dry run moved feature: got %s want %s", got, unsignedTip)
	}
	if got := runGit(t, dir, "rev-parse", "HEAD"); got != head {
		t.Fatalf("a dry run moved HEAD: got %s want %s", got, head)
	}
}

func TestMerge_AlreadyContainedSource_IsSkippedNotAnError(t *testing.T) {
	bin := buildCLI(t)
	dir := signingRepo(t)
	runGit(t, dir, "branch", "feature")

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	if record := gateRecord(t, r, "feature"); record["action"] != "empty_range" {
		t.Fatalf("gate action=%v, want empty_range: %+v", record["action"], record)
	}
}

func TestMerge_NoSigningKey_RefusesRatherThanLandingUnsigned(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	// A repository-local ssh signing key that does not exist resolves to no key
	// at all, and being local it overrides whatever the host's own git config
	// carries — the refusal cannot accidentally sign with a real key.
	runGit(t, dir, "config", "gpg.format", "ssh")
	runGit(t, dir, "config", "user.signingkey", filepath.Join(t.TempDir(), "absent-key.pub"))
	head := runGit(t, dir, "rev-parse", "HEAD")
	unsignedBranch(t, dir, "feature")

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature")
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("status=%s exit=%d, want precondition_unmet/30: %+v", r.Status, exit, r)
	}
	if len(r.Errors) == 0 || !strings.Contains(r.Errors[0]["message"].(string), "no key resolved") {
		t.Fatalf("refusal does not name the unresolved key: %+v", r.Errors)
	}
	if got := runGit(t, dir, "rev-parse", "HEAD"); got != head {
		t.Fatalf("main moved despite the refusal: HEAD=%s want %s", got, head)
	}
}

// An octopus merge gates each source in turn, so a refusal on a later source
// follows a rewrite that already landed on an earlier one. The refusal names
// what was rewritten and merges nothing.
func TestMerge_OctopusLaterSourceRefusal_ReportsEarlierRewrite(t *testing.T) {
	bin := buildCLI(t)
	dir := signingRepo(t)
	head := runGit(t, dir, "rev-parse", "HEAD")
	unsignedBranch(t, dir, "alpha")

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "alpha", "beta")
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("status=%s exit=%d, want precondition_unmet/30: %+v", r.Status, exit, r)
	}
	context, _ := r.Errors[0]["context"].(map[string]any)
	rewritten, _ := context["rewritten"].([]any)
	if len(rewritten) != 1 {
		t.Fatalf("refusal does not report the one earlier rewrite: %+v", context)
	}
	entry, _ := rewritten[0].(map[string]any)
	if entry["source"] != "alpha" || entry["backup_tag"] == "" {
		t.Fatalf("rewrite report is incomplete: %+v", entry)
	}
	if got := runGit(t, dir, "rev-parse", "HEAD"); got != head {
		t.Fatalf("the merge landed despite the refusal: HEAD=%s want %s", got, head)
	}
}

// The companion to the case above, with a refusal the gate itself reaches
// rather than one that rejects the argument: beta is a real branch the gate
// walks and finds no fork point for. Same accumulation, same report, from the
// far side of the argument checks.
func TestMerge_OctopusUnrelatedLaterSource_ReportsEarlierRewrite(t *testing.T) {
	bin := buildCLI(t)
	dir := signingRepo(t)
	head := runGit(t, dir, "rev-parse", "HEAD")
	unsignedBranch(t, dir, "alpha")
	runGit(t, dir, "switch", "-q", "--orphan", "beta")
	commitFile(t, dir, "beta.txt", "beta\n", "beta work")
	runGit(t, dir, "checkout", "-q", "main")

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "alpha", "beta")
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("status=%s exit=%d, want precondition_unmet/30: %+v", r.Status, exit, r)
	}
	if code, _ := r.Errors[0]["code"].(string); !strings.Contains(code, "merge_no_fork_point") {
		t.Fatalf("refusal code=%q, want the no-fork-point refusal: %+v", code, r.Errors[0])
	}
	context, _ := r.Errors[0]["context"].(map[string]any)
	rewritten, _ := context["rewritten"].([]any)
	if len(rewritten) != 1 {
		t.Fatalf("refusal does not report the one earlier rewrite: %+v", context)
	}
	if entry, _ := rewritten[0].(map[string]any); entry["source"] != "alpha" || entry["backup_tag"] == "" {
		t.Fatalf("rewrite report is incomplete: %+v", rewritten[0])
	}
	if got := runGit(t, dir, "rev-parse", "HEAD"); got != head {
		t.Fatalf("the merge landed despite the refusal: HEAD=%s want %s", got, head)
	}
}

// A range whose commits all verify is a no-op the gate reports and steps over:
// rewriting would mint fresh SHAs for nothing and detach anything built on the
// current tip, so the branch must come out of the merge byte-for-byte itself.
func TestMerge_AlreadySignedSource_IsSkippedWithoutRewriting(t *testing.T) {
	bin := buildCLI(t)
	dir := signingRepo(t)
	runGit(t, dir, "checkout", "-q", "-b", "feature", "main")
	tip := commitFile(t, dir, "feature.txt", "feature\n", "feature work")
	runGit(t, dir, "checkout", "-q", "main")

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	record := gateRecord(t, r, "feature")
	if record["action"] != "already_signed" {
		t.Fatalf("gate action=%v, want already_signed: %+v", record["action"], record)
	}
	if record["commits"] != float64(1) {
		t.Fatalf("gate counted %v commits in range, want 1: %+v", record["commits"], record)
	}
	if got := runGit(t, dir, "rev-parse", "feature"); got != tip {
		t.Fatalf("a signed branch was rewritten anyway: feature=%s want %s", got, tip)
	}
	if tags := runGit(t, dir, "tag", "--list"); tags != "" {
		t.Fatalf("a skipped range still left a backup tag: %q", tags)
	}
}

// The octopus shape the build process actually produces: several task branches
// landing at once, each carrying unsigned work. Every source is re-signed and
// then all of them merge in one commit.
func TestMerge_OctopusTwoUnsignedSources_BothResignedThenMerged(t *testing.T) {
	bin := buildCLI(t)
	dir := signingRepo(t)
	alphaTip := unsignedBranch(t, dir, "alpha")
	betaTip := unsignedBranch(t, dir, "beta")

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "alpha", "beta", "--message", "merge alpha beta")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	for source, oldTip := range map[string]string{"alpha": alphaTip, "beta": betaTip} {
		record := gateRecord(t, r, source)
		if record["action"] != "resigned" {
			t.Fatalf("gate action for %s=%v, want resigned: %+v", source, record["action"], record)
		}
		newTip := runGit(t, dir, "rev-parse", source)
		if newTip == oldTip {
			t.Fatalf("%s still points at its unsigned commit %s", source, oldTip)
		}
		if state := runGit(t, dir, "log", "-1", "--format=%G?", source); state != "G" {
			t.Fatalf("%s tip signature state=%q, want G", source, state)
		}
		runGit(t, dir, "merge-base", "--is-ancestor", newTip, "HEAD")
	}
}
