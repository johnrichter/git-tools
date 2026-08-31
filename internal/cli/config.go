package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/knadh/koanf/parsers/yaml"
	"github.com/knadh/koanf/providers/confmap"
	"github.com/knadh/koanf/providers/env"
	"github.com/knadh/koanf/providers/file"
	"github.com/knadh/koanf/providers/posflag"
	"github.com/knadh/koanf/v2"
	"github.com/spf13/pflag"

	"github.com/johnrichter/claude-shared-tooling/go/githooks"
	"github.com/johnrichter/claude-shared-tooling/go/sysops"
)

// envPrefix selects which environment variables loadConfig reads, e.g.
// GITTOOLS_PRIVACY_TIER for the privacy_tier key.
const envPrefix = "GITTOOLS_"

// defaultConfigFile is the config file loadConfig looks for when the caller
// does not pass --config. Its absence is not an error: a config file is an
// optional way to stop repeating flags, never a requirement. Named without a
// leading dot on purpose: it is ordinary, reviewed repo content, not a
// hidden dotfile, and its own presence is a signal a reader should be able
// to see without passing -a to ls.
//
// Known, accepted residual: loadConfigFile reads this file from disk exactly
// as it sits at scan time, with no requirement that it be tracked or clean.
// An uncommitted, untracked, or dirty edit to it therefore takes effect
// immediately, potentially widening privacy_marker_exempt/secret_scan_exempt
// for whatever merge/push/tag create runs next. warnIfConfigTampered flags
// this loudly (a stderr warning naming the file and its state) but does not
// block it: a tracked-and-clean requirement was considered and rejected, as
// it would reverse a design internal/cli/scan_gate_test.go's own writeConfig
// helper deliberately asserts (an untracked config file taking effect), and
// needs its own design decision plus a rewritten test suite, not a drive-by
// change here. See marketplace's own
// .dat/reports/git-tools-privacy-scan-scope-bug.md for the full record.
const defaultConfigFile = "git-tools.yaml"

// Config is git-tools' resolved settings: every field a value one command or
// another needs, loaded once at the root so a config file or environment
// only has to state a setting once for every subcommand to see it.
type Config struct {
	Repo                string   `koanf:"repo"`
	Remote              string   `koanf:"remote"`
	PrivacyTier         string   `koanf:"privacy_tier"`
	Strict              bool     `koanf:"strict"`
	MaxBinaryBytes      int64    `koanf:"max_binary_bytes"`
	PrivacyMarkerExempt []string `koanf:"privacy_marker_exempt"`
	SecretScanExempt    []string `koanf:"secret_scan_exempt"`
	// AllowedEmailDomains exempts the public tier's employee-email
	// internal-identifier check from flagging an address at one of the named
	// domains (e.g. an organization's own mail domains). The check itself
	// always runs at that tier: with no domains configured — the default —
	// only githooks' own hardcoded example.com stays exempt, and any other
	// real-looking address flags. Deliberately a per-repo config key and
	// nothing else: which domains identify an organization's own people is
	// that organization's value, so it belongs in the repo that needs it, not
	// in this CLI's shared source, which serves any repo and ships publicly.
	AllowedEmailDomains []string `koanf:"allowed_email_domains"`
	// SecretScanExtraRules are a repo's own additional betterleaks detection
	// rules (githooks.ScanCredentials' opts.ExtraRules), layered on top of
	// the compiled-in base ruleset that scan can only ever extend, never
	// weaken. Each entry's Category is carried through config for a repo's
	// own record-keeping only — githooks.BetterleaksRule has no Category
	// field, so every betterleaks finding still reports Category
	// "credentials" regardless of what an extra rule's Category names.
	SecretScanExtraRules []SecretScanExtraRule `koanf:"secret_scan_extra_rules"`
	// SecretScanExtraAllowlist are a repo's own additional betterleaks
	// exemptions (githooks.ScanCredentials' opts.ExtraAllowlist), layered on
	// top of the compiled-in base allowlist the same way SecretScanExtraRules
	// layers onto the base ruleset.
	SecretScanExtraAllowlist []SecretScanExtraAllowlistEntry `koanf:"secret_scan_extra_allowlist"`
}

// SecretScanExtraRule is one secret_scan_extra_rules entry: a repo-supplied
// betterleaks detection rule, in exactly the shape githooks.BetterleaksRule
// needs plus a Category label this CLI does not otherwise act on (see
// Config.SecretScanExtraRules).
type SecretScanExtraRule struct {
	ID       string `koanf:"id"`
	Regex    string `koanf:"regex"`
	Category string `koanf:"category"`
}

// SecretScanExtraAllowlistEntry is one secret_scan_extra_allowlist entry: a
// repo-supplied betterleaks exemption, in exactly the shape
// githooks.BetterleaksAllowlistEntry needs. RuleID names which rule (base or
// extra) this narrows — "" or "*" applies across every rule, matching
// BetterleaksAllowlistEntry's own semantics. Exactly one of Value (an exact
// secret value) or Regex (a secret-value regex) is expected to be set; that
// requirement is enforced by githooks.ScanCredentials itself, not
// re-validated here.
type SecretScanExtraAllowlistEntry struct {
	RuleID string `koanf:"rule_id"`
	Value  string `koanf:"value"`
	Regex  string `koanf:"regex"`
}

// defaultConfig seeds koanf's lowest-precedence layer. Every key here must
// match a Config `koanf` tag and the normalized (hyphen->underscore) form
// of the matching persistent flag, so the same setting resolves to one key
// across default/file/env/flag layers.
func defaultConfig() map[string]interface{} {
	return map[string]interface{}{
		"repo":                        ".",
		"remote":                      "origin",
		"privacy_tier":                "public",
		"strict":                      false,
		"max_binary_bytes":            githooks.DefaultMaxBytes,
		"privacy_marker_exempt":       []string{},
		"secret_scan_exempt":          []string{},
		"allowed_email_domains":       []string{},
		"secret_scan_extra_rules":     []map[string]interface{}{},
		"secret_scan_extra_allowlist": []map[string]interface{}{},
	}
}

// normalizeKey maps a flag or env name onto its canonical snake_case koanf
// key, e.g. "privacy-tier" and "PRIVACY_TIER" both resolve to "privacy_tier".
func normalizeKey(s string) string {
	return strings.ReplaceAll(strings.ToLower(s), "-", "_")
}

// loadConfig resolves Config from, in increasing precedence: built-in
// defaults, an optional YAML file, the GITTOOLS_* environment, then fs's
// already-parsed flags. Each layer is loaded only — nothing here writes
// back to a file or the environment.
func loadConfig(fs *pflag.FlagSet) (*Config, error) {
	k := koanf.New(".")

	if err := k.Load(confmap.Provider(defaultConfig(), "."), nil); err != nil {
		return nil, fmt.Errorf("cli: load config defaults: %w", err)
	}

	if err := loadConfigFile(k, fs); err != nil {
		return nil, err
	}

	envProvider := env.Provider(envPrefix, ".", func(s string) string {
		return normalizeKey(strings.TrimPrefix(s, envPrefix))
	})
	if err := k.Load(envProvider, nil); err != nil {
		return nil, fmt.Errorf("cli: load config env: %w", err)
	}

	flagProvider := posflag.ProviderWithValue(fs, ".", k, func(key, value string) (string, interface{}) {
		return normalizeKey(key), value
	})
	if err := k.Load(flagProvider, nil); err != nil {
		return nil, fmt.Errorf("cli: load config flags: %w", err)
	}

	var cfg Config
	if err := k.Unmarshal("", &cfg); err != nil {
		return nil, fmt.Errorf("cli: unmarshal config: %w", err)
	}
	return &cfg, nil
}

// loadConfigFile loads the YAML config named by --config, or defaultConfigFile
// if present and --config was not given. An explicitly named file that does
// not exist is an error; an implicit default that does not exist is not. The
// implicit default is resolved against --repo's own target directory, not
// the invoking process's cwd — a caller running git-tools against a
// different repo than the one it happens to be sitting in must still pick up
// that repo's own git-tools.yaml, not silently fall back to no config (or,
// worse, an unrelated git-tools.yaml the cwd happens to carry).
func loadConfigFile(k *koanf.Koanf, fs *pflag.FlagSet) error {
	explicit := fs.Changed("config")
	path, _ := fs.GetString("config")
	if path == "" {
		path = filepath.Join(repoDirForConfig(fs), defaultConfigFile)
	}

	if _, err := os.Stat(path); err != nil {
		if explicit {
			return fmt.Errorf("cli: config file %s: %w", path, err)
		}
		return nil
	}

	if err := k.Load(file.Provider(path), yaml.Parser()); err != nil {
		return fmt.Errorf("cli: load config file %s: %w", path, err)
	}
	warnIfConfigTampered(path)
	return nil
}

// repoDirForConfig resolves --repo's own target directory using only what is
// available before loadConfigFile runs: fs's already-parsed flags and the
// GITTOOLS_REPO environment variable, in the same flag-beats-env-beats-
// default precedence loadConfig applies to every other setting. It cannot
// wait for the full koanf resolution in loadConfig, since that resolution
// itself needs loadConfigFile to run first.
func repoDirForConfig(fs *pflag.FlagSet) string {
	if fs.Changed("repo") {
		if v, err := fs.GetString("repo"); err == nil && v != "" {
			return v
		}
	}
	if v := os.Getenv(envPrefix + "REPO"); v != "" {
		return v
	}
	return "."
}

// warnIfConfigTampered emits a stderr warning naming path when the config
// file just loaded is untracked or differs from the version committed at
// HEAD — the same repository state a caller relying on git-tools' scan to
// gate a merge, push, or tag create should be told about, since either state
// means the exemptions just loaded were never reviewed as part of that
// commit history. It never blocks the operation: a caller wanting to fail
// closed on this needs a separate, explicit choice this flag set does not
// yet offer (see defaultConfigFile's own doc comment). Any failure to run
// git at all (path outside a repository, git missing, or the directory
// housing path is not the git-tools.yaml's own repo) is silently treated as
// "nothing to warn about" — this diagnostic is a courtesy on top of the
// scan, not a scan of its own that must itself fail loudly.
func warnIfConfigTampered(path string) {
	dir := filepath.Dir(path)
	rel := filepath.Base(path)
	res, err := sysops.Run(context.Background(), "git", []string{"status", "--porcelain", "--", rel}, sysops.Options{Dir: dir})
	if err != nil || res.ExitCode != 0 {
		return
	}
	status := strings.TrimSpace(string(res.Stdout))
	if status == "" {
		return
	}
	state := "locally modified (differs from the committed HEAD version)"
	if strings.HasPrefix(status, "??") {
		state = "untracked"
	}
	fmt.Fprintf(os.Stderr, "warning: config file %s is %s; an uncommitted or locally modified git-tools.yaml is in effect\n", path, state)
}
