// Tests for the push verb: the ALLOW/REFUSE matrix over a hermetic tmpdir
// repo with a local bare remote — clean/dirty tree, --repo/--config
// retargeting, ref-kind resolution and HEAD match, the already-current and
// --dry-run no-ops, and the --help/Example contract.
package cli_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runCLIIn runs bin with args with its working directory set to dir. push
// refuses --repo, so a test targeting it relocates the process itself
// rather than pointing a flag at the scratch repo, exactly as a real caller
// of push would.
func runCLIIn(t *testing.T, bin, dir string, args ...string) (wireResult, int) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	out, _ := cmd.Output()
	exit := cmd.ProcessState.ExitCode()
	var r wireResult
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, out)
	}
	return r, exit
}

// newBareRemote clones dir into a fresh bare repository and wires it as
// dir's "origin", giving push a real remote to publish to without any
// network dependency.
func newBareRemote(t *testing.T, dir string) string {
	t.Helper()
	bare := t.TempDir()
	cmd := exec.Command("git", "clone", "--bare", "-q", dir, bare)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git clone --bare: %v\n%s", err, out)
	}
	runGit(t, dir, "remote", "add", "origin", bare)
	return bare
}

func TestPush_Refusals(t *testing.T) {
	bin := buildCLI(t)

	cases := []struct {
		name        string
		setup       func(t *testing.T, dir string)
		args        []string
		wantStatus  string
		wantExit    int
		wantMessage string // substring the governing error must name — the state a hand-completed push would need fixed
	}{
		{
			name: "tracked modification",
			setup: func(t *testing.T, dir string) {
				if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("changed\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			args:        []string{"push", "main"},
			wantStatus:  "precondition_unmet",
			wantExit:    30,
			wantMessage: "tracked modifications or staged changes",
		},
		{
			name: "staged change",
			setup: func(t *testing.T, dir string) {
				if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("changed\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				runGit(t, dir, "add", "base.txt")
			},
			args:        []string{"push", "main"},
			wantStatus:  "precondition_unmet",
			wantExit:    30,
			wantMessage: "tracked modifications or staged changes",
		},
		{
			name:        "--repo retargeting flag",
			setup:       func(t *testing.T, dir string) {},
			args:        []string{"push", "main", "--repo", "."},
			wantStatus:  "usage",
			wantExit:    50,
			wantMessage: "--repo/--config are refused",
		},
		{
			name:        "--config retargeting flag",
			setup:       func(t *testing.T, dir string) {},
			args:        []string{"push", "main", "--config", "git-tools.yaml"},
			wantStatus:  "usage",
			wantExit:    50,
			wantMessage: "--repo/--config are refused",
		},
		{
			name: "HEAD not on the requested branch",
			setup: func(t *testing.T, dir string) {
				runGit(t, dir, "branch", "feature")
			},
			args:        []string{"push", "feature"},
			wantStatus:  "conflict",
			wantExit:    41,
			wantMessage: "HEAD is on main, not feature",
		},
		{
			name:        "ref is neither a local branch nor a local tag",
			setup:       func(t *testing.T, dir string) {},
			args:        []string{"push", "does-not-exist"},
			wantStatus:  "not_found",
			wantExit:    40,
			wantMessage: "neither a local branch nor a local tag",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := initRepo(t)
			newBareRemote(t, dir)
			tc.setup(t, dir)
			r, exit := runCLIIn(t, bin, dir, tc.args...)
			if r.Status != tc.wantStatus || exit != tc.wantExit {
				t.Fatalf("status=%s exit=%d, want %s/%d: %+v", r.Status, exit, tc.wantStatus, tc.wantExit, r)
			}
			if len(r.Errors) == 0 {
				t.Fatalf("no governing error recorded: %+v", r)
			}
			message, _ := r.Errors[0]["message"].(string)
			if !strings.Contains(message, tc.wantMessage) {
				t.Fatalf("governing error message %q does not name the blocking state %q", message, tc.wantMessage)
			}
		})
	}
}

func TestPush_BranchAdvancesRemote(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	bare := newBareRemote(t, dir)
	tip := commitFile(t, dir, "next.txt", "next\n", "advance main")

	// The first line of the command's own documented Example, run exactly
	// as written (push refuses --repo, so cwd stands in for it).
	r, exit := runCLIIn(t, bin, dir, "push", "main")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	if got := runGit(t, bare, "rev-parse", "refs/heads/main"); got != tip {
		t.Fatalf("remote main = %s, want %s", got, tip)
	}
	if newHead, _ := r.Data["new_head"].(string); newHead != tip {
		t.Fatalf("data.new_head = %v, want %s", r.Data["new_head"], tip)
	}
}

func TestPush_DirectlyAuthoredCommits_NoHistoryAudit(t *testing.T) {
	// push never tries to tell a commit that landed via a merge apart from
	// one authored straight onto the branch — these commits are authored
	// directly (no merge involved at all), and push must treat them the
	// same as any other clean, on-branch state.
	bin := buildCLI(t)
	dir := initRepo(t)
	bare := newBareRemote(t, dir)
	commitFile(t, dir, "a.txt", "a\n", "direct commit a")
	tip := commitFile(t, dir, "b.txt", "b\n", "direct commit b")

	r, exit := runCLIIn(t, bin, dir, "push", "main")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	if got := runGit(t, bare, "rev-parse", "refs/heads/main"); got != tip {
		t.Fatalf("remote main = %s, want %s", got, tip)
	}
}

func TestPush_TagOnlyPushSucceedsFromAnyBranch(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	bare := newBareRemote(t, dir)
	runGit(t, dir, "tag", "-a", "v1.0.0", "-m", "v1.0.0")
	tagID := runGit(t, dir, "rev-parse", "v1.0.0")

	r, exit := runCLIIn(t, bin, dir, "push", "v1.0.0")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	if got := runGit(t, bare, "rev-parse", "refs/tags/v1.0.0"); got != tagID {
		t.Fatalf("remote tag = %s, want %s", got, tagID)
	}
}

func TestPush_AlreadyCurrent_IsCaveatNoOp(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	newBareRemote(t, dir)
	commitFile(t, dir, "next.txt", "next\n", "advance main")
	if r, exit := runCLIIn(t, bin, dir, "push", "main"); r.Status != "success" || exit != 0 {
		t.Fatalf("first push: status=%s exit=%d: %+v", r.Status, exit, r)
	}

	r, exit := runCLIIn(t, bin, dir, "push", "main")
	if r.Status != "caveats" || exit != 10 {
		t.Fatalf("status=%s exit=%d, want caveats/10: %+v", r.Status, exit, r)
	}
	if len(r.Caveats) == 0 {
		t.Fatal("caveats result carries no caveats for the already-current no-op")
	}
}

func TestPush_DryRun_DoesNotMutateRemote(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	bare := newBareRemote(t, dir)
	before := runGit(t, bare, "rev-parse", "refs/heads/main")
	commitFile(t, dir, "next.txt", "next\n", "advance main")

	r, exit := runCLIIn(t, bin, dir, "push", "main", "--dry-run")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	if _, ok := r.Data["would_push"]; !ok {
		t.Fatalf("dry-run result missing data.would_push: %+v", r.Data)
	}
	if after := runGit(t, bare, "rev-parse", "refs/heads/main"); after != before {
		t.Fatalf("--dry-run pushed anyway: remote main moved from %s to %s", before, after)
	}
}

func TestPush_UntrackedFile_IsNotDirtiness(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	newBareRemote(t, dir)
	commitFile(t, dir, "tracked.txt", "tracked\n", "advance main")
	if err := os.WriteFile(filepath.Join(dir, "scratch.tmp"), []byte("untracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r, exit := runCLIIn(t, bin, dir, "push", "main")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0 (an untracked file is not dirtiness): %+v", r.Status, exit, r)
	}
}

func TestPush_IgnoredFile_IsNotDirtiness(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	newBareRemote(t, dir)
	commitFile(t, dir, ".gitignore", "*.log\n", "add gitignore")
	if err := os.WriteFile(filepath.Join(dir, "debug.log"), []byte("noise\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r, exit := runCLIIn(t, bin, dir, "push", "main")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0 (an ignored file is not dirtiness): %+v", r.Status, exit, r)
	}
}

// TestPush_DivergedHistory_RejectedNotForced pins down the "never force"
// claim in push's docstring: when local and remote have diverged (neither
// is an ancestor of the other), a plain, non-force push is refused by git
// itself, and push must surface that refusal (transient/60) rather than
// forcing the remote to match — which would silently discard the commit
// only the remote has.
func TestPush_DivergedHistory_RejectedNotForced(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	bare := newBareRemote(t, dir)

	base := runGit(t, dir, "rev-parse", "HEAD")
	commitFile(t, dir, "local-only.txt", "local\n", "advance main locally")

	// A second clone advances the remote out from under dir's local branch,
	// so dir's next push is a genuine non-fast-forward.
	other := t.TempDir()
	runGit(t, filepath.Dir(other), "clone", "-q", bare, other)
	runGit(t, other, "config", "user.name", "Test User")
	runGit(t, other, "config", "user.email", "test@example.com")
	runGit(t, other, "config", "commit.gpgsign", "false")
	runGit(t, other, "checkout", "-q", base)
	runGit(t, other, "checkout", "-q", "-b", "tmp")
	remoteTip := commitFile(t, other, "remote-only.txt", "remote\n", "advance main remotely")
	runGit(t, other, "push", "-q", "origin", "tmp:main")

	r, exit := runCLIIn(t, bin, dir, "push", "main")
	if r.Status != "transient" || exit != 60 {
		t.Fatalf("status=%s exit=%d, want transient/60: %+v", r.Status, exit, r)
	}
	if got := runGit(t, bare, "rev-parse", "refs/heads/main"); got != remoteTip {
		t.Fatalf("remote main = %s, want unchanged %s (push must not force)", got, remoteTip)
	}
}

func TestPush_HelpIsComplete(t *testing.T) {
	bin := buildCLI(t)

	out, err := exec.Command(bin, "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("--help exited non-zero: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "push") {
		t.Fatalf("top-level --help does not mention push:\n%s", out)
	}

	out, err = exec.Command(bin, "push", "--help").CombinedOutput()
	if err != nil {
		t.Fatalf("push --help exited non-zero: %v\n%s", err, out)
	}
	text := string(out)
	for _, want := range []string{"Usage:", "Examples:", "--dry-run", "Exit codes:", "never inspects commit history"} {
		if !strings.Contains(text, want) {
			t.Errorf("push --help missing %q:\n%s", want, text)
		}
	}
}
