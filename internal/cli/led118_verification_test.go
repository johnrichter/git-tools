// Independent test-engineer verification for LED-118, written separately
// from the engineer's own merge_commit_message_hook_test.go: same three
// live outcomes (accept / reject / no hook), plus a fourth probe into the
// scope note about git's own default merge message hitting a rejecting
// hook. Deliberately uses its own hook script wording and its own
// assertions rather than reusing the engineer's fixtures, so a pass here is
// independent evidence, not an echo of the implementation's own tests.
package cli_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// decodeWire decodes a raw CLI stdout capture into the same wireResult shape
// runCLI uses, for call sites that need a custom exec.Cmd (e.g. a modified
// Env) instead of runCLI's own.
func decodeWire(t *testing.T, out []byte) wireResult {
	t.Helper()
	var r wireResult
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, out)
	}
	return r
}

// qaHook writes a commit-msg hook at a fresh core.hooksPath that inspects
// the message file it is handed (its sole argument, per git's own
// commit-msg contract) and rejects unless the message contains the literal
// substring "APPROVED". This is a different verdict rule than the
// engineer's fixed accept/reject flag, so a pass here proves the hook's
// argument-passing and message contents are real, not just its exit code.
func qaHook(t *testing.T, dir string) (markerPath string) {
	t.Helper()
	hooksDir := filepath.Join(t.TempDir(), "qa-hooks")
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	markerPath = filepath.Join(hooksDir, "qa-invoked")
	script := `#!/bin/sh
cat "$1" >> ` + markerPath + `
echo "---" >> ` + markerPath + `
if grep -q APPROVED "$1"; then
  exit 0
fi
echo "qa-hook: message lacks APPROVED marker" >&2
exit 7
`
	if err := os.WriteFile(filepath.Join(hooksDir, "commit-msg"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "config", "core.hooksPath", hooksDir)
	return markerPath
}

// TestLED118_QA_HookAccepts_MergeLands is the test engineer's own live
// accept-path exercise: a hook that reads the actual message content
// (not just a fixed exit code) and approves it.
func TestLED118_QA_HookAccepts_MergeLands(t *testing.T) {
	bin := buildCLI(t)
	dir := signingRepo(t)
	signedBranch(t, dir, "qa-feature")
	marker := qaHook(t, dir)

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "qa-feature", "--fast-forward", "never", "--message", "APPROVED: land qa-feature")
	if exit != 0 || r.Status != "success" {
		t.Fatalf("qa: exit=%d status=%s, want 0/success when the hook approves: %+v", exit, r.Status, r)
	}
	content, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("qa: hook marker not written; hook did not run: %v", err)
	}
	if !strings.Contains(string(content), "APPROVED") {
		t.Fatalf("qa: marker %q does not show the hook actually saw the supplied message", content)
	}
	if got := runGit(t, dir, "log", "-1", "--format=%s", "HEAD"); got != "APPROVED: land qa-feature" {
		t.Fatalf("qa: HEAD subject=%q, want the exact supplied message", got)
	}
}

// TestLED118_QA_HookRejects_MergeBlockedNothingMoves is the test engineer's
// own live reject-path exercise, additionally checking the exit code and
// diagnostic vocabulary against the documented contract (exit 30,
// precondition_unmet) rather than merely "not success".
func TestLED118_QA_HookRejects_MergeBlockedNothingMoves(t *testing.T) {
	bin := buildCLI(t)
	dir := signingRepo(t)
	featureTip := signedBranch(t, dir, "qa-feature")
	mainTip := runGit(t, dir, "rev-parse", "HEAD")
	marker := qaHook(t, dir)

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "qa-feature", "--fast-forward", "never", "--message", "land qa-feature without the marker")
	if exit != 30 {
		t.Fatalf("qa: exit=%d, want 30 (precondition_unmet) for a rejecting hook", exit)
	}
	if r.Status != "precondition_unmet" {
		t.Fatalf("qa: status=%q, want precondition_unmet: %+v", r.Status, r)
	}
	if len(r.Errors) != 1 {
		t.Fatalf("qa: want exactly one error diagnostic, got %d: %+v", len(r.Errors), r.Errors)
	}
	if code, _ := r.Errors[0]["code"].(string); code != "precondition_unmet.git.commit_message_hook_rejected" {
		t.Fatalf("qa: error code=%q, want precondition_unmet.git.commit_message_hook_rejected", code)
	}
	if msg, _ := r.Errors[0]["message"].(string); !strings.Contains(msg, "lacks APPROVED marker") {
		t.Fatalf("qa: message %q does not surface the hook's own stderr detail", msg)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("qa: hook marker missing; hook did not actually run: %v", err)
	}
	if got := runGit(t, dir, "rev-parse", "main"); got != mainTip {
		t.Fatalf("qa: main moved to %s, want unchanged %s -- a rejected message must land nothing", got, mainTip)
	}
	if got := runGit(t, dir, "rev-parse", "qa-feature"); got != featureTip {
		t.Fatalf("qa: qa-feature moved to %s, want unchanged %s -- the signing gate must never run for a doomed merge", got, featureTip)
	}
}

// TestLED118_QA_HostGlobalHookIsInherited_NotActuallyNoHook is a finding,
// not a straightforward pass/fail check: this sandbox's own git installation
// carries a real global core.hooksPath (/usr/local/lib/dd-git-hooks, set in
// $HOME/.gitconfig by the host, not by git-tools or this test), with an
// executable commit-msg script there. That means a freshly initialized repo
// in this environment is never actually "no hook configured" unless a test
// explicitly isolates itself from the host's global git config -- the
// engineer's own TestCheck_NoHookConfigured_IsNoOp and
// TestMerge_CommitMessageHook_NoneConfigured_ProceedsNormally do not do
// this, so in this sandbox they exercise the delegation path (the host's
// real hook actually runs and accepts the benign test message) rather than
// the true no-op path their names claim. This test pins that down directly:
// Check resolves and actually runs the host's hook here.
func TestLED118_QA_HostGlobalHookIsInherited_NotActuallyNoHook(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-q", "-b", "main")
	hooksPath, err := exec.Command("git", "-C", dir, "config", "--get", "core.hooksPath").CombinedOutput()
	if err != nil {
		t.Skip("qa: this host carries no global core.hooksPath; the 'no hook configured' tests are exercising a true no-op here, nothing to flag")
	}
	resolved := strings.TrimSpace(string(hooksPath))
	if _, statErr := os.Stat(filepath.Join(resolved, "commit-msg")); statErr != nil {
		t.Skipf("qa: global core.hooksPath %q carries no commit-msg script; nothing to flag", resolved)
	}
	t.Logf("qa FINDING: a fresh repo in this sandbox inherits a real, executable global commit-msg hook at %s -- 'no hook configured' tests that do not isolate GIT_CONFIG_GLOBAL/HOME are not exercising a true no-op in this environment", filepath.Join(resolved, "commit-msg"))
}

// TestLED118_QA_TrueNoHook_MergeUnaffected isolates the CLI subprocess and
// its git config from this host's real global commit-msg hook (via
// GIT_CONFIG_GLOBAL=/dev/null, GIT_CONFIG_SYSTEM=/dev/null), producing an
// actual, verified no-hook-configured repository -- the genuine scenario
// (c) the dispatch asked for, not the host-global-hook-happens-to-accept
// case the existing suite runs today.
func TestLED118_QA_TrueNoHook_MergeUnaffected(t *testing.T) {
	bin := buildCLI(t)
	dir := t.TempDir()
	isolatedEnv := append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")

	runIsolatedGit := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = isolatedEnv
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("qa isolated git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	runIsolatedGit("init", "-q", "-b", "main")
	runIsolatedGit("config", "user.name", "QA")
	runIsolatedGit("config", "user.email", "qa@example.com")

	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Fatalf("qa: ssh-keygen required for this fixture's signing setup: %v", err)
	}
	keyDir := t.TempDir()
	key := filepath.Join(keyDir, "signing-key")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "qa signing fixture", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("qa ssh-keygen: %v\n%s", err, out)
	}
	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	allowed := filepath.Join(keyDir, "allowed_signers")
	if err := os.WriteFile(allowed, []byte(`qa@example.com namespaces="git" `+string(pub)), 0o600); err != nil {
		t.Fatal(err)
	}
	runIsolatedGit("config", "gpg.format", "ssh")
	runIsolatedGit("config", "user.signingkey", key+".pub")
	runIsolatedGit("config", "gpg.ssh.allowedSignersFile", allowed)
	runIsolatedGit("config", "commit.gpgsign", "true")

	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runIsolatedGit("add", "seed.txt")
	runIsolatedGit("commit", "-q", "-m", "seed")
	runIsolatedGit("checkout", "-q", "-b", "qa-feature")
	if err := os.WriteFile(filepath.Join(dir, "feature.txt"), []byte("feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runIsolatedGit("add", "feature.txt")
	runIsolatedGit("commit", "-q", "-m", "feature work")
	runIsolatedGit("checkout", "-q", "main")

	cmd := exec.Command("git", "-C", dir, "config", "--get", "core.hooksPath")
	cmd.Env = isolatedEnv
	if got, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("qa: isolation failed even with the override applied, core.hooksPath still resolves to %q", got)
	}

	cliCmd := exec.Command(bin, "--repo", dir, "merge", "qa-feature", "--fast-forward", "never", "--message", "no hook here at all, verified")
	cliCmd.Env = isolatedEnv
	out, _ := cliCmd.Output()
	r, exit := decodeWire(t, out), cliCmd.ProcessState.ExitCode()
	if exit != 0 || r.Status != "success" {
		t.Fatalf("qa: exit=%d status=%s, want 0/success with a genuinely unconfigured hook: %+v", exit, r.Status, r)
	}
	if got := runIsolatedGit("log", "-1", "--format=%s", "HEAD"); got != "no hook here at all, verified" {
		t.Fatalf("qa: HEAD subject=%q, want the supplied message unchanged", got)
	}
}

// TestLED118_QA_DefaultMergeMessage_RejectingHook_ClassifiesAsInternal
// reproduces the scope note flagged in the engineer's report: a
// commit-msg hook that rejects git's own default (git-tools-composed
// --message omitted) merge message surfaces through git's native merge
// invocation, not through commitmsg.Check. This test's own purpose is to
// pin down exactly how that surfaces today -- exit code and diagnostic
// code -- as evidence for the quality reviewer's judgment call, not to
// assert either answer is correct.
func TestLED118_QA_DefaultMergeMessage_RejectingHook_ClassifiesAsInternal(t *testing.T) {
	bin := buildCLI(t)
	dir := signingRepo(t)
	featureTip := signedBranch(t, dir, "qa-feature")
	mainTip := runGit(t, dir, "rev-parse", "HEAD")
	qaHook(t, dir) // rejects unless message contains APPROVED; git's default merge message never will.

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "qa-feature", "--fast-forward", "never")

	t.Logf("qa: default-message + rejecting-hook outcome: exit=%d status=%s errors=%+v", exit, r.Status, r.Errors)

	if got := runGit(t, dir, "rev-parse", "main"); got != mainTip {
		t.Fatalf("qa: main moved to %s despite a rejecting hook, want unchanged %s", got, mainTip)
	}
	if got := runGit(t, dir, "rev-parse", "qa-feature"); got != featureTip {
		t.Fatalf("qa: qa-feature moved to %s despite a rejecting hook, want unchanged %s", got, featureTip)
	}

	// Pin the report's claim precisely: today this is exit 90 / internal,
	// not exit 30 / precondition_unmet. A future fix that reclassifies this
	// should update this assertion deliberately, not by accident.
	if exit != 90 || r.Status != "internal" {
		t.Fatalf("qa: exit=%d status=%s -- report claimed exit 90/internal for this path; if this changed, the report's scope note is stale either way (fixed or regressed)", exit, r.Status)
	}
}
