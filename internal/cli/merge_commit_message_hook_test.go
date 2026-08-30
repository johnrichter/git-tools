// LED-118: merge checks an explicit --message against the repository's own
// configured commit-msg hook, ahead of the content scan and the signing
// gate. These tests cover
// the three outcomes that hook can produce: no hook configured (a no-op),
// a hook that accepts, and a hook that rejects.
package cli_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// installCommitMsgHook points dir's core.hooksPath at a fresh directory
// carrying a commit-msg script that behaves per accept: exit 0 when true,
// exit 1 (with a fixed stderr message) when false. It also writes a marker
// file the script touches on every invocation, so a test can prove the hook
// actually ran rather than merely that its verdict happened to match.
func installCommitMsgHook(t *testing.T, dir string, accept bool) (hooksDir, markerPath string) {
	t.Helper()
	hooksDir = filepath.Join(t.TempDir(), "hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	markerPath = filepath.Join(hooksDir, "ran")
	script := "#!/bin/sh\ntouch " + markerPath + "\n"
	if accept {
		script += "exit 0\n"
	} else {
		script += "echo 'commit message rejected: no ticket reference' >&2\nexit 1\n"
	}
	hookPath := filepath.Join(hooksDir, "commit-msg")
	if err := os.WriteFile(hookPath, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "config", "core.hooksPath", hooksDir)
	return hooksDir, markerPath
}

// requireNoCommitMsgHook fails unless dir genuinely has no commit-msg hook to
// delegate to: no core.hooksPath resolved anywhere, and no hook at git's own
// default location. A "no hook configured" case has to assert this rather
// than assume it -- see isolateHostGitConfig.
func requireNoCommitMsgHook(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "config", "--get", "core.hooksPath")
	cmd.Dir = dir
	if out, err := cmd.Output(); err == nil {
		t.Fatalf("core.hooksPath resolves to %q; this case needs no hook configured at all", strings.TrimSpace(string(out)))
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "hooks", "commit-msg")); err == nil {
		t.Fatal("the default .git/hooks/commit-msg exists; this case needs no hook configured at all")
	}
}

// isolateHostGitConfig detaches this case from the host's own global and
// system git config. On a machine whose global core.hooksPath already names a
// real, executable commit-msg hook, an unisolated scratch repo inherits it --
// so a case meant to cover the no-hook no-op would instead exercise the
// delegation path and pass only because the host's hook accepts the message.
// runGit and runCLI both let their child inherit this process's environment,
// so one call here covers the fixture's git calls and the CLI subprocess
// alike. Call it before the fixture is built.
func isolateHostGitConfig(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
}

func TestMerge_CommitMessageHook_NoneConfigured_ProceedsNormally(t *testing.T) {
	isolateHostGitConfig(t)
	bin := buildCLI(t)
	dir := signingRepo(t)
	signedBranch(t, dir, "feature")
	requireNoCommitMsgHook(t, dir)

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature", "--fast-forward", "never", "--message", "merge feature into main")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0 with no hook configured: %+v", r.Status, exit, r)
	}
	if got := runGit(t, dir, "log", "-1", "--format=%s", "HEAD"); got != "merge feature into main" {
		t.Fatalf("HEAD subject=%q, want the supplied --message", got)
	}
}

func TestMerge_CommitMessageHook_Accepts_ProceedsAndHookActuallyRan(t *testing.T) {
	bin := buildCLI(t)
	dir := signingRepo(t)
	signedBranch(t, dir, "feature")
	_, marker := installCommitMsgHook(t, dir, true)

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature", "--fast-forward", "never", "--message", "merge feature into main")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0 with an accepting hook: %+v", r.Status, exit, r)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("commit-msg hook marker missing; the hook did not actually run: %v", err)
	}
	if got := runGit(t, dir, "log", "-1", "--format=%s", "HEAD"); got != "merge feature into main" {
		t.Fatalf("HEAD subject=%q, want the supplied --message", got)
	}
}

func TestMerge_CommitMessageHook_Rejects_RefusesBeforeAnythingLands(t *testing.T) {
	bin := buildCLI(t)
	dir := signingRepo(t)
	featureTip := signedBranch(t, dir, "feature")
	mainTip := runGit(t, dir, "rev-parse", "HEAD")
	_, marker := installCommitMsgHook(t, dir, false)

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature", "--fast-forward", "never", "--message", "merge feature into main")
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("status=%s exit=%d, want precondition_unmet/30 for a rejecting hook: %+v", r.Status, exit, r)
	}
	if len(r.Errors) == 0 {
		t.Fatalf("no error diagnostic reported: %+v", r)
	}
	code, _ := r.Errors[0]["code"].(string)
	if code != "precondition_unmet.git.commit_message_hook_rejected" {
		t.Fatalf("error code=%q, want precondition_unmet.git.commit_message_hook_rejected: %+v", code, r.Errors[0])
	}
	msg, _ := r.Errors[0]["message"].(string)
	if !strings.Contains(msg, "no ticket reference") {
		t.Fatalf("error message %q does not surface the hook's own rejection detail", msg)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("commit-msg hook marker missing; the hook did not actually run: %v", err)
	}

	// Nothing landed: the target branch is untouched, and the check ran
	// early enough that the signing gate never touched the source branch
	// either -- a rejected message costs no re-signing work.
	if got := runGit(t, dir, "rev-parse", "main"); got != mainTip {
		t.Fatalf("main moved to %s despite the hook's rejection, want unchanged %s", got, mainTip)
	}
	if got := runGit(t, dir, "rev-parse", "feature"); got != featureTip {
		t.Fatalf("feature moved to %s despite the hook's rejection (the signing gate should never have run), want unchanged %s", got, featureTip)
	}
}

// A merge with no --message never runs the explicit check itself -- there is
// no git-tools-composed message to check. git's own "git merge" below still
// runs its own default message past a configured hook natively (unaffected
// by this task, and unchanged by it): an accepting hook still lets that
// merge land.
func TestMerge_CommitMessageHook_NoExplicitMessage_HookStillRunsNatively(t *testing.T) {
	bin := buildCLI(t)
	dir := signingRepo(t)
	signedBranch(t, dir, "feature")
	_, marker := installCommitMsgHook(t, dir, true)

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature", "--fast-forward", "never")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0 -- git's own default message still passes an accepting hook natively: %+v", r.Status, exit, r)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("commit-msg hook marker missing; git's own merge should have run it natively even though the explicit check was skipped: %v", err)
	}
}
