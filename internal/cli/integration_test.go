// Integration tests exercise the built git-tools binary as a real
// subprocess against scratch git repositories, per the task's test
// strategy: resign/worktree/merge/rebase work, scans detect planted
// findings, hooks install, --help is complete, and exit codes follow
// clikit's taxonomy.
package cli_test

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestMain builds git-tools once (see buildCLI) and cleans up the shared
// binary's temp directory after the whole package's tests finish, since it
// lives outside any single test's t.TempDir().
func TestMain(m *testing.M) {
	code := m.Run()
	if cliBinDir != "" {
		os.RemoveAll(cliBinDir)
	}
	os.Exit(code)
}

// plantedAWSAccessKeyID is an AWS-access-key-id-shaped string that ScanSecrets
// and ScanPrivacy still flag: githooks carries an exact-match exemption for
// AWS's own reserved documentation placeholder, so a fixture proving real
// detection needs a different 20-character AKIA id.
const plantedAWSAccessKeyID = "AKIA" + "TESTKEY1234567Z9"

var (
	cliBinOnce sync.Once
	// cliBinDir is tracked separately from cliBinPath so TestMain still
	// removes the temp directory when the build itself fails.
	cliBinDir  string
	cliBinPath string
	cliBinErr  error
)

// buildCLI compiles git-tools once for the whole test binary run (guarded by
// cliBinOnce) and returns the path to the resulting executable. Every test
// case shares this one binary instead of paying for its own `go build`.
func buildCLI(t *testing.T) string {
	t.Helper()
	cliBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "git-tools-cli-test-")
		if err != nil {
			cliBinErr = err
			return
		}
		cliBinDir = dir
		bin := filepath.Join(dir, "git-tools")
		repoRoot, err := filepath.Abs("../..")
		if err != nil {
			cliBinErr = err
			return
		}
		cmd := exec.Command("go", "build", "-o", bin, "./cmd/git-tools")
		cmd.Dir = repoRoot
		if out, err := cmd.CombinedOutput(); err != nil {
			cliBinErr = fmt.Errorf("go build git-tools: %w\n%s", err, out)
			return
		}
		cliBinPath = bin
	})
	if cliBinErr != nil {
		t.Fatal(cliBinErr)
	}
	return cliBinPath
}

type wireResult struct {
	SchemaVersion int              `json:"schema_version"`
	Command       []string         `json:"command"`
	Status        string           `json:"status"`
	ExitCode      int              `json:"exit_code"`
	Data          map[string]any   `json:"data"`
	Errors        []map[string]any `json:"errors"`
	Caveats       []map[string]any `json:"caveats"`
}

// runCLI runs bin with args, decoding its one-line clikit JSON record.
func runCLI(t *testing.T, bin string, args ...string) (wireResult, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	out, _ := cmd.Output()
	exit := cmd.ProcessState.ExitCode()
	var r wireResult
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, out)
	}
	return r, exit
}

// runGit runs a git plumbing/porcelain command directly against dir, for
// scratch-repo setup that is not itself under test.
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// initRepo creates a scratch repo with one committed file on branch main,
// signing disabled and no signing key configured at all. Two fixtures layer
// a key over it, for verbs that sign:
//   - signingRepo — key plus commit.gpgsign, so this fixture's own commits
//     are signed too (merge, tag create, and the scan gate's create path).
//   - configureSigningKey — key only, leaving commits unsigned, for sign and
//     resign, whose whole point is turning an unsigned commit into a signed
//     one.
func initRepo(t *testing.T) string {
	t.Helper()
	// Isolate this fixture (and the CLI binary it drives) from the host's
	// global/system git config, most importantly core.hooksPath: without
	// this, every commit made here runs whatever hook the host has
	// configured globally, which can dominate the test's wall time. Set at
	// process level so it also reaches runCLI's subprocess.
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	runGit(t, dir, "config", "user.name", "Test User")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "commit.gpgsign", "false")
	// Isolate from the host's global gitignore (e.g. a blanket "*.env" rule)
	// so a planted fixture file lands under version control every time.
	runGit(t, dir, "config", "core.excludesfile", "")
	commitFile(t, dir, "base.txt", "base\n", "base")
	return dir
}

func commitFile(t *testing.T, dir, name, content, message string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", name)
	runGit(t, dir, "commit", "-q", "-m", message)
	return runGit(t, dir, "rev-parse", "HEAD")
}

func TestHelp_TopLevelIsComplete(t *testing.T) {
	bin := buildCLI(t)
	out, err := exec.Command(bin, "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("--help exited non-zero: %v\n%s", err, out)
	}
	text := string(out)
	for _, want := range []string{"sign", "resign", "worktree", "branch", "merge", "rebase", "scan", "hooks", "Examples:", "Usage:"} {
		if !strings.Contains(text, want) {
			t.Errorf("--help missing %q:\n%s", want, text)
		}
	}
}

func TestHelp_EverySubcommandHasHelp(t *testing.T) {
	bin := buildCLI(t)
	for _, args := range [][]string{
		{"sign", "--help"}, {"resign", "--help"},
		{"worktree", "--help"}, {"worktree", "add", "--help"}, {"worktree", "remove", "--help"}, {"worktree", "list", "--help"},
		{"branch", "--help"}, {"branch", "create", "--help"}, {"branch", "delete", "--help"}, {"branch", "list", "--help"},
		{"merge", "--help"}, {"rebase", "--help"},
		{"scan", "--help"}, {"scan", "secrets", "--help"}, {"scan", "lfs", "--help"}, {"scan", "privacy", "--help"}, {"scan", "all", "--help"},
		{"hooks", "--help"}, {"hooks", "install", "--help"},
	} {
		out, err := exec.Command(bin, args...).CombinedOutput()
		if err != nil {
			t.Errorf("%v --help exited non-zero: %v\n%s", args, err, out)
		}
		if !strings.Contains(string(out), "Usage:") {
			t.Errorf("%v --help missing Usage section:\n%s", args, out)
		}
	}
}

func TestUnknownSubcommand_IsUsageError(t *testing.T) {
	bin := buildCLI(t)
	r, exit := runCLI(t, bin, "frobnicate")
	if r.Status != "usage" || exit != 50 {
		t.Fatalf("status=%s exit=%d, want usage/50: %+v", r.Status, exit, r)
	}
}

func TestUnknownFlag_IsUsageError(t *testing.T) {
	bin := buildCLI(t)
	r, exit := runCLI(t, bin, "sign", "--this-flag-does-not-exist")
	if r.Status != "usage" || exit != 50 {
		t.Fatalf("status=%s exit=%d, want usage/50: %+v", r.Status, exit, r)
	}
}

func TestRequireRepo_NotAGitTree_IsNotFound(t *testing.T) {
	bin := buildCLI(t)
	r, exit := runCLI(t, bin, "--repo", t.TempDir(), "worktree", "list")
	if r.Status != "not_found" || exit != 40 {
		t.Fatalf("status=%s exit=%d, want not_found/40: %+v", r.Status, exit, r)
	}
}

func TestSign_ResignsTipCommitWithIdenticalTree(t *testing.T) {
	bin := buildCLI(t)
	// sign turns an unsigned tip into a signed one, so this fixture's own
	// commits must stay unsigned (unlike signingRepo) while still carrying a
	// key that resolves under isolation from the host's own config, for
	// `sign` itself to sign with.
	dir := initRepo(t)
	configureSigningKey(t, dir)
	oldHead := commitFile(t, dir, "next.txt", "next\n", "next")
	oldTree := runGit(t, dir, "rev-parse", "HEAD^{tree}")

	r, exit := runCLI(t, bin, "--repo", dir, "sign", "HEAD")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	newHead, _ := r.Data["new_head"].(string)
	if newHead == "" || newHead == oldHead {
		t.Fatalf("sign did not produce a new head: %+v", r.Data)
	}
	newTree := runGit(t, dir, "rev-parse", newHead+"^{tree}")
	if newTree != oldTree {
		t.Fatalf("sign changed the tree: old %s new %s", oldTree, newTree)
	}
	if got := runGit(t, dir, "rev-parse", "HEAD"); got != newHead {
		t.Fatalf("HEAD not moved to resigned commit: got %s want %s", got, newHead)
	}
}

func TestSign_RootCommit_IsUsageError(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	r, exit := runCLI(t, bin, "--repo", dir, "sign", "HEAD")
	if r.Status != "usage" || exit != 50 {
		t.Fatalf("status=%s exit=%d, want usage/50 (root commit has no parent to derive a base from): %+v", r.Status, exit, r)
	}
}

func TestResign_RangeAcrossBase(t *testing.T) {
	bin := buildCLI(t)
	// resign turns unsigned commits into signed ones, so this fixture's own
	// commits must stay unsigned (unlike signingRepo) while still carrying a
	// key that resolves under isolation from the host's own config, for
	// `resign` itself to sign with.
	dir := initRepo(t)
	configureSigningKey(t, dir)
	base := runGit(t, dir, "rev-parse", "HEAD")
	commitFile(t, dir, "a.txt", "a\n", "a")
	oldHead := commitFile(t, dir, "b.txt", "b\n", "b")

	r, exit := runCLI(t, bin, "--repo", dir, "resign", "--base", base, "HEAD")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	newHead, _ := r.Data["new_head"].(string)
	if newHead == "" || newHead == oldHead {
		t.Fatalf("resign did not produce a new head: %+v", r.Data)
	}
	if got := runGit(t, dir, "rev-parse", "HEAD^{tree}"); got != runGit(t, dir, "rev-parse", oldHead+"^{tree}") {
		t.Fatalf("resign changed the tip tree")
	}
}

func TestResign_UnresolvableBase_IsUsageError(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	r, exit := runCLI(t, bin, "--repo", dir, "resign", "--base", "does-not-exist", "HEAD")
	if r.Status != "usage" || exit != 50 {
		t.Fatalf("status=%s exit=%d, want usage/50: %+v", r.Status, exit, r)
	}
}

func TestBranch_CreateAndDelete(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	head := runGit(t, dir, "rev-parse", "HEAD")

	r, exit := runCLI(t, bin, "--repo", dir, "branch", "create", "feature/x", "HEAD")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("branch create: status=%s exit=%d: %+v", r.Status, exit, r)
	}
	runGit(t, dir, "show-ref", "--verify", "refs/heads/feature/x")

	// feature/x carries no commit beyond main, so --landing-target main lets
	// the reachability guard resolve and pass -- the delete itself is what
	// this test exercises.
	r, exit = runCLI(t, bin, "--repo", dir, "branch", "delete", "feature/x", head, "--landing-target", "main")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("branch delete: status=%s exit=%d: %+v", r.Status, exit, r)
	}
	if err := exec.Command("git", "-C", dir, "show-ref", "--verify", "refs/heads/feature/x").Run(); err == nil {
		t.Fatal("branch still exists after delete")
	}
}

func TestBranch_List(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	head := runGit(t, dir, "rev-parse", "HEAD")
	runGit(t, dir, "branch", "feature/x")

	r, exit := runCLI(t, bin, "--repo", dir, "branch", "list")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("branch list: status=%s exit=%d: %+v", r.Status, exit, r)
	}
	if count, _ := r.Data["count"].(float64); count != 2 {
		t.Fatalf("count=%v, want 2: %+v", r.Data["count"], r.Data)
	}
	names := map[string]bool{}
	for _, e := range r.Data["branches"].([]any) {
		m := e.(map[string]any)
		names[m["name"].(string)] = true
		if got, _ := m["head"].(string); got != head {
			t.Fatalf("branch %v head=%q, want %q", m["name"], got, head)
		}
	}
	if !names["main"] || !names["feature/x"] {
		t.Fatalf("branch list did not report both branches: %+v", r.Data["branches"])
	}
}

func TestBranch_DeleteStaleExpectedHead_IsConflict(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	runGit(t, dir, "branch", "feature/x")

	// feature/x carries no commit beyond main, so the reachability guard
	// passes and the stale expected-head is what fails the compare-and-swap.
	r, exit := runCLI(t, bin, "--repo", dir, "branch", "delete", "feature/x", "0000000000000000000000000000000000000000", "--landing-target", "main")
	if r.Status != "conflict" || exit != 41 {
		t.Fatalf("status=%s exit=%d, want conflict/41: %+v", r.Status, exit, r)
	}
}

func TestWorktree_AddListRemove(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	wtPath := filepath.Join(t.TempDir(), "review")

	r, exit := runCLI(t, bin, "--repo", dir, "worktree", "add", wtPath, "HEAD", "--branch", "review")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("worktree add: status=%s exit=%d: %+v", r.Status, exit, r)
	}
	if _, err := os.Stat(filepath.Join(wtPath, "base.txt")); err != nil {
		t.Fatalf("worktree add did not check out files: %v", err)
	}

	r, exit = runCLI(t, bin, "--repo", dir, "worktree", "list")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("worktree list: status=%s exit=%d: %+v", r.Status, exit, r)
	}
	if count, _ := r.Data["count"].(float64); count != 2 {
		t.Fatalf("worktree list count=%v, want 2: %+v", r.Data["count"], r.Data)
	}

	// worktree remove now proves the work is safely landed before removing.
	// review was created at HEAD and never diverged, so pointing its upstream
	// at main gives the no-work-loss guard a resolvable landing target it clears
	// cleanly -- all from local refs, no network.
	runGit(t, dir, "branch", "--set-upstream-to=main", "review")

	r, exit = runCLI(t, bin, "--repo", dir, "worktree", "remove", wtPath)
	if r.Status != "success" || exit != 0 {
		t.Fatalf("worktree remove: status=%s exit=%d: %+v", r.Status, exit, r)
	}
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Fatalf("worktree remove left %s behind", wtPath)
	}
}

// worktree remove no longer declares --force (SC-C1/D3): the flag is
// unknown to cobra now, so it must fail as a usage error before Cleanup
// runs at all, and the worktree it would have targeted stays untouched.
func TestWorktreeRemove_ForceFlag_IsUsageError(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	wtPath := filepath.Join(t.TempDir(), "review")

	r, exit := runCLI(t, bin, "--repo", dir, "worktree", "add", wtPath, "HEAD", "--branch", "review")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("worktree add: status=%s exit=%d: %+v", r.Status, exit, r)
	}

	r, exit = runCLI(t, bin, "--repo", dir, "worktree", "remove", wtPath, "--force")
	if r.Status != "usage" || exit != 50 {
		t.Fatalf("worktree remove --force: status=%s exit=%d, want usage/50: %+v", r.Status, exit, r)
	}
	if _, err := os.Stat(wtPath); err != nil {
		t.Fatalf("an unknown --force flag disturbed the worktree %s: %v", wtPath, err)
	}
}

// TestWorktreeRemove_UnmergedWork_RefusedWithExit30 exercises the
// unmerged-work refusal at the CLI boundary: a worktree carrying a commit
// unreachable from the resolved landing target is refused with the stable
// precondition_unmet exit code, and the worktree is never touched. No flag
// exists anymore that could override this (SC-C2).
func TestWorktreeRemove_UnmergedWork_RefusedWithExit30(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	wtPath := filepath.Join(t.TempDir(), "feature")
	runGit(t, dir, "worktree", "add", "-b", "feature", wtPath, "main")
	commitFile(t, wtPath, "feature.txt", "feature\n", "feature work")
	runGit(t, dir, "branch", "--set-upstream-to=main", "feature")

	r, exit := runCLI(t, bin, "--repo", dir, "worktree", "remove", wtPath)
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("status=%s exit=%d, want precondition_unmet/30: %+v", r.Status, exit, r)
	}
	if _, err := os.Stat(wtPath); err != nil {
		t.Fatalf("a refused removal disturbed the worktree %s: %v", wtPath, err)
	}
	if len(r.Errors) == 0 {
		t.Fatal("precondition_unmet result carries no errors for the unmerged commit")
	}
}

// TestWorktreeRemove_DirtyTree_RefusedAndStaysPresent covers the second half
// of SC-C2: a worktree whose branch is fully landed but whose tree carries
// an uncommitted change is refused (by git itself, since Force is never
// passed through) and stays present.
func TestWorktreeRemove_DirtyTree_RefusedAndStaysPresent(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	wtPath := filepath.Join(t.TempDir(), "review")
	runGit(t, dir, "worktree", "add", "-b", "review", wtPath, "main")
	runGit(t, dir, "branch", "--set-upstream-to=main", "review")
	if err := os.WriteFile(filepath.Join(wtPath, "dirty.txt"), []byte("uncommitted\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r, exit := runCLI(t, bin, "--repo", dir, "worktree", "remove", wtPath)
	if r.Status == "success" || exit == 0 {
		t.Fatalf("a dirty worktree must not be removed: status=%s exit=%d: %+v", r.Status, exit, r)
	}
	if _, err := os.Stat(wtPath); err != nil {
		t.Fatalf("a refused removal disturbed the worktree %s: %v", wtPath, err)
	}
}

// TestWorktreeRemove_HelpDoesNotMentionForce guards the advice text: once
// --force is removed, nothing in the verb's own help should still reference
// it as an available override.
func TestWorktreeRemove_HelpDoesNotMentionForce(t *testing.T) {
	bin := buildCLI(t)
	out, err := exec.Command(bin, "worktree", "remove", "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("worktree remove --help exited non-zero: %v\n%s", err, out)
	}
	if strings.Contains(string(out), "--force") {
		t.Fatalf("worktree remove --help still mentions --force:\n%s", out)
	}
}

// TestWorktreeAdd_RelativePathIsCwdIndependent covers the case where the
// process's current directory differs from the --repo working tree: the
// <path> argument is git's concept of "relative to the repository", so
// self-verification must resolve it the same way, not against the
// process's cwd.
func TestWorktreeAdd_RelativePathIsCwdIndependent(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	elsewhere := t.TempDir()

	cmd := exec.Command(bin, "worktree", "add", "review", "HEAD", "--repo", dir, "--branch", "review")
	cmd.Dir = elsewhere
	out, _ := cmd.Output()
	var r wireResult
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, out)
	}
	exit := cmd.ProcessState.ExitCode()
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	if _, err := os.Stat(filepath.Join(dir, "review", "base.txt")); err != nil {
		t.Fatalf("worktree add did not check out files: %v", err)
	}
}

// The merge verb runs the signing gate on every merge, so its tests use the
// signing fixture (see signingRepo): the gate finds every commit in range
// already signed and leaves the branch tips these assertions name intact.
func TestMerge_FastForward(t *testing.T) {
	bin := buildCLI(t)
	dir := signingRepo(t)
	runGit(t, dir, "branch", "feature")
	runGit(t, dir, "checkout", "-q", "feature")
	tip := commitFile(t, dir, "feature.txt", "feature\n", "feature work")
	runGit(t, dir, "checkout", "-q", "main")

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	if got := runGit(t, dir, "rev-parse", "HEAD"); got != tip {
		t.Fatalf("main did not fast-forward to feature tip: got %s want %s", got, tip)
	}
}

func TestMerge_ConflictingContent_IsConflict(t *testing.T) {
	bin := buildCLI(t)
	dir := signingRepo(t)
	runGit(t, dir, "branch", "feature")
	commitFile(t, dir, "base.txt", "main change\n", "main changes base")
	runGit(t, dir, "checkout", "-q", "feature")
	commitFile(t, dir, "base.txt", "feature change\n", "feature changes base")
	runGit(t, dir, "checkout", "-q", "main")

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature", "--message", "merge feature")
	if r.Status != "conflict" || exit != 41 {
		t.Fatalf("status=%s exit=%d, want conflict/41: %+v", r.Status, exit, r)
	}
}

func TestRebase_ReplaysCommitsOntoUpstream(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	runGit(t, dir, "branch", "feature")
	commitFile(t, dir, "main-only.txt", "main\n", "main advances")
	runGit(t, dir, "checkout", "-q", "feature")
	commitFile(t, dir, "feature-only.txt", "feature\n", "feature work")

	r, exit := runCLI(t, bin, "--repo", dir, "rebase", "main")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	mainHead := runGit(t, dir, "rev-parse", "main")
	featureParent := runGit(t, dir, "rev-parse", "HEAD^")
	if featureParent != mainHead {
		t.Fatalf("feature was not replayed onto main: parent=%s main=%s", featureParent, mainHead)
	}
}

func TestScanSecrets_DetectsPlantedAWSKey(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	commitFile(t, dir, "config.env", "AWS_KEY="+plantedAWSAccessKeyID+"\n", "add config")

	r, exit := runCLI(t, bin, "--repo", dir, "scan", "secrets")
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("status=%s exit=%d, want precondition_unmet/30: %+v", r.Status, exit, r)
	}
	if len(r.Errors) == 0 {
		t.Fatal("precondition_unmet result carries no errors for the planted secret")
	}
}

func TestScanSecrets_CleanTreeSucceeds(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	r, exit := runCLI(t, bin, "--repo", dir, "scan", "secrets")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
}

// TestScanSecrets_SecretScanExemptConfigExemptsOnlyNamedPath covers `scan
// secrets`' own githooks.ScanSecrets call — a third call site that builds its
// secret-exempt ruleset itself, sharing neither the scanTree that
// merge/push/rebase use nor `scan privacy`'s ScanPrivacy call, so no test of
// either proves this verb passes the exemption at all. The exact path named
// under secret_scan_exempt passes; an identical secret-shaped file at another
// path still fails in the same repository, so the exemption is not a blanket
// disable of the verb.
func TestScanSecrets_SecretScanExemptConfigExemptsOnlyNamedPath(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	writeConfig(t, dir, "secret_scan_exempt:\n  - fixtures/sample.env\n")
	commitNestedFile(t, dir, "fixtures/sample.env", secretFixtureSecret, "add fixture sample")

	// Run with the process's cwd in dir rather than pointing --repo at it:
	// loadConfigFile auto-discovers git-tools.yaml from the cwd only (see
	// TestScanGate_PrivacyMarkerExemptConfigNotLoadedFromRepoFlagTarget).
	r, exit := runCLIIn(t, bin, dir, "scan", "secrets")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}

	commitNestedFile(t, dir, "config/prod.env", secretFixtureSecret, "add prod config")
	r, exit = runCLIIn(t, bin, dir, "scan", "secrets")
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("out-of-scope secret: status=%s exit=%d, want precondition_unmet/30: %+v", r.Status, exit, r)
	}
	if got, _ := r.Data["secrets_found"].(float64); int(got) != 1 {
		t.Fatalf("secrets_found=%v, want 1 (config/prod.env alone) — fixtures/sample.env was flagged too, so its secret exemption did not hold: %+v", r.Data["secrets_found"], r.Data)
	}
	for _, e := range r.Errors {
		context, _ := e["context"].(map[string]any)
		if path, _ := context["path"].(string); path == "fixtures/sample.env" {
			t.Fatalf("exempted path is named by a reported error: %+v", e)
		}
	}
}

// TestScanSecrets_MalformedSecretScanExemptIsUsageError is the silent-disable
// guard at `scan secrets`' own call site: a malformed glob must refuse the
// invocation by name rather than reach fsx.ClassifyPath, where an
// uncompilable pattern is demoted to an always-match and would exempt the
// whole repository from secret detection. githooks v0.4.0 rejects such a
// pattern too, so the failure is loud either way — but only this check makes
// it the caller's usage error naming the bad config entry instead of an
// opaque internal scan failure.
func TestScanSecrets_MalformedSecretScanExemptIsUsageError(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	writeConfig(t, dir, "secret_scan_exempt:\n  - \"fixtures[\"\n")
	commitNestedFile(t, dir, "config/prod.env", secretFixtureSecret, "add prod config")

	r, exit := runCLIIn(t, bin, dir, "scan", "secrets")
	if r.Status != "usage" || exit != 50 {
		t.Fatalf("status=%s exit=%d, want usage/50: %+v", r.Status, exit, r)
	}
	if len(r.Errors) == 0 || !strings.Contains(r.Errors[0]["message"].(string), "fixtures[") {
		t.Fatalf("usage error does not name the malformed pattern: %+v", r.Errors)
	}
}

func TestScanLFS_DetectsOversizedRawBinary(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	payload := append([]byte{0x00, 0x01, 0x02, 0x03}, make([]byte, 64)...)
	if err := os.WriteFile(filepath.Join(dir, "blob.bin"), payload, 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "blob.bin")
	runGit(t, dir, "commit", "-q", "-m", "add blob")

	r, exit := runCLI(t, bin, "--repo", dir, "scan", "lfs", "--max-binary-bytes", "16")
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("status=%s exit=%d, want precondition_unmet/30: %+v", r.Status, exit, r)
	}
}

func TestScanPrivacy_DetectsForbiddenMarker(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	content := "---\nprivacy: internal\n---\n\nbody\n"
	commitFile(t, dir, "doc.md", content, "add doc")

	r, exit := runCLI(t, bin, "--repo", dir, "scan", "privacy", "--privacy-tier", "public")
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("status=%s exit=%d, want precondition_unmet/30: %+v", r.Status, exit, r)
	}
}

// employeeEmailConfig turns the public tier's optional employee-email check on
// for a placeholder domain, with one allowlisted role address. The domain is
// deliberately a documentation placeholder, never a real organization's: which
// domains identify an org's own people is per-repo config, so a test proving
// the mechanism needs a fixture value, not a live one.
const employeeEmailConfig = "employee_email_domains:\n  - example.com\nemployee_email_allowlist:\n  - support@example.com\n"

// TestScanPrivacy_InternalEmailWarnsWithoutStrict covers the public tier's
// employee-email check as a repo configures it through git-tools.yaml, and its
// allowlist: the flagged individual address warns (and fails under --strict)
// while the allowlisted role address at the same domain does not.
func TestScanPrivacy_InternalEmailWarnsWithoutStrict(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	commitFile(t, dir, "git-tools.yaml", employeeEmailConfig, "configure the employee-email check")
	commitFile(t, dir, "doc.md", "contact person@example.com, not support@example.com\n", "add doc")

	r, exit := runCLI(t, bin, "--repo", dir, "scan", "privacy", "--privacy-tier", "public")
	if r.Status != "caveats" || exit != 10 {
		t.Fatalf("status=%s exit=%d, want caveats/10: %+v", r.Status, exit, r)
	}
	if len(r.Caveats) != 1 {
		t.Errorf("caveats = %d, want exactly 1 (the allowlisted role address must not warn): %+v", len(r.Caveats), r.Caveats)
	}

	r, exit = runCLI(t, bin, "--repo", dir, "scan", "privacy", "--privacy-tier", "public", "--strict")
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("--strict: status=%s exit=%d, want precondition_unmet/30: %+v", r.Status, exit, r)
	}
}

// TestScanPrivacy_EmployeeEmailCheckOffWithoutConfig pins the check off by
// default. git-tools holds no organization's mail domains in its own source —
// it ships publicly and serves any repo — so an address at a domain no
// git-tools.yaml named is not an internal identifier and the scan is clean.
func TestScanPrivacy_EmployeeEmailCheckOffWithoutConfig(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	commitFile(t, dir, "doc.md", "contact person@example.com for details\n", "add doc")

	r, exit := runCLI(t, bin, "--repo", dir, "scan", "privacy", "--privacy-tier", "public")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0 (no configured domain means no employee-email check): %+v", r.Status, exit, r)
	}
}

func TestScanPrivacy_InvalidTier_IsUsageError(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	r, exit := runCLI(t, bin, "--repo", dir, "scan", "privacy", "--privacy-tier", "nonsense")
	if r.Status != "usage" || exit != 50 {
		t.Fatalf("status=%s exit=%d, want usage/50: %+v", r.Status, exit, r)
	}
}

// TestScanPrivacy_RetiredTierWireValues_AreUsageErrors proves the tier rename
// to confidential/private (from the retired datadog/personal wire values) is
// a hard cutover: the old values fall through tier.Known()'s ordinary usage-
// error path, not a special-cased backward-compat warning.
func TestScanPrivacy_RetiredTierWireValues_AreUsageErrors(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	for _, tier := range []string{"datadog", "personal"} {
		r, exit := runCLI(t, bin, "--repo", dir, "scan", "privacy", "--privacy-tier", tier)
		if r.Status != "usage" || exit != 50 {
			t.Errorf("--privacy-tier %s: status=%s exit=%d, want usage/50: %+v", tier, r.Status, exit, r)
		}
	}
}

// TestScanPrivacy_SecretScanExemptConfigExemptsOnlySecretFinding covers `scan
// privacy`'s own githooks.ScanPrivacy call — which builds SecretExemptRules
// itself rather than through the scanTree that merge/push/rebase share, so
// TestScanGate_SecretScanExemptStillCatchesMarkerAndInternalIdentifier proves
// nothing about this call site — using the same all-three-checks-at-once
// fixture: naming its path under secret_scan_exempt must silence only its
// secret-pattern finding, leaving its forbidden-marker and internal-identifier
// findings intact in the same `scan privacy --strict` run.
func TestScanPrivacy_SecretScanExemptConfigExemptsOnlySecretFinding(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	writeConfig(t, dir, "secret_scan_exempt:\n  - fixtures/sample.md\n")
	commitNestedFile(t, dir, "fixtures/sample.md", secretExemptMarkerFixture, "add fixture sample")

	// Run with the process's cwd in dir rather than pointing --repo at it:
	// loadConfigFile auto-discovers git-tools.yaml from the cwd only (see
	// TestScanGate_PrivacyMarkerExemptConfigNotLoadedFromRepoFlagTarget).
	r, exit := runCLIIn(t, bin, dir, "scan", "privacy", "--privacy-tier", "public", "--strict")
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("status=%s exit=%d, want precondition_unmet/30: %+v", r.Status, exit, r)
	}

	var sawMarker, sawInternalID, sawSecret bool
	for _, e := range r.Errors {
		context, _ := e["context"].(map[string]any)
		if path, _ := context["path"].(string); path != "fixtures/sample.md" {
			continue
		}
		switch context["rule"] {
		case "forbidden_marker":
			sawMarker = true
		case "internal_identifier":
			sawInternalID = true
		case "github_token":
			sawSecret = true
		}
	}
	if !sawMarker {
		t.Fatalf("secret_scan_exempt also suppressed the frontmatter-marker check: %+v", r.Errors)
	}
	if !sawInternalID {
		t.Fatalf("secret_scan_exempt also suppressed the internal-identifier check: %+v", r.Errors)
	}
	if sawSecret {
		t.Fatalf("secret_scan_exempt did not suppress the secret-pattern check: %+v", r.Errors)
	}
}

// TestScanPrivacy_PrivacyMarkerExemptConfigExemptsOnlyNamedPath covers `scan
// privacy`'s own githooks.ScanPrivacy call, which builds MarkerExemptRules
// itself rather than through the scanTree that merge/push/rebase share — so
// nothing about that shared path proves this one passes the exemption at
// all. A marker-bearing file under the configured privacy_marker_exempt path
// passes; an identical file outside it still fails in the same repository.
func TestScanPrivacy_PrivacyMarkerExemptConfigExemptsOnlyNamedPath(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	writeConfig(t, dir, "privacy_marker_exempt:\n  - fixtures\n")
	commitNestedFile(t, dir, "fixtures/sample.md", markerFrontmatter, "add fixture sample")

	// Run with the process's cwd in dir rather than pointing --repo at it:
	// loadConfigFile auto-discovers git-tools.yaml from the cwd only (see
	// TestScanGate_PrivacyMarkerExemptConfigNotLoadedFromRepoFlagTarget).
	r, exit := runCLIIn(t, bin, dir, "scan", "privacy", "--privacy-tier", "public")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}

	commitNestedFile(t, dir, "docs/real.md", markerFrontmatter, "add real doc")
	r, exit = runCLIIn(t, bin, dir, "scan", "privacy", "--privacy-tier", "public")
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("out-of-scope marker: status=%s exit=%d, want precondition_unmet/30: %+v", r.Status, exit, r)
	}
	if got, _ := r.Data["privacy_violations_found"].(float64); int(got) != markerFrontmatterFindings {
		t.Fatalf("privacy_violations_found=%v, want %d (docs/real.md alone) — fixtures/sample.md was flagged too, so its marker exemption did not hold: %+v", r.Data["privacy_violations_found"], markerFrontmatterFindings, r.Data)
	}
	for _, e := range r.Errors {
		context, _ := e["context"].(map[string]any)
		if path, _ := context["path"].(string); path == "fixtures/sample.md" {
			t.Fatalf("exempted path is named by a reported error: %+v", e)
		}
	}
}

// TestScanPrivacy_CodeSuffixIsMarkerExemptByDefault covers check_privacy.py's
// own CODE_SUFFIXES behavior: a source file's own literal "privacy:"/
// "owner:" text (a scanner's own pattern definition, a fixture string) is
// marker-exempt in every repo by default, with no privacy_marker_exempt
// config needed. The identical text committed at an otherwise-plain path
// (an .md file, never marker-exempt) still fails, proving the exemption is
// keyed on the code suffix, not on some other accident of this fixture.
func TestScanPrivacy_CodeSuffixIsMarkerExemptByDefault(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	commitFile(t, dir, "fixture.py", markerFrontmatter, "add code fixture")

	r, exit := runCLI(t, bin, "--repo", dir, "scan", "privacy", "--privacy-tier", "public")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0 (a .py file's own marker text is exempt by default): %+v", r.Status, exit, r)
	}

	commitFile(t, dir, "fixture.md", markerFrontmatter, "add non-code fixture")
	r, exit = runCLI(t, bin, "--repo", dir, "scan", "privacy", "--privacy-tier", "public")
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("status=%s exit=%d, want precondition_unmet/30 (identical text in an .md file is not exempt): %+v", r.Status, exit, r)
	}
	if got, _ := r.Data["privacy_violations_found"].(float64); int(got) != markerFrontmatterFindings {
		t.Fatalf("privacy_violations_found=%v, want %d (fixture.md alone) — fixture.py was flagged too, so the code-suffix exemption did not hold: %+v", r.Data["privacy_violations_found"], markerFrontmatterFindings, r.Data)
	}
}

// TestScanPrivacy_MalformedPrivacyMarkerExemptIsUsageError is the
// silent-disable guard at `scan privacy`'s own call site: a malformed glob
// must refuse the invocation by name, not reach fsx.ClassifyPath, where an
// uncompilable pattern is demoted to an always-match and would exempt the
// whole repository from the marker check. Without the validation the planted
// docs/real.md marker goes unreported and this command exits success/0.
func TestScanPrivacy_MalformedPrivacyMarkerExemptIsUsageError(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	writeConfig(t, dir, "privacy_marker_exempt:\n  - \"fixtures[\"\n")
	commitNestedFile(t, dir, "docs/real.md", markerFrontmatter, "add real doc")

	r, exit := runCLIIn(t, bin, dir, "scan", "privacy", "--privacy-tier", "public")
	if r.Status != "usage" || exit != 50 {
		t.Fatalf("status=%s exit=%d, want usage/50: %+v", r.Status, exit, r)
	}
	if len(r.Errors) == 0 || !strings.Contains(r.Errors[0]["message"].(string), "fixtures[") {
		t.Fatalf("usage error does not name the malformed pattern: %+v", r.Errors)
	}
}

// TestScanAll_MalformedPrivacyMarkerExemptIsUsageError is the same guard at
// `scan all`'s call site. Its scan is scanTree, the one merge/push/rebase
// share, but its validation is its own statement: delete that statement and
// only this test fails — `scan all` would exit success/0 on the planted
// docs/real.md marker while every gate test stayed green.
func TestScanAll_MalformedPrivacyMarkerExemptIsUsageError(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	writeConfig(t, dir, "privacy_marker_exempt:\n  - \"fixtures[\"\n")
	commitNestedFile(t, dir, "docs/real.md", markerFrontmatter, "add real doc")

	r, exit := runCLIIn(t, bin, dir, "scan", "all", "--privacy-tier", "public")
	if r.Status != "usage" || exit != 50 {
		t.Fatalf("status=%s exit=%d, want usage/50: %+v", r.Status, exit, r)
	}
	if len(r.Errors) == 0 || !strings.Contains(r.Errors[0]["message"].(string), "fixtures[") {
		t.Fatalf("usage error does not name the malformed pattern: %+v", r.Errors)
	}
}

func TestScanAll_CombinesEveryScanner(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	commitFile(t, dir, "config.env", "AWS_KEY="+plantedAWSAccessKeyID+"\n", "add config")

	r, exit := runCLI(t, bin, "--repo", dir, "scan", "all", "--staged")
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("status=%s exit=%d, want precondition_unmet/30: %+v", r.Status, exit, r)
	}
	if got, _ := r.Data["secrets_found"].(float64); got != 1 {
		t.Fatalf("secrets_found=%v, want 1: %+v", r.Data["secrets_found"], r.Data)
	}
}

func TestHooksInstall_WritesScriptAndSetsHooksPath(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)

	r, exit := runCLI(t, bin, "--repo", dir, "hooks", "install", "--hook", "pre-commit")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	scriptPath := filepath.Join(dir, ".githooks", "pre-commit")
	info, err := os.Stat(scriptPath)
	if err != nil {
		t.Fatalf("hook script not written: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Fatal("hook script is not executable")
	}
	if got := runGit(t, dir, "config", "core.hooksPath"); got != ".githooks" {
		t.Fatalf("core.hooksPath=%q, want .githooks", got)
	}

	// The hook itself fires: staging a planted secret and running it as
	// pre-commit does through `git commit` should refuse the commit.
	if err := os.WriteFile(filepath.Join(dir, "secret.env"), []byte("AWS_KEY="+"AKIA"+"IOSFODNN7"+"EXAMPLE"+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "secret.env")
	commit := exec.Command("git", "commit", "-q", "-m", "add secret")
	commit.Dir = dir
	if out, err := commit.CombinedOutput(); err == nil {
		t.Fatalf("commit with a planted secret should have been blocked by the installed hook:\n%s", out)
	}
}

// TestHooksInstall_RetiredTierWireValues_AreUsageErrors covers the other half
// of the tier cutover: install embeds the tier verbatim in the script it
// writes, so a retired value must be refused here rather than baked into a
// hook that then fails every commit. Nothing is installed on refusal.
func TestHooksInstall_RetiredTierWireValues_AreUsageErrors(t *testing.T) {
	bin := buildCLI(t)
	for _, tier := range []string{"datadog", "personal", "nonsense"} {
		dir := initRepo(t)
		r, exit := runCLI(t, bin, "--repo", dir, "hooks", "install", "--privacy-tier", tier)
		if r.Status != "usage" || exit != 50 {
			t.Errorf("--privacy-tier %s: status=%s exit=%d, want usage/50: %+v", tier, r.Status, exit, r)
		}
		if _, err := os.Stat(filepath.Join(dir, ".githooks", "pre-commit")); !os.IsNotExist(err) {
			t.Errorf("--privacy-tier %s: a hook was installed despite the usage error (stat err = %v)", tier, err)
		}
	}
}

// TestAbandonmentRoute_MergedBranch_SucceedsInTwoActs and
// TestAbandonmentRoute_UnmergedBranch_RefusedAtBothActs are R10's evidence:
// cluster C (SC-C6, this abandonment route) lands only because SC-C7 shipped
// branch delete alongside it, so the route these two acts complete now
// exists. D3 keeps it a two-act, no-single-call route: worktree remove and
// branch delete are separate deliberate invocations, and neither carries a
// flag that discards work the other's guard would refuse.
//
// The two acts run worktree remove before branch delete, in that order: once
// a linked worktree's branch ref is gone, `git worktree list` still reports
// the worktree as checked out on the deleted ref (the worktree
// administrative files cache the branch name), so a later worktree remove's
// own reachability check fails trying to resolve a ref that no longer
// exists. Removing the worktree first, while the branch ref still resolves,
// avoids that dead end -- the only order in which both acts land cleanly.
func TestAbandonmentRoute_MergedBranch_SucceedsInTwoActs(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	wtPath := filepath.Join(t.TempDir(), "feature")
	runGit(t, dir, "worktree", "add", "-b", "feature", wtPath, "main")
	head := runGit(t, dir, "rev-parse", "feature")

	// Act 1: remove the worktree. feature carries no commit beyond main, so
	// the no-work-loss guard clears and the worktree goes.
	r, exit := runCLI(t, bin, "--repo", dir, "worktree", "remove", wtPath, "--landing-target", "main")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("act 1 (worktree remove): status=%s exit=%d: %+v", r.Status, exit, r)
	}
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Fatalf("act 1 left the worktree behind: %s", wtPath)
	}

	// Act 2: delete the now-unchecked-out branch. Its own compare-and-swap
	// guard runs the identical no-work-loss check.
	r, exit = runCLI(t, bin, "--repo", dir, "branch", "delete", "feature", head, "--landing-target", "main")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("act 2 (branch delete): status=%s exit=%d: %+v", r.Status, exit, r)
	}
	if err := exec.Command("git", "-C", dir, "show-ref", "--verify", "refs/heads/feature").Run(); err == nil {
		t.Fatal("act 2 left the branch ref behind")
	}

	// The backup ref from act 2 makes the abandonment recoverable (SC-C6): it
	// must exist and still resolve to the deleted branch's old head.
	backupRef, _ := r.Data["backup_ref"].(string)
	if backupRef == "" {
		t.Fatalf("branch delete result carries no backup_ref: %+v", r.Data)
	}
	if got := runGit(t, dir, "rev-parse", backupRef); got != head {
		t.Fatalf("backup ref %s resolves to %s, want the deleted branch's old head %s", backupRef, got, head)
	}
}

// TestAbandonmentRoute_UnmergedBranch_RefusedAtBothActs proves D3 holds for
// the abandonment route itself: a branch that still carries committed work
// unreachable from its landing target cannot be abandoned through either
// act. Order does not matter here -- unlike the merged route, neither act
// depends on the other succeeding first -- so both are exercised, and each
// leaves its target exactly as it found it. Together with the merged-route
// test above, this enumerates every verb that could discard the worktree or
// the branch (worktree remove, branch delete) and confirms each still
// refuses, and that neither advertises a flag that would let it.
func TestAbandonmentRoute_UnmergedBranch_RefusedAtBothActs(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	wtPath := filepath.Join(t.TempDir(), "feature")
	runGit(t, dir, "worktree", "add", "-b", "feature", wtPath, "main")
	commitFile(t, wtPath, "feature.txt", "feature\n", "feature work")
	head := runGit(t, dir, "rev-parse", "feature")

	for _, help := range [][]string{{"branch", "delete", "--help"}, {"worktree", "remove", "--help"}} {
		out, err := exec.Command(bin, help...).CombinedOutput()
		if err != nil {
			t.Fatalf("%v exited non-zero: %v\n%s", help, err, out)
		}
		if strings.Contains(string(out), "--force") {
			t.Fatalf("%v still advertises a --force override: no verb may bypass the abandonment refusal", help)
		}
	}

	// branch delete: refused, ref unmoved.
	r, exit := runCLI(t, bin, "--repo", dir, "branch", "delete", "feature", head, "--landing-target", "main")
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("branch delete: status=%s exit=%d, want precondition_unmet/30: %+v", r.Status, exit, r)
	}
	if got := runGit(t, dir, "rev-parse", "feature"); got != head {
		t.Fatalf("a refused branch delete moved the ref: got %s want %s", got, head)
	}

	// worktree remove: refused, worktree present.
	r, exit = runCLI(t, bin, "--repo", dir, "worktree", "remove", wtPath, "--landing-target", "main")
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("worktree remove: status=%s exit=%d, want precondition_unmet/30: %+v", r.Status, exit, r)
	}
	if _, err := os.Stat(wtPath); err != nil {
		t.Fatalf("a refused worktree remove disturbed the worktree: %v", err)
	}
}

// TestOtherVerbs_DataMaps_DoNotCarryMergeDataKeys is SC-A2's regression
// backstop: finishErr, finishUsage and finishDiagnostic all gained a data
// parameter for merge's sake, but every pre-existing call site outside
// merge.go still passes nil, so no other verb's result should carry the
// "repo" or "target" keys merge now populates. One case per verb, chosen to
// hit a different one of the three widened builders and a different
// clikit status, so a mistake threading data into any of them shows up
// here.
func TestOtherVerbs_DataMaps_DoNotCarryMergeDataKeys(t *testing.T) {
	bin := buildCLI(t)

	assertNoMergeKeys := func(t *testing.T, r wireResult) {
		t.Helper()
		if _, ok := r.Data["repo"]; ok {
			t.Fatalf("data unexpectedly carries a repo key: %+v", r.Data)
		}
		if _, ok := r.Data["target"]; ok {
			t.Fatalf("data unexpectedly carries a target key: %+v", r.Data)
		}
	}

	t.Run("sign root commit usage error", func(t *testing.T) {
		dir := initRepo(t)
		r, exit := runCLI(t, bin, "--repo", dir, "sign", "HEAD")
		if r.Status != "usage" || exit != 50 {
			t.Fatalf("status=%s exit=%d, want usage/50: %+v", r.Status, exit, r)
		}
		assertNoMergeKeys(t, r)
	})

	t.Run("worktree list not_found", func(t *testing.T) {
		r, exit := runCLI(t, bin, "--repo", t.TempDir(), "worktree", "list")
		if r.Status != "not_found" || exit != 40 {
			t.Fatalf("status=%s exit=%d, want not_found/40: %+v", r.Status, exit, r)
		}
		assertNoMergeKeys(t, r)
	})

	t.Run("branch delete unmerged precondition_unmet", func(t *testing.T) {
		dir := initRepo(t)
		runGit(t, dir, "branch", "feature")
		runGit(t, dir, "checkout", "-q", "feature")
		head := commitFile(t, dir, "feature.txt", "feature\n", "feature work")
		runGit(t, dir, "checkout", "-q", "main")
		r, exit := runCLI(t, bin, "--repo", dir, "branch", "delete", "feature", head, "--landing-target", "main")
		if r.Status != "precondition_unmet" || exit != 30 {
			t.Fatalf("status=%s exit=%d, want precondition_unmet/30: %+v", r.Status, exit, r)
		}
		assertNoMergeKeys(t, r)
	})

	t.Run("scan secrets precondition_unmet", func(t *testing.T) {
		dir := initRepo(t)
		commitFile(t, dir, "config.env", "AWS_KEY="+plantedAWSAccessKeyID+"\n", "add config")
		r, exit := runCLI(t, bin, "--repo", dir, "scan", "secrets")
		if r.Status != "precondition_unmet" || exit != 30 {
			t.Fatalf("status=%s exit=%d, want precondition_unmet/30: %+v", r.Status, exit, r)
		}
		assertNoMergeKeys(t, r)
	})

	t.Run("branch create success", func(t *testing.T) {
		dir := initRepo(t)
		r, exit := runCLI(t, bin, "--repo", dir, "branch", "create", "feature/x", "HEAD")
		if r.Status != "success" || exit != 0 {
			t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
		}
		assertNoMergeKeys(t, r)
	})

	t.Run("rebase success", func(t *testing.T) {
		dir := initRepo(t)
		runGit(t, dir, "branch", "feature")
		commitFile(t, dir, "main-only.txt", "main\n", "main advances")
		runGit(t, dir, "checkout", "-q", "feature")
		commitFile(t, dir, "feature-only.txt", "feature\n", "feature work")
		r, exit := runCLI(t, bin, "--repo", dir, "rebase", "main")
		if r.Status != "success" || exit != 0 {
			t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
		}
		assertNoMergeKeys(t, r)
	})

	t.Run("hooks install conflict", func(t *testing.T) {
		dir := initRepo(t)
		if r, exit := runCLI(t, bin, "--repo", dir, "hooks", "install"); r.Status != "success" || exit != 0 {
			t.Fatalf("first install: status=%s exit=%d: %+v", r.Status, exit, r)
		}
		r, exit := runCLI(t, bin, "--repo", dir, "hooks", "install")
		if r.Status != "conflict" || exit != 41 {
			t.Fatalf("status=%s exit=%d, want conflict/41: %+v", r.Status, exit, r)
		}
		assertNoMergeKeys(t, r)
	})

	t.Run("push repo retargeting usage", func(t *testing.T) {
		dir := initRepo(t)
		r, exit := runCLIIn(t, bin, dir, "push", "main", "--repo", ".")
		if r.Status != "usage" || exit != 50 {
			t.Fatalf("status=%s exit=%d, want usage/50: %+v", r.Status, exit, r)
		}
		assertNoMergeKeys(t, r)
	})
}

func TestHooksInstall_ExistingScriptWithoutForce_IsConflict(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)

	r, exit := runCLI(t, bin, "--repo", dir, "hooks", "install")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("first install: status=%s exit=%d: %+v", r.Status, exit, r)
	}
	r, exit = runCLI(t, bin, "--repo", dir, "hooks", "install")
	if r.Status != "conflict" || exit != 41 {
		t.Fatalf("status=%s exit=%d, want conflict/41: %+v", r.Status, exit, r)
	}

	r, exit = runCLI(t, bin, "--repo", dir, "hooks", "install", "--force")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("--force: status=%s exit=%d: %+v", r.Status, exit, r)
	}
	if overwritten, _ := r.Data["overwritten"].(bool); !overwritten {
		t.Fatalf("--force install did not report overwritten=true: %+v", r.Data)
	}
}
