package cli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/pflag"
)

// gitRepoForConfigTest creates a scratch git repo with one committed file,
// signing disabled — enough for warnIfConfigTampered's own `git status` call
// to have a HEAD to compare against.
func gitRepoForConfigTest(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
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

// captureStderr redirects os.Stderr to a scratch file for the duration of fn,
// returning what fn wrote to it — the only way to observe
// warnIfConfigTampered's own fmt.Fprintf(os.Stderr, ...) call.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	real := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	defer func() { os.Stderr = real }()

	fn()

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64*1024)
	n, _ := r.Read(buf)
	return string(buf[:n])
}

func rootFlags() *pflag.FlagSet {
	fs := pflag.NewFlagSet("git-tools", pflag.ContinueOnError)
	fs.String("config", "", "")
	fs.String("repo", "", "")
	fs.String("remote", "", "")
	fs.String("privacy-tier", "", "")
	fs.Bool("strict", false, "")
	fs.Int64("max-binary-bytes", 0, "")
	return fs
}

func TestLoadConfig_Defaults(t *testing.T) {
	cfg, err := loadConfig(rootFlags())
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Repo != "." || cfg.Remote != "origin" || cfg.PrivacyTier != "public" {
		t.Errorf("unexpected defaults: %+v", cfg)
	}
	if cfg.Strict {
		t.Error("strict must default to false")
	}
}

func TestLoadConfig_FlagBeatsEnvBeatsFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cfg.yaml")
	if err := os.WriteFile(cfgPath, []byte("remote: from-file\nprivacy_tier: from-file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envPrefix+"REMOTE", "from-env")

	fs := rootFlags()
	if err := fs.Set("config", cfgPath); err != nil {
		t.Fatal(err)
	}
	if err := fs.Set("remote", "from-flag"); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(fs)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Remote != "from-flag" {
		t.Errorf("remote = %q, want from-flag (flag must win)", cfg.Remote)
	}
	if cfg.PrivacyTier != "from-file" {
		t.Errorf("privacy_tier = %q, want from-file (only file set it)", cfg.PrivacyTier)
	}
}

func TestLoadConfig_EnvBeatsFile(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "cfg.yaml")
	if err := os.WriteFile(cfgPath, []byte("remote: from-file\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envPrefix+"REMOTE", "from-env")

	fs := rootFlags()
	if err := fs.Set("config", cfgPath); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(fs)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Remote != "from-env" {
		t.Errorf("remote = %q, want from-env (env must win over file)", cfg.Remote)
	}
}

func TestLoadConfig_HyphenatedFlagMapsToUnderscoredKey(t *testing.T) {
	fs := rootFlags()
	if err := fs.Set("privacy-tier", "private"); err != nil {
		t.Fatal(err)
	}
	if err := fs.Set("max-binary-bytes", "1024"); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(fs)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.PrivacyTier != "private" {
		t.Errorf("--privacy-tier did not map to privacy_tier, got %q", cfg.PrivacyTier)
	}
	if cfg.MaxBinaryBytes != 1024 {
		t.Errorf("--max-binary-bytes did not map to max_binary_bytes, got %d", cfg.MaxBinaryBytes)
	}
}

func TestLoadConfig_ExplicitMissingConfigFileErrors(t *testing.T) {
	fs := rootFlags()
	if err := fs.Set("config", "/nonexistent/path/does-not-exist.yaml"); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(fs); err == nil {
		t.Error("loadConfig did not error on an explicit --config path that does not exist")
	}
}

// TestLoadConfig_DefaultResolvesAgainstRepoFlagTarget proves the directory-
// mismatch fix: the implicit default git-tools.yaml is resolved against
// --repo's own target directory, not the invoking process's cwd.
func TestLoadConfig_DefaultResolvesAgainstRepoFlagTarget(t *testing.T) {
	dir := gitRepoForConfigTest(t)
	if err := os.WriteFile(filepath.Join(dir, "git-tools.yaml"), []byte("remote: from-repo-dir\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fs := rootFlags()
	if err := fs.Set("repo", dir); err != nil {
		t.Fatal(err)
	}
	cfg, err := loadConfig(fs)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if cfg.Remote != "from-repo-dir" {
		t.Errorf("remote = %q, want from-repo-dir (config must load from --repo's target, not the process cwd)", cfg.Remote)
	}
}

// TestLoadConfig_WarnsOnUntrackedConfig proves the FB2 fix: an untracked
// git-tools.yaml still loads (loadConfigFile does not block on it) but now
// prints a stderr warning naming the file and its untracked state.
func TestLoadConfig_WarnsOnUntrackedConfig(t *testing.T) {
	dir := gitRepoForConfigTest(t)
	if err := os.WriteFile(filepath.Join(dir, "git-tools.yaml"), []byte("remote: from-untracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fs := rootFlags()
	if err := fs.Set("repo", dir); err != nil {
		t.Fatal(err)
	}
	var cfg *Config
	stderr := captureStderr(t, func() {
		var err error
		cfg, err = loadConfig(fs)
		if err != nil {
			t.Fatalf("loadConfig: %v", err)
		}
	})
	if cfg.Remote != "from-untracked" {
		t.Fatalf("remote = %q, want from-untracked (an untracked config must still load)", cfg.Remote)
	}
	if !strings.Contains(stderr, "git-tools.yaml") || !strings.Contains(stderr, "untracked") {
		t.Errorf("stderr = %q, want a warning naming git-tools.yaml as untracked", stderr)
	}
}

// TestLoadConfig_WarnsOnLocallyModifiedConfig covers the other FB2 case: a
// tracked git-tools.yaml edited since its last commit, not just a wholly
// untracked one.
func TestLoadConfig_WarnsOnLocallyModifiedConfig(t *testing.T) {
	dir := gitRepoForConfigTest(t)
	cfgPath := filepath.Join(dir, "git-tools.yaml")
	if err := os.WriteFile(cfgPath, []byte("remote: committed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitAdd := exec.Command("git", "add", "git-tools.yaml")
	commitAdd.Dir = dir
	if out, err := commitAdd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	commit := exec.Command("git", "commit", "-q", "-m", "add config")
	commit.Dir = dir
	if out, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}
	if err := os.WriteFile(cfgPath, []byte("remote: locally-modified\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	fs := rootFlags()
	if err := fs.Set("repo", dir); err != nil {
		t.Fatal(err)
	}
	var cfg *Config
	stderr := captureStderr(t, func() {
		var err error
		cfg, err = loadConfig(fs)
		if err != nil {
			t.Fatalf("loadConfig: %v", err)
		}
	})
	if cfg.Remote != "locally-modified" {
		t.Fatalf("remote = %q, want locally-modified (the on-disk edit must still load)", cfg.Remote)
	}
	if !strings.Contains(stderr, "git-tools.yaml") || !strings.Contains(stderr, "locally modified") {
		t.Errorf("stderr = %q, want a warning naming git-tools.yaml as locally modified", stderr)
	}
}

// TestLoadConfig_NoWarningOnCleanTrackedConfig is the no-regression
// counterpart: a tracked, unmodified git-tools.yaml must load silently.
func TestLoadConfig_NoWarningOnCleanTrackedConfig(t *testing.T) {
	dir := gitRepoForConfigTest(t)
	cfgPath := filepath.Join(dir, "git-tools.yaml")
	if err := os.WriteFile(cfgPath, []byte("remote: committed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commitAdd := exec.Command("git", "add", "git-tools.yaml")
	commitAdd.Dir = dir
	if out, err := commitAdd.CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}
	commit := exec.Command("git", "commit", "-q", "-m", "add config")
	commit.Dir = dir
	if out, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("git commit: %v\n%s", err, out)
	}

	fs := rootFlags()
	if err := fs.Set("repo", dir); err != nil {
		t.Fatal(err)
	}
	stderr := captureStderr(t, func() {
		if _, err := loadConfig(fs); err != nil {
			t.Fatalf("loadConfig: %v", err)
		}
	})
	if stderr != "" {
		t.Errorf("stderr = %q, want no warning for a tracked and clean config", stderr)
	}
}

func TestLoadConfig_ImplicitMissingConfigFileIsNotAnError(t *testing.T) {
	dir := t.TempDir()
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.Chdir(cwd); err != nil {
			t.Fatal(err)
		}
	}()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := loadConfig(rootFlags()); err != nil {
		t.Errorf("loadConfig errored with no --config and no default file present: %v", err)
	}
}
