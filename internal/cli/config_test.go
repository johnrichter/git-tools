package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/pflag"
)

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
