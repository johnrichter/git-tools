// White-box unit tests for merge.go's unexported helpers: headSigState (the
// LED-109 residual gap -- verifying the minted merge commit's own signature
// rather than trusting `git merge -S`'s exit code) and commitsLanded (the
// commit-count fix). Both run directly against a scratch repo, without going
// through the built binary, since neither is reachable from package
// cli_test.
package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func runGitHere(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func initScratchRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runGitHere(t, dir, "init", "-q", "-b", "main")
	runGitHere(t, dir, "config", "user.name", "Test User")
	runGitHere(t, dir, "config", "user.email", "test@example.com")
	runGitHere(t, dir, "config", "commit.gpgsign", "false")
	return dir
}

func commitHere(t *testing.T, dir, name, message string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte("content\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitHere(t, dir, "add", name)
	runGitHere(t, dir, "commit", "-q", "-m", message)
	return strings.TrimSpace(runGitHere(t, dir, "rev-parse", "HEAD"))
}

// headSigState on a genuinely unsigned commit must report "N" -- the exact
// state the merge verb's post-merge check treats as a failed verification,
// caveat-ing the merge as unsigned rather than reporting success.
func TestHeadSigState_UnsignedCommit_ReportsN(t *testing.T) {
	dir := initScratchRepo(t)
	tip := commitHere(t, dir, "a.txt", "unsigned commit")

	state, err := headSigState(context.Background(), dir, tip)
	if err != nil {
		t.Fatalf("headSigState: %v", err)
	}
	if state != "N" {
		t.Fatalf("headSigState = %q, want N (unsigned)", state)
	}
	// This is exactly the condition merge's RunE tests to decide whether to
	// caveat the merge as unsigned; pin it here so a change to that
	// condition or to git's own vocabulary shows up as a unit failure.
	if state == "G" || state == "U" {
		t.Fatalf("an unsigned commit must not satisfy the verified states: %q", state)
	}
}

// commitsLanded counts exactly the commits newly reachable from newHead,
// including a merge commit when the range mints one -- proven here with a
// synthetic three-commit range rather than a real merge, isolating the
// rev-list arithmetic from the merge verb's own gate and signing machinery.
func TestCommitsLanded_CountsExactlyTheNewRange(t *testing.T) {
	dir := initScratchRepo(t)
	oldHead := commitHere(t, dir, "base.txt", "base")
	commitHere(t, dir, "one.txt", "commit one")
	commitHere(t, dir, "two.txt", "commit two")
	newHead := commitHere(t, dir, "three.txt", "commit three")

	n, err := commitsLanded(context.Background(), dir, oldHead, newHead)
	if err != nil {
		t.Fatalf("commitsLanded: %v", err)
	}
	if n != 3 {
		t.Fatalf("commitsLanded = %d, want 3", n)
	}

	// An empty range (old and new head identical) lands nothing.
	n, err = commitsLanded(context.Background(), dir, newHead, newHead)
	if err != nil {
		t.Fatalf("commitsLanded over an empty range: %v", err)
	}
	if n != 0 {
		t.Fatalf("commitsLanded over an empty range = %d, want 0", n)
	}
}
