// End-to-end tests for the merge verb's signing gate and its opt-in worktree
// cleanup, driving the built binary against scratch repositories. Every merge
// runs the gate, so every test here uses the signing fixture: --cleanup
// removes the merged branch's worktree, omitting it removes nothing, a cleanup
// that refuses is reported as a caveat on a merge that is never rolled back,
// and an unsigned source is re-signed or refused before anything lands.
package cli_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnrichter/claude-shared-tooling/go/git"

	"github.com/johnrichter/git-tools/internal/signing"
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

// merge no longer declares --force: SC-C1/D3 removed the Force plumbing
// end to end, so both spellings of the flag are now unknown to cobra and
// must fail as a usage error before anything is merged.
func TestMerge_ForceFlag_IsUsageError(t *testing.T) {
	bin := buildCLI(t)
	dir := signingRepo(t)
	runGit(t, dir, "branch", "feature")

	before := runGit(t, dir, "rev-parse", "HEAD")
	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature", "--force")
	if r.Status != "usage" || exit != 50 {
		t.Fatalf("--force: status=%s exit=%d, want usage/50: %+v", r.Status, exit, r)
	}
	if got := runGit(t, dir, "rev-parse", "HEAD"); got != before {
		t.Fatalf("an unknown --force flag must not have moved HEAD: got %s want %s", got, before)
	}
}

func TestMerge_CleanupForceFlag_IsUsageError(t *testing.T) {
	bin := buildCLI(t)
	dir := signingRepo(t)
	wt, tip := featureWorktree(t, dir, "feature")

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature", "--cleanup", "--force")
	if r.Status != "usage" || exit != 50 {
		t.Fatalf("--cleanup --force: status=%s exit=%d, want usage/50: %+v", r.Status, exit, r)
	}
	// Nothing was attempted: the merge itself never ran, and the worktree the
	// merge would have targeted for cleanup is untouched.
	if got := runGit(t, dir, "rev-parse", "HEAD"); got == tip {
		t.Fatalf("an unknown flag must not have let the merge land")
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("worktree %s disturbed by a usage error: %v", wt, err)
	}
}

// A cleanup that cannot remove its target because the worktree is dirty
// (uncommitted changes) must behave exactly like any other cleanup refusal:
// the merge still lands, and the caveat's advice never names a --force flag,
// because no flag can override this refusal anymore (SC-C2).
func TestMerge_CleanupDirtyWorktree_ReportsCaveatAndKeepsMerge(t *testing.T) {
	bin := buildCLI(t)
	dir := signingRepo(t)
	wt, tip := featureWorktree(t, dir, "feature")
	if err := os.WriteFile(filepath.Join(wt, "feature.txt"), []byte("dirtied after landing\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature", "--cleanup")
	if r.Status != "caveats" || exit != 10 {
		t.Fatalf("status=%s exit=%d, want caveats/10: %+v", r.Status, exit, r)
	}
	if got := runGit(t, dir, "rev-parse", "HEAD"); got != tip {
		t.Fatalf("the merge was rolled back after a cleanup refusal: HEAD=%s want %s", got, tip)
	}
	if r.Data["unremoved_worktrees"] == nil {
		t.Fatalf("caveat result names no unremoved worktree: %+v", r.Data)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("a refusing cleanup still removed the dirty worktree %s: %v", wt, err)
	}
	for _, c := range r.Caveats {
		triage, _ := c["triage"].(map[string]any)
		instruction, _ := triage["instruction"].(string)
		if strings.Contains(instruction, "--force") {
			t.Fatalf("caveat triage names a removed flag: %+v", c)
		}
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

// A single non-existent source is a user precondition (a typo'd or absent
// branch), not an internal fault: the gate must catch it as a precondition
// refusal (exit 30) before the pre-merge minting check runs merge-base against
// the unresolvable ref, which would otherwise surface as internal exit 90.
func TestMerge_NonexistentSingleSource_RefusesNotInternal(t *testing.T) {
	bin := buildCLI(t)
	dir := signingRepo(t)

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "ghost-branch")
	if exit == 90 {
		t.Fatalf("a typo'd branch regressed to internal-error exit 90: %+v", r)
	}
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("status=%s exit=%d, want precondition_unmet/30: %+v", r.Status, exit, r)
	}
	if code, _ := r.Errors[0]["code"].(string); code != "precondition_unmet.git.merge_source_not_branch" {
		t.Fatalf("refusal code=%q, want precondition_unmet.git.merge_source_not_branch: %+v", code, r.Errors[0])
	}
}

// signedBranch creates branch off main carrying one signed, already-verifying
// commit — the signingRepo fixture signs by default — and returns its tip.
func signedBranch(t *testing.T, dir, branch string) string {
	t.Helper()
	runGit(t, dir, "checkout", "-q", "-b", branch, "main")
	tip := commitFile(t, dir, branch+".txt", branch+"\n", branch+" work")
	runGit(t, dir, "checkout", "-q", "main")
	return tip
}

// breakSigningKey points user.signingkey at a key that does not exist. Being
// repository-local it overrides the host's git config, so the repository can no
// longer sign a new commit — verification of existing signatures, which reads
// the untouched allowed-signers file, still works.
func breakSigningKey(t *testing.T, dir string) {
	t.Helper()
	runGit(t, dir, "config", "user.signingkey", filepath.Join(t.TempDir(), "absent-key.pub"))
}

// SC-B1: a merge that mints a commit signs that commit even when commit.gpgsign
// is unset. The source already verifies, so the gate skips it without probing;
// only the verb's own -S can make the minted merge commit verify.
func TestMerge_ForcedMergeCommit_IsSignedThoughGpgsignUnset(t *testing.T) {
	bin := buildCLI(t)
	dir := signingRepo(t)
	signedBranch(t, dir, "feature")
	runGit(t, dir, "config", "--unset", "commit.gpgsign")

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature", "--fast-forward", "never")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	if record := gateRecord(t, r, "feature"); record["action"] != "already_signed" {
		t.Fatalf("gate action=%v, want already_signed (the merge-commit probe, not the gate, is under test): %+v", record["action"], record)
	}
	if state := runGit(t, dir, "log", "-1", "--format=%G?", "HEAD"); state != "G" && state != "U" {
		t.Fatalf("minted merge commit signature state=%q, want G or U", state)
	}
	// A forbidden fast-forward mints a real two-parent merge commit, not a
	// fast-forward that would carry no new signature at all.
	if parents := runGit(t, dir, "rev-list", "--parents", "-1", "HEAD"); len(strings.Fields(parents)) != 3 {
		t.Fatalf("HEAD is not a two-parent merge commit: %q", parents)
	}
}

// SC-B2: an already-verifying source in a repository that can verify but no
// longer sign, merged where no fast-forward is possible, refuses at the
// merge-commit signing check — exit 30 with the unresolved-key code, NOT the
// pre-fix internal-error 90 a bare `git merge -S` would surface as — and leaves
// the target ref exactly where it was.
func TestMerge_MintedCommitUnsignable_Refuses30NotInternal(t *testing.T) {
	bin := buildCLI(t)
	dir := signingRepo(t)
	signedBranch(t, dir, "feature")
	commitFile(t, dir, "main.txt", "main\n", "diverge main") // main is no longer an ancestor of feature
	breakSigningKey(t, dir)
	head := runGit(t, dir, "rev-parse", "HEAD")

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature")
	if exit == 90 {
		t.Fatalf("the merge-commit signing check regressed to internal-error exit 90 (a bare `git merge -S` failing): %+v", r)
	}
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("status=%s exit=%d, want precondition_unmet/30: %+v", r.Status, exit, r)
	}
	if code, _ := r.Errors[0]["code"].(string); code != "precondition_unmet.git.signing_key_unresolved" {
		t.Fatalf("refusal code=%q, want precondition_unmet.git.signing_key_unresolved: %+v", code, r.Errors[0])
	}
	if got := runGit(t, dir, "rev-parse", "HEAD"); got != head {
		t.Fatalf("the target ref moved despite the refusal: HEAD=%s want %s", got, head)
	}
}

// An octopus (two or more sources) always mints a merge commit, so the signing
// probe must run even when every source's range already verifies and the gate
// itself never probes. Both sources verify here but the repository cannot sign:
// the merge still refuses at the merge-commit check.
func TestMerge_OctopusBothVerifyUnsignable_ProbesAndRefuses(t *testing.T) {
	bin := buildCLI(t)
	dir := signingRepo(t)
	signedBranch(t, dir, "alpha")
	signedBranch(t, dir, "beta")
	breakSigningKey(t, dir)
	head := runGit(t, dir, "rev-parse", "HEAD")

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "alpha", "beta", "--message", "merge alpha beta")
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("status=%s exit=%d, want precondition_unmet/30: %+v", r.Status, exit, r)
	}
	if code, _ := r.Errors[0]["code"].(string); code != "precondition_unmet.git.signing_key_unresolved" {
		t.Fatalf("refusal code=%q, want precondition_unmet.git.signing_key_unresolved: %+v", code, r.Errors[0])
	}
	if got := runGit(t, dir, "rev-parse", "HEAD"); got != head {
		t.Fatalf("the target ref moved despite the refusal: HEAD=%s want %s", got, head)
	}
}

// A dry run mints no commit, so the merge-commit signing check must not run: a
// dry run of a merge that WOULD mint a commit still reports would_merge at exit
// 0 and moves no ref, even in a repository that cannot sign.
func TestMerge_DryRunMintedCommitUnsignable_StillReportsWouldMerge(t *testing.T) {
	bin := buildCLI(t)
	dir := signingRepo(t)
	signedBranch(t, dir, "feature")
	commitFile(t, dir, "main.txt", "main\n", "diverge main") // would mint a merge commit
	breakSigningKey(t, dir)
	head := runGit(t, dir, "rev-parse", "HEAD")

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature", "--dry-run")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	if r.Data["would_merge"] != true {
		t.Fatalf("dry run did not report would_merge: %+v", r.Data)
	}
	if got := runGit(t, dir, "rev-parse", "HEAD"); got != head {
		t.Fatalf("a dry run moved the target ref: HEAD=%s want %s", got, head)
	}
}

// assertNoSigningKeyResolves fails the test unless dir cannot actually sign a
// commit, using the same probe the merge verb itself relies on. This makes the
// keyless carve-out tests below assert the fixture's own precondition instead
// of merely assuming it.
func assertNoSigningKeyResolves(t *testing.T, dir string) {
	t.Helper()
	available, detail, err := signing.NewProber(&git.Repo{Dir: dir}).Available(context.Background())
	if err != nil {
		t.Fatalf("probing whether %s can sign: %v", dir, err)
	}
	if available {
		t.Fatalf("fixture unexpectedly resolves a signing key in %s", dir)
	}
	if detail == "" {
		t.Fatalf("no-key probe reported no detail")
	}
}

// SC-B3: a fast-forwardable, already-verifying source must land even in a
// repository that cannot sign at all. This is structural, not incidental: per
// WillMintCommit, a single source the target can fast-forward to (FastForward
// != never) mints no merge commit, so the merge-commit signing check that
// follows the gate never runs; and per the gate itself, a range that already
// verifies is skipped as already_signed without ever probing for a key. Two
// independent carve-outs, not one, both have to hold for this to pass — a
// future change that hoists the signing probe earlier, or that makes the gate
// probe before checking allVerify, would break one of them silently. Do not
// delete this as "obviously true" from reading the code alone.
func TestMerge_KeylessFastForwardableAlreadyVerifying_Lands(t *testing.T) {
	bin := buildCLI(t)
	dir := signingRepo(t)
	tip := signedBranch(t, dir, "feature")
	breakSigningKey(t, dir)
	assertNoSigningKeyResolves(t, dir)

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	if len(r.Errors) != 0 {
		t.Fatalf("a keyless fast-forward triggered a signing refusal: %+v", r.Errors)
	}
	if record := gateRecord(t, r, "feature"); record["action"] != "already_signed" {
		t.Fatalf("gate action=%v, want already_signed: %+v", record["action"], record)
	}
	if got := runGit(t, dir, "rev-parse", "HEAD"); got != tip {
		t.Fatalf("HEAD did not fast-forward to the source tip: got %s want %s", got, tip)
	}
}

// The --fast-forward only form of the same carve-out: WillMintCommit treats
// FastForwardOnly the same as the default allow (only FastForwardNever or an
// octopus mints a commit), so this must succeed for the identical structural
// reason as the plain-merge case above.
func TestMerge_KeylessFastForwardOnlyAlreadyVerifying_Lands(t *testing.T) {
	bin := buildCLI(t)
	dir := signingRepo(t)
	tip := signedBranch(t, dir, "feature")
	breakSigningKey(t, dir)
	assertNoSigningKeyResolves(t, dir)

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature", "--fast-forward", "only")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	if len(r.Errors) != 0 {
		t.Fatalf("a keyless --fast-forward only merge triggered a signing refusal: %+v", r.Errors)
	}
	if record := gateRecord(t, r, "feature"); record["action"] != "already_signed" {
		t.Fatalf("gate action=%v, want already_signed: %+v", record["action"], record)
	}
	if got := runGit(t, dir, "rev-parse", "HEAD"); got != tip {
		t.Fatalf("HEAD did not fast-forward to the source tip: got %s want %s", got, tip)
	}
}
