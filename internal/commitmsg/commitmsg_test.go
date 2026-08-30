package commitmsg

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "init", "-q", "-b", "main")
	run(t, dir, "config", "user.name", "Test User")
	run(t, dir, "config", "user.email", "test@example.com")
	return dir
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
