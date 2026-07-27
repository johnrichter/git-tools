// Integration tests exercise the built git-tools binary as a real
// subprocess against scratch git repositories, per the task's test
// strategy: resign/worktree/merge/rebase work, scans detect planted
// findings, hooks install, --help is complete, and exit codes follow
// clikit's taxonomy.
package cli_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildCLI compiles git-tools once per test binary run and returns the path
// to the resulting executable.
func buildCLI(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "git-tools")
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("go", "build", "-o", bin, "./cmd/git-tools")
	cmd.Dir = repoRoot
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go build git-tools: %v\n%s", err, out)
	}
	return bin
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
// signing disabled so tests run without a configured GPG/SSH signing key.
func initRepo(t *testing.T) string {
	t.Helper()
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
		{"branch", "--help"}, {"branch", "create", "--help"}, {"branch", "delete", "--help"},
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
	dir := initRepo(t)
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
	dir := initRepo(t)
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

	r, exit = runCLI(t, bin, "--repo", dir, "branch", "delete", "feature/x", head)
	if r.Status != "success" || exit != 0 {
		t.Fatalf("branch delete: status=%s exit=%d: %+v", r.Status, exit, r)
	}
	if err := exec.Command("git", "-C", dir, "show-ref", "--verify", "refs/heads/feature/x").Run(); err == nil {
		t.Fatal("branch still exists after delete")
	}
}

func TestBranch_DeleteStaleExpectedHead_IsConflict(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	runGit(t, dir, "branch", "feature/x")

	r, exit := runCLI(t, bin, "--repo", dir, "branch", "delete", "feature/x", "0000000000000000000000000000000000000000")
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

	r, exit = runCLI(t, bin, "--repo", dir, "worktree", "remove", wtPath)
	if r.Status != "success" || exit != 0 {
		t.Fatalf("worktree remove: status=%s exit=%d: %+v", r.Status, exit, r)
	}
	if _, err := os.Stat(wtPath); !os.IsNotExist(err) {
		t.Fatalf("worktree remove left %s behind", wtPath)
	}
}

func TestMerge_FastForward(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
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
	dir := initRepo(t)
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
	commitFile(t, dir, "config.env", "AWS_KEY=AKIAIOSFODNN7EXAMPLE\n", "add config")

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
	content := "---\nowner: datadog\n---\n\nbody\n"
	commitFile(t, dir, "doc.md", content, "add doc")

	r, exit := runCLI(t, bin, "--repo", dir, "scan", "privacy", "--privacy-tier", "public")
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("status=%s exit=%d, want precondition_unmet/30: %+v", r.Status, exit, r)
	}
}

func TestScanPrivacy_InternalEmailWarnsWithoutStrict(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	commitFile(t, dir, "doc.md", "contact person@datadoghq.com for details\n", "add doc")

	r, exit := runCLI(t, bin, "--repo", dir, "scan", "privacy", "--privacy-tier", "public")
	if r.Status != "caveats" || exit != 10 {
		t.Fatalf("status=%s exit=%d, want caveats/10: %+v", r.Status, exit, r)
	}

	r, exit = runCLI(t, bin, "--repo", dir, "scan", "privacy", "--privacy-tier", "public", "--strict")
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("--strict: status=%s exit=%d, want precondition_unmet/30: %+v", r.Status, exit, r)
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

func TestScanAll_CombinesEveryScanner(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	commitFile(t, dir, "config.env", "AWS_KEY=AKIAIOSFODNN7EXAMPLE\n", "add config")

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
	if err := os.WriteFile(filepath.Join(dir, "secret.env"), []byte("AWS_KEY=AKIAIOSFODNN7EXAMPLE\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", "secret.env")
	commit := exec.Command("git", "commit", "-q", "-m", "add secret")
	commit.Dir = dir
	if out, err := commit.CombinedOutput(); err == nil {
		t.Fatalf("commit with a planted secret should have been blocked by the installed hook:\n%s", out)
	}
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
