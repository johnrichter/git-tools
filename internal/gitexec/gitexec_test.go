// Tests for the raw git spawner and its yes/no decoders, driven against
// scratch repositories. The three shapes that matter for RunGit are a clean
// exit (Result, no error), a non-zero exit (still a Result, no error — git's
// own way of answering), and a spawn failure (a Go error, no Result).
package gitexec

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// scratchRepo creates a throwaway repo with one commit on branch main and
// returns its path. Setup runs git directly so the package under test is not
// also the fixture builder.
func scratchRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.name", "Test User")
	run("config", "user.email", "test@example.com")
	run("config", "commit.gpgsign", "false")
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "base.txt")
	run("commit", "-q", "-m", "base")
	return dir
}

func TestRunGit_CleanExit(t *testing.T) {
	dir := scratchRepo(t)
	res, err := RunGit(context.Background(), dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("clean exit returned a Go error: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("exit code = %d, want 0", res.ExitCode)
	}
	if strings.TrimSpace(string(res.Stdout)) == "" {
		t.Fatalf("rev-parse HEAD produced no stdout")
	}
}

func TestRunGit_NonZeroExit(t *testing.T) {
	dir := scratchRepo(t)
	// Verifying a ref that does not exist is a non-zero exit, which is a
	// structured answer git returns through the Result — never a Go error.
	res, err := RunGit(context.Background(), dir, "rev-parse", "--verify", "refs/heads/absent")
	if err != nil {
		t.Fatalf("a non-zero exit became a Go error: %v", err)
	}
	if res.ExitCode == 0 {
		t.Fatalf("exit code = 0, want non-zero for an absent ref")
	}
}

func TestRunGit_SpawnFailure(t *testing.T) {
	// A working directory that does not exist makes the spawn itself fail, the
	// one case RunGit surfaces as a Go error with no Result.
	res, err := RunGit(context.Background(), filepath.Join(t.TempDir(), "does-not-exist"), "status")
	if err == nil {
		t.Fatalf("spawn failure returned no error (res=%v)", res)
	}
	if res != nil {
		t.Fatalf("spawn failure returned a non-nil Result: %+v", res)
	}
	if !strings.Contains(err.Error(), "exec git") {
		t.Fatalf("error %q does not name the failed git exec", err)
	}
}

func TestTreeDirty(t *testing.T) {
	dir := scratchRepo(t)
	dirty, err := TreeDirty(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if dirty {
		t.Fatalf("a freshly committed tree reports dirty")
	}
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirty, err = TreeDirty(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if !dirty {
		t.Fatalf("a modified tracked file does not report dirty")
	}
}

func TestCurrentBranch(t *testing.T) {
	dir := scratchRepo(t)
	branch, err := CurrentBranch(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if branch != "main" {
		t.Fatalf("current branch = %q, want main", branch)
	}
	// Detaching HEAD must read back as the empty string, not an error.
	head, err := RunGit(context.Background(), dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := RunGit(context.Background(), dir, "checkout", "-q", strings.TrimSpace(string(head.Stdout))); err != nil {
		t.Fatal(err)
	}
	branch, err = CurrentBranch(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if branch != "" {
		t.Fatalf("detached HEAD current branch = %q, want empty", branch)
	}
}

func TestRefExists(t *testing.T) {
	dir := scratchRepo(t)
	ok, err := RefExists(context.Background(), dir, "heads", "main")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatalf("refs/heads/main reported absent")
	}
	ok, err = RefExists(context.Background(), dir, "heads", "absent")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatalf("refs/heads/absent reported present")
	}
}
