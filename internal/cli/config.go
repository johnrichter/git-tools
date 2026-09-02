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
//
// An explicit --config widens that same residual in one way worth naming: a
// file outside any repository gets no warning at all, since
// warnIfConfigTampered's `git status` runs in that file's own directory and
// fails there, which it treats as nothing to report. The worktree gate no
// longer denies `merge --config <path>` from a primary checkout either (it
// never should have -- see sc15Retargets), so this is the reachable shape of
// the residual. It is bounded by the scan exemptions the schema exposes and
// never by which repository a verb acts on: loadConfigForDir pins that to
// repoDirForConfig regardless of any file.
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
	// SecretScanExtraAllowlist are a repo's own additional exemptions, layered
	// on top of the compiled-in base allowlist the same way
	// SecretScanExtraRules layers onto the base ruleset. One config key feeds
	// both scanners: githooks.ScanCredentials' opts.ExtraAllowlist (the
	// betterleaks findings) and githooks.ScanSecrets' extraAllowlist
	// parameter (the hand-rolled private_key_block/aws_access_key_id/
	// slack_token/github_token patterns) both take the identical
	// []githooks.BetterleaksAllowlistEntry shape this converts to.
	SecretScanExtraAllowlist []SecretScanExtraAllowlistEntry `koanf:"secret_scan_extra_allowlist"`
	// SecretScanCategorizedSeverity governs whether a categorized
	// (credentials/pii/financial) betterleaks finding hard-blocks or only
	// caveats a scan/merge/push/rebase/tag create — githooks.ScanOutcome.
	// WarnOnCategorizedSecrets is set from this on every ScanOutcome this CLI
	// builds. Exactly "warn" or "block"; validated the same way PrivacyTier
	// is, before any scan runs. Defaults to "warn": paired with the
	// credential scan now being mandatory (see errBetterleaksUnconfigured), a
	// fleet rollout with no per-repo config gets scanned everywhere without
	// also hard-blocking on day one — a repo opts into "block" once its own
	// findings are clean. Never governs an uncategorized (empty-Category)
	// finding, which always hard-blocks regardless of this setting.
	SecretScanCategorizedSeverity string `koanf:"secret_scan_categorized_severity"`
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
//
// "repo" is deliberately absent: it is the one setting koanf does not resolve
// at all. repoDirForConfig owns it end to end, default included, and
// loadConfigForDir assigns its answer over whatever the layers produced --
// see that assignment for why a config file must not be able to select the
// repository a verb acts on.
func defaultConfig() map[string]interface{} {
	return map[string]interface{}{
		"remote":                           "origin",
		"privacy_tier":                     "public",
		"strict":                           false,
		"max_binary_bytes":                 githooks.DefaultMaxBytes,
		"privacy_marker_exempt":            []string{},
		"secret_scan_exempt":               []string{},
		"allowed_email_domains":            []string{},
		"secret_scan_extra_rules":          []map[string]interface{}{},
		"secret_scan_extra_allowlist":      []map[string]interface{}{},
		"secret_scan_categorized_severity": "warn",
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
	return loadConfigForDir(fs, repoDirForConfig(fs), true)
}

// loadConfigForDir resolves Config exactly as loadConfig does, except the
// implicit default config file (--config not given) is resolved against dir
// rather than --repo's own target directory. This is how a caller judges a
// prospective tree — e.g. a trial merge's scratch worktree — by that tree's
// own git-tools.yaml rather than the real repository's: an explicit --config
// still names one fixed file regardless of dir, exactly as loadConfig itself
// treats it.
//
// warnIfTampered is what loadConfig always passes true and a prospective-tree
// caller always passes false. That warning reports a config file an operator
// can act on, in a checkout they can look at; a machine-built prospective
// tree is neither. Its git-tools.yaml differs from that tree's own HEAD
// precisely because the trial merge under test put it there, which is the
// arrangement being judged rather than tampering, so warning there would fire
// on every merge that touches the file and name a scratch path already
// deleted by the time anyone read it. The operator's own checkout still gets
// the warning, from loadConfig itself.
func loadConfigForDir(fs *pflag.FlagSet, dir string, warnIfTampered bool) (*Config, error) {
	k := koanf.New(".")

	if err := k.Load(confmap.Provider(defaultConfig(), "."), nil); err != nil {
		return nil, fmt.Errorf("cli: load config defaults: %w", err)
	}

	if err := loadConfigFile(k, fs, dir, warnIfTampered); err != nil {
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
	// The acting repository comes from repoDirForConfig alone -- the --repo
	// flag, GITTOOLS_REPO, or "." -- never from a config file. A YAML "repo"
	// key parses into the layers above like any other, so it is overwritten
	// here rather than left to select which repository a verb writes to.
	//
	// The worktree gate depends on exactly this. sc15Retargets treats --repo
	// as the ONLY flag that retargets a sanctioned landing verb, and
	// gitToolsDestinations reads only --repo's value as a destination
	// (worktree-gate/detect/decide.go). Were a config file able to set "repo",
	// `merge --config <file>` would be sanctioned from a primary checkout
	// while acting on a repository the gate never saw and never judged, and
	// the implicit git-tools.yaml would do the same with no flag at all. This
	// assignment is what makes both classifiers' premise true, so it has to
	// survive: TestLoadConfig_ConfigFileCannotSelectTheRepo pins it.
	//
	// Nothing legitimate is lost. A repository's own git-tools.yaml naming a
	// different repository for every verb to act on has no use case, and an
	// explicit --config names a policy file, which is a separate concern from
	// which tree that policy is applied to.
	cfg.Repo = repoDirForConfig(fs)
	return &cfg, nil
}

// loadConfigFile loads the YAML config named by --config, or defaultConfigFile
// if present and --config was not given. An explicitly named file that does
// not exist is an error; an implicit default that does not exist is not. The
// implicit default is resolved against implicitDefaultDir, not the invoking
// process's cwd — a caller running git-tools against a different repo than
// the one it happens to be sitting in must still pick up that repo's own
// git-tools.yaml, not silently fall back to no config (or, worse, an
// unrelated git-tools.yaml the cwd happens to carry). An explicit --config
// ignores implicitDefaultDir entirely: it names one fixed file, and that
// file's identity has nothing to do with which directory a caller passes.
// warnIfTampered gates the tampered-config warning only; see loadConfigForDir
// for which callers suppress it and why.
func loadConfigFile(k *koanf.Koanf, fs *pflag.FlagSet, implicitDefaultDir string, warnIfTampered bool) error {
	explicit := fs.Changed("config")
	path, _ := fs.GetString("config")
	if path == "" {
		path = filepath.Join(implicitDefaultDir, defaultConfigFile)
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
	if warnIfTampered {
		warnIfConfigTampered(path)
	}
	return nil
}

// repoDirForConfig resolves --repo's own target directory using only what is
// available before loadConfigFile runs: fs's already-parsed flags and the
// GITTOOLS_REPO environment variable, in the same flag-beats-env-beats-
// default precedence loadConfig applies to every other setting. It cannot
// wait for the full koanf resolution in loadConfig, since that resolution
// itself needs loadConfigFile to run first.
//
// It is also the CLI's only answer to "which repository does this verb act
// on": loadConfigForDir assigns it over Config.Repo, so no config-file layer
// can move a verb onto a different repository. See that assignment.
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
