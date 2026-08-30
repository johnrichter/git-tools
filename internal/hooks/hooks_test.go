package hooks

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"testing"
)

func initScratchRepo(t *testing.T) string {
	t.Helper()
	// Isolate from the host's global/system git config, for consistency with
	// every other fixture in this repo -- no commit happens here, so there is
	// little wall-clock time to win, but this keeps the convention uniform.
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	dir := t.TempDir()
	cmd := exec.Command("git", "init", "-q")
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	return dir
}

func TestRenderScript_EmbedsBinaryTierAndStrict(t *testing.T) {
	script := RenderScript("git-tools", "confidential", true)
	if got := "exec 'git-tools' scan all --staged --privacy-tier 'confidential' --strict\n"; !containsLine(script, got) {
		t.Errorf("script = %q, want it to contain %q", script, got)
	}
}

func TestRenderScript_WithoutStrictOmitsFlag(t *testing.T) {
	script := RenderScript("git-tools", "public", false)
	if containsLine(script, "--strict") {
		t.Errorf("script = %q, should not contain --strict", script)
	}
}

func TestRenderScript_QuotesSingleQuoteInBinary(t *testing.T) {
	script := RenderScript("weird'binary", "public", false)
	if !containsLine(script, `'weird'\''binary'`) {
		t.Errorf("script = %q, want binary safely single-quoted", script)
	}
}

func containsLine(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}

func TestInstall_WritesScriptAndSetsHooksPath(t *testing.T) {
	dir := initScratchRepo(t)
	res, err := Install(context.Background(), InstallOptions{
		RepoDir: dir, HooksDir: ".githooks", HookName: "pre-commit",
		Binary: "git-tools", PrivacyTier: "public",
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if res.Overwritten {
		t.Error("first install reported Overwritten=true")
	}
	info, err := os.Stat(res.Path)
	if err != nil {
		t.Fatalf("hook script not written: %v", err)
	}
	if info.Mode()&0o111 == 0 {
		t.Error("hook script is not executable")
	}
}

func TestInstall_ExistingWithoutForceReturnsErrHookExists(t *testing.T) {
	dir := initScratchRepo(t)
	opts := InstallOptions{RepoDir: dir, HooksDir: ".githooks", HookName: "pre-commit", Binary: "git-tools", PrivacyTier: "public"}
	if _, err := Install(context.Background(), opts); err != nil {
		t.Fatalf("first Install: %v", err)
	}
	_, err := Install(context.Background(), opts)
	if !errors.Is(err, ErrHookExists) {
		t.Fatalf("second Install error = %v, want ErrHookExists", err)
	}
}

func TestInstall_ForceOverwritesAndReportsSo(t *testing.T) {
	dir := initScratchRepo(t)
	opts := InstallOptions{RepoDir: dir, HooksDir: ".githooks", HookName: "pre-commit", Binary: "git-tools", PrivacyTier: "public"}
	if _, err := Install(context.Background(), opts); err != nil {
		t.Fatalf("first Install: %v", err)
	}
	opts.Force = true
	res, err := Install(context.Background(), opts)
	if err != nil {
		t.Fatalf("forced Install: %v", err)
	}
	if !res.Overwritten {
		t.Error("forced Install did not report Overwritten=true")
	}
}

func TestInstall_DryRunWritesNothing(t *testing.T) {
	dir := initScratchRepo(t)
	res, err := Install(context.Background(), InstallOptions{
		RepoDir: dir, HooksDir: ".githooks", HookName: "pre-commit",
		Binary: "git-tools", PrivacyTier: "public", DryRun: true,
	})
	if err != nil {
		t.Fatalf("Install: %v", err)
	}
	if !res.DryRun {
		t.Error("result did not report DryRun=true")
	}
	if _, err := os.Stat(res.Path); !os.IsNotExist(err) {
		t.Error("dry run wrote a hook script to disk")
	}
}
