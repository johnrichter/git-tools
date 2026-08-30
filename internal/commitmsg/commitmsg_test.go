package commitmsg

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initRepo creates a scratch repository whose configured commit-msg hook is
// whatever the case itself plants, and nothing else.
//
// The two GIT_CONFIG_* overrides are load-bearing, not hygiene: a host whose
// own global core.hooksPath already names a real, executable commit-msg hook
// (a corporate secrets scanner, say) makes every unisolated case here resolve
// that hook instead of the one it planted. The no-op cases would then run the
// host's hook and pass only because it happens to accept a benign message,
// and the default-.git/hooks cases would exercise a file they never wrote.
// Both pass either way, so the coupling is silent -- hence the override, plus
// the assertion below that it actually took effect. Check reads git config
// through this process's own environment, so setting it here reaches both the
// resolution calls and the hook invocation.
func initRepo(t *testing.T) string {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	dir := t.TempDir()
	run(t, dir, "init", "-q", "-b", "main")
	run(t, dir, "config", "user.name", "Test User")
	run(t, dir, "config", "user.email", "test@example.com")
	requireNoHooksPathConfigured(t, dir)
	return dir
}

// requireNoHooksPathConfigured fails unless dir resolves no core.hooksPath at
// all -- the state initRepo's overrides are there to produce, asserted rather
// than assumed so a case meaning to exercise git's default .git/hooks
// resolution cannot quietly start exercising a host hook instead.
func requireNoHooksPathConfigured(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("git", "config", "--get", "core.hooksPath")
	cmd.Dir = dir
	out, err := cmd.Output()
	if err == nil {
		t.Fatalf("core.hooksPath resolves to %q; this fixture needs it unset (isolation from the host's git config failed)", strings.TrimSpace(string(out)))
	}
}

func run(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeHook(t *testing.T, path string, accept bool) {
	t.Helper()
	body := "#!/bin/sh\n"
	if accept {
		body += "exit 0\n"
	} else {
		body += "echo 'rejected: bad format' >&2\nexit 1\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestCheck_NoHookConfigured_IsNoOp(t *testing.T) {
	dir := initRepo(t)
	refusal, err := Check(context.Background(), dir, "any message at all")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if refusal != nil {
		t.Fatalf("refusal = %+v, want nil with no hook configured", refusal)
	}
}

func TestCheck_DefaultGitHooksDir_Accepts(t *testing.T) {
	dir := initRepo(t)
	hookPath := filepath.Join(dir, ".git", "hooks", "commit-msg")
	writeHook(t, hookPath, true)

	refusal, err := Check(context.Background(), dir, "a fine message")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if refusal != nil {
		t.Fatalf("refusal = %+v, want nil for an accepting hook", refusal)
	}
}

func TestCheck_DefaultGitHooksDir_Rejects(t *testing.T) {
	dir := initRepo(t)
	hookPath := filepath.Join(dir, ".git", "hooks", "commit-msg")
	writeHook(t, hookPath, false)

	refusal, err := Check(context.Background(), dir, "a bad message")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if refusal == nil {
		t.Fatal("refusal = nil, want a refusal for a rejecting hook")
	}
	if refusal.Code() != "precondition_unmet.git.commit_message_hook_rejected" {
		t.Fatalf("Code() = %q, want precondition_unmet.git.commit_message_hook_rejected", refusal.Code())
	}
	if !strings.Contains(refusal.Message(), "rejected: bad format") {
		t.Fatalf("Message() = %q, does not surface the hook's own detail", refusal.Message())
	}
}

func TestCheck_NonExecutableHookFile_IsNoOp(t *testing.T) {
	dir := initRepo(t)
	hookPath := filepath.Join(dir, ".git", "hooks", "commit-msg")
	if err := os.WriteFile(hookPath, []byte("#!/bin/sh\nexit 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	refusal, err := Check(context.Background(), dir, "any message")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if refusal != nil {
		t.Fatalf("refusal = %+v, want nil for a non-executable hook file (not configured to run)", refusal)
	}
}

func TestCheck_RelativeHooksPath_ResolvedAgainstRepo(t *testing.T) {
	dir := initRepo(t)
	if err := os.MkdirAll(filepath.Join(dir, ".githooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeHook(t, filepath.Join(dir, ".githooks", "commit-msg"), false)
	run(t, dir, "config", "core.hooksPath", ".githooks")

	refusal, err := Check(context.Background(), dir, "message")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if refusal == nil {
		t.Fatal("refusal = nil, want a refusal from the relative core.hooksPath hook")
	}
}

func TestCheck_AbsoluteHooksPath_UsedDirectly(t *testing.T) {
	dir := initRepo(t)
	hooksDir := t.TempDir()
	writeHook(t, filepath.Join(hooksDir, "commit-msg"), false)
	run(t, dir, "config", "core.hooksPath", hooksDir)

	refusal, err := Check(context.Background(), dir, "message")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if refusal == nil {
		t.Fatal("refusal = nil, want a refusal from the absolute core.hooksPath hook")
	}
}

func TestCheck_ConfiguredHooksPathWithNoCommitMsgScript_IsNoOp(t *testing.T) {
	dir := initRepo(t)
	hooksDir := t.TempDir()
	run(t, dir, "config", "core.hooksPath", hooksDir)

	refusal, err := Check(context.Background(), dir, "message")
	if err != nil {
		t.Fatalf("Check: %v", err)
	}
	if refusal != nil {
		t.Fatalf("refusal = %+v, want nil when the configured directory carries no commit-msg script", refusal)
	}
}
