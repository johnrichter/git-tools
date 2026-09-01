package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/spf13/cobra"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
	"github.com/johnrichter/claude-shared-tooling/go/fsx"
	"github.com/johnrichter/claude-shared-tooling/go/githooks"
	"github.com/johnrichter/claude-shared-tooling/go/sysops"
)

// betterleaksBinEnvVar names the environment variable holding a resolved,
// absolute path to the betterleaks binary the credential scan shells out to.
// A path, never a $PATH lookup: this fleet's own governance plugin is
// responsible for provisioning that binary and setting this variable — a
// separate provisioning task, not this one — so git-tools itself only reads
// it. Deliberately outside the GITTOOLS_* config-env prefix (envPrefix):
// unlike privacy_tier/secret_scan_exempt and the rest of Config, this is not
// a per-repo setting a git-tools.yaml or that prefix's env layer should ever
// resolve — it is a provisioned runtime dependency's location, the same kind
// of value an environment sets for a build tool's compiler path.
const betterleaksBinEnvVar = "GIT_TOOLS_BETTERLEAKS_BIN"

// errBetterleaksUnconfigured is scanCredentials' sentinel for "there is no
// working betterleaks binary to scan with": betterleaksBinEnvVar is unset, or
// it names a path that does not resolve to an existing file. Every caller
// checks for it with errors.Is and turns it into a precondition_unmet
// refusal instead of a generic internal failure — a missing credential
// scanner is a missing precondition for a security-relevant scan, not an
// unexpected fault, and unlike the old silent-skip behavior it must never let
// the scan, or whatever it gates, proceed as though nothing were wrong.
var errBetterleaksUnconfigured = errors.New("the credential scanner is not configured (" + betterleaksBinEnvVar + " does not resolve to an existing betterleaks binary)")

// resolveBetterleaksPath reads betterleaksBinEnvVar and confirms it names an
// existing file, returning errBetterleaksUnconfigured when it is unset or
// does not resolve — the credential scan is mandatory, so an unprovisioned
// binary is a real failure for the caller to refuse on, never a silent
// skip.
func resolveBetterleaksPath() (string, error) {
	path := os.Getenv(betterleaksBinEnvVar)
	if path == "" {
		return "", errBetterleaksUnconfigured
	}
	if _, err := os.Stat(path); err != nil {
		return "", errBetterleaksUnconfigured
	}
	return path, nil
}

// betterleaksExtraRules converts cfg's secret_scan_extra_rules entries into
// the githooks.BetterleaksRule slice githooks.ScanCredentials' opts.ExtraRules
// expects. Only ID and Regex carry through: githooks.BetterleaksRule has no
// Category field, so every betterleaks finding is still tagged "credentials"
// regardless of an extra rule's own Category (see Config.SecretScanExtraRules).
func betterleaksExtraRules(rules []SecretScanExtraRule) []githooks.BetterleaksRule {
	out := make([]githooks.BetterleaksRule, len(rules))
	for i, r := range rules {
		out[i] = githooks.BetterleaksRule{ID: r.ID, Regex: r.Regex}
	}
	return out
}

// betterleaksExtraAllowlist converts cfg's secret_scan_extra_allowlist
// entries into the githooks.BetterleaksAllowlistEntry slice
// githooks.ScanCredentials' opts.ExtraAllowlist expects.
func betterleaksExtraAllowlist(entries []SecretScanExtraAllowlistEntry) []githooks.BetterleaksAllowlistEntry {
	out := make([]githooks.BetterleaksAllowlistEntry, len(entries))
	for i, e := range entries {
		out[i] = githooks.BetterleaksAllowlistEntry{RuleID: e.RuleID, Value: e.Value, Regex: e.Regex}
	}
	return out
}

// scanCredentials runs the betterleaks-based scan over dir, using cfg's
// extra-rules/extra-allowlist config, and returns its findings unchanged for
// the caller to merge into a ScanOutcome's Secrets. It returns
// errBetterleaksUnconfigured, never invoking betterleaks at all, when
// resolveBetterleaksPath cannot resolve a usable binary — the scan is
// mandatory, so an unprovisioned binary is a failure for the caller to
// refuse on, not a reason to proceed as though nothing were scanned.
func scanCredentials(dir string, cfg *Config) ([]githooks.Finding, error) {
	path, err := resolveBetterleaksPath()
	if err != nil {
		return nil, err
	}
	return githooks.ScanCredentials(dir, path, githooks.BetterleaksOptions{
		SkipRules:      gitToolsSkipRules,
		ExtraRules:     betterleaksExtraRules(cfg.SecretScanExtraRules),
		ExtraAllowlist: betterleaksExtraAllowlist(cfg.SecretScanExtraAllowlist),
	})
}

// categoryCountKeys maps a Finding.Category value onto the ScanOutcome data
// key its count is reported under. Empty Category (every non-betterleaks,
// non-PII/financial finding kind) has no key here and is not counted.
var categoryCountKeys = map[string]string{
	"credentials": "credentials_found",
	"pii":         "pii_found",
	"financial":   "financial_found",
}

// addCategoryCounts sets outcome data's category-grouped keys
// (credentials_found, pii_found, financial_found) on result, computed from
// secrets — the merged Secrets slice a ScanOutcome carries once
// scanCredentials' findings are folded in. secrets_found (result.Data's
// existing, githooks-computed key) is untouched: it still reports the total
// regardless of category.
func addCategoryCounts(result *clikit.Result, secrets []githooks.Finding) {
	counts := map[string]int{"credentials_found": 0, "pii_found": 0, "financial_found": 0}
	for _, f := range secrets {
		if key, ok := categoryCountKeys[f.Category]; ok {
			counts[key]++
		}
	}
	if result.Data == nil {
		result.Data = map[string]any{}
	}
	for key, count := range counts {
		result.Data[key] = count
	}
}

// credentialScannerUnconfiguredDiagnostic builds and emits the
// precondition_unmet refusal every scanCredentials caller reports when
// errBetterleaksUnconfigured comes back: the credential scan could not run
// at all, so nothing downstream of it may proceed either. done names the
// past-tense effect nothing was, e.g. "scanned", "merged", "pushed".
func credentialScannerUnconfiguredDiagnostic(cmd *cobra.Command, data map[string]any, done string) error {
	return finishDiagnostic(cmd, data, clikit.NewPreconditionUnmet,
		"precondition_unmet.githooks.credential_scanner_unconfigured",
		fmt.Sprintf("%s; nothing was %s", errBetterleaksUnconfigured, done),
		clikit.Manual(fmt.Sprintf("provision a betterleaks binary and set %s to its path, then re-run; nothing was %s", betterleaksBinEnvVar, done)),
		nil)
}

// gitToolsSkipRules layers one git-tools-specific exclusion on top of
// githooks.DefaultSkipRules: nested Claude Code worktrees at
// .claude/worktrees/<slug>/, created at a repo's root by this fleet's own
// governance convention for isolated feature-branch work inside the primary
// checkout. That directory's content belongs to whatever unrelated,
// in-progress branch each nested worktree happens to be on — not to "this
// repo's tree" for scanning purposes — so every scanner skips it entirely,
// the same way DefaultSkipRules already skips .git internals. The pattern is
// anchored to the scanned root (no leading "**/") so it can only ever match
// the literal .claude/worktrees/ prefix, never an unrelated worktrees/
// directory that happens to be legitimately tracked elsewhere in the tree.
var gitToolsSkipRules = append(append([]fsx.Rule(nil), githooks.DefaultSkipRules...), fsx.Rule{
	Pattern: ".claude/worktrees/**",
	Class:   githooks.SkipClass,
})

// codeMarkerExemptSuffixes are source-file extensions exempt from the
// frontmatter-marker check by default, in every repo, with no config needed:
// each legitimately embeds a literal "privacy:"/"owner:" string as source
// text — a scanner's own pattern definition, a test's own fixture literal —
// never as that file's real sensitivity/owner declaration. The secret and
// internal-identifier checks still run on these files unchanged; only the
// marker check is exempt.
var codeMarkerExemptSuffixes = []string{
	".py", ".go", ".sh", ".bash", ".rb", ".js", ".ts", ".java", ".rs", ".c", ".h", ".cpp",
}

// codeMarkerExemptRules is codeMarkerExemptSuffixes rendered as the
// fsx.Rule ruleset privacyMarkerExemptRules always includes, ahead of any
// repo-configured privacy_marker_exempt prefix.
var codeMarkerExemptRules = func() []fsx.Rule {
	rules := make([]fsx.Rule, len(codeMarkerExemptSuffixes))
	for i, suffix := range codeMarkerExemptSuffixes {
		rules[i] = fsx.Rule{Pattern: "**/*" + suffix, Class: githooks.SkipClass}
	}
	return rules
}()

func newScanCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scan",
		Short: "Run the githooks content guardrails: secrets, raw-binary/LFS, and privacy",
	}
	cmd.AddCommand(newScanSecretsCmd())
	cmd.AddCommand(newScanLFSCmd())
	cmd.AddCommand(newScanPrivacyCmd())
	cmd.AddCommand(newScanAllCmd())
	return cmd
}

func newScanSecretsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "secrets",
		Short:   "Scan for plaintext secret signatures (private keys, cloud/VCS/chat tokens)",
		Args:    cobra.NoArgs,
		Example: "  git-tools scan secrets",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(cmd.Flags())
			if err != nil {
				return finishErr(cmd, nil, "internal.config.load_failed", "load configuration", err)
			}
			if bad, ok := malformedSecretScanExempt(cfg.SecretScanExempt); ok {
				return finishUsage(cmd, nil, "usage.cli.invalid_secret_scan_exempt", fmt.Sprintf("secret_scan_exempt entry %q is not a valid glob pattern", bad))
			}
			findings, err := githooks.ScanSecrets(cfg.Repo, gitToolsSkipRules, secretExemptRules(cfg.SecretScanExempt))
			if err != nil {
				return finishErr(cmd, nil, "internal.githooks.scan_secrets_failed", "scan for secrets", err)
			}
			credFindings, err := scanCredentials(cfg.Repo, cfg)
			if err != nil {
				if errors.Is(err, errBetterleaksUnconfigured) {
					return credentialScannerUnconfiguredDiagnostic(cmd, nil, "scanned")
				}
				return finishErr(cmd, nil, "internal.githooks.scan_credentials_failed", "scan for credentials", err)
			}
			return emitScan(cmd, githooks.ScanOutcome{
				Secrets: append(findings, credFindings...),
			})
		},
	}
	return cmd
}

func newScanLFSCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "lfs",
		Short:   "Scan for raw (non-LFS) binaries over the configured size threshold",
		Args:    cobra.NoArgs,
		Example: "  git-tools scan lfs --staged",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(cmd.Flags())
			if err != nil {
				return finishErr(cmd, nil, "internal.config.load_failed", "load configuration", err)
			}
			staged, _ := cmd.Flags().GetBool("staged")
			candidates, err := listCandidates(cmd.Context(), cfg.Repo, staged)
			if err != nil {
				return finishErr(cmd, nil, "internal.git.list_candidates_failed", "list candidate files", err)
			}
			findings, err := githooks.ScanRawBinary(cfg.Repo, candidates, gitToolsSkipRules, cfg.MaxBinaryBytes, lfsRouteChecker(cmd.Context(), cfg.Repo))
			if err != nil {
				return finishErr(cmd, nil, "internal.githooks.scan_raw_binary_failed", "scan for raw binaries", err)
			}
			return emitScan(cmd, githooks.ScanOutcome{RawBinary: findings})
		},
	}
	cmd.Flags().Bool("staged", false, "scan only staged files (git diff --cached) instead of the full tracked tree (git ls-files)")
	return cmd
}

func newScanPrivacyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "privacy",
		Short:   "Scan for forbidden privacy markers and internal-identifier mentions",
		Args:    cobra.NoArgs,
		Example: "  git-tools scan privacy --privacy-tier confidential",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(cmd.Flags())
			if err != nil {
				return finishErr(cmd, nil, "internal.config.load_failed", "load configuration", err)
			}
			tier := githooks.PrivacyTier(cfg.PrivacyTier)
			if !tier.Known() {
				return finishUsage(cmd, nil, "usage.cli.invalid_privacy_tier", fmt.Sprintf("--privacy-tier %q is not one of public, confidential, private", cfg.PrivacyTier))
			}
			if bad, ok := malformedPrivacyMarkerExempt(cfg.PrivacyMarkerExempt); ok {
				return finishUsage(cmd, nil, "usage.cli.invalid_privacy_marker_exempt", fmt.Sprintf("privacy_marker_exempt entry %q is not a valid glob pattern", bad))
			}
			if bad, ok := malformedSecretScanExempt(cfg.SecretScanExempt); ok {
				return finishUsage(cmd, nil, "usage.cli.invalid_secret_scan_exempt", fmt.Sprintf("secret_scan_exempt entry %q is not a valid glob pattern", bad))
			}
			failures, warnings, err := githooks.ScanPrivacy(cfg.Repo, tier, githooks.PrivacyOptions{
				SkipRules:         gitToolsSkipRules,
				MarkerExemptRules: privacyMarkerExemptRules(cfg.PrivacyMarkerExempt),
				SecretExemptRules: secretExemptRules(cfg.SecretScanExempt),
				EmployeeEmail:     employeeEmailCheck(cfg),
			})
			if err != nil {
				return finishErr(cmd, nil, "internal.githooks.scan_privacy_failed", "scan for privacy violations", err)
			}
			return emitScan(cmd, githooks.ScanOutcome{PrivacyFailures: failures, PrivacyWarnings: warnings, Strict: cfg.Strict})
		},
	}
	return cmd
}

func newScanAllCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "all",
		Short:   "Run every scanner and emit one combined result — what an installed hook runs",
		Args:    cobra.NoArgs,
		Example: "  git-tools scan all --staged --strict",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(cmd.Flags())
			if err != nil {
				return finishErr(cmd, nil, "internal.config.load_failed", "load configuration", err)
			}
			tier := githooks.PrivacyTier(cfg.PrivacyTier)
			if !tier.Known() {
				return finishUsage(cmd, nil, "usage.cli.invalid_privacy_tier", fmt.Sprintf("--privacy-tier %q is not one of public, confidential, private", cfg.PrivacyTier))
			}
			if bad, ok := malformedPrivacyMarkerExempt(cfg.PrivacyMarkerExempt); ok {
				return finishUsage(cmd, nil, "usage.cli.invalid_privacy_marker_exempt", fmt.Sprintf("privacy_marker_exempt entry %q is not a valid glob pattern", bad))
			}
			if bad, ok := malformedSecretScanExempt(cfg.SecretScanExempt); ok {
				return finishUsage(cmd, nil, "usage.cli.invalid_secret_scan_exempt", fmt.Sprintf("secret_scan_exempt entry %q is not a valid glob pattern", bad))
			}
			staged, _ := cmd.Flags().GetBool("staged")

			outcome, err := scanTree(cmd.Context(), cfg.Repo, cfg, staged)
			if err != nil {
				if errors.Is(err, errBetterleaksUnconfigured) {
					return credentialScannerUnconfiguredDiagnostic(cmd, nil, "scanned")
				}
				return finishErr(cmd, nil, "internal.githooks.scan_failed", "run the content scan", err)
			}
			return emitScan(cmd, outcome)
		},
	}
	// --staged narrows only the raw-binary/LFS check, which is the one scan
	// that must resolve per-file LFS routing (a `git check-attr` call per
	// candidate) and so benefits from a smaller candidate list. Secrets and
	// privacy always scan the full tracked tree regardless of this flag: an
	// already-committed secret or privacy marker is still a live finding on
	// every commit, not just the one that introduced it.
	cmd.Flags().Bool("staged", false, "scan only staged files for the raw-binary check instead of the full tracked tree")
	return cmd
}

// malformedPrivacyMarkerExempt reports the first configured
// privacy_marker_exempt entry that does not compile as a doublestar glob, so
// a caller can refuse the invocation outright. fsx.ClassifyPath treats an
// unparseable pattern as an always-match rather than dropping it — correct
// for a SkipRules-style ruleset, where "matched" means "be careful here", but
// backwards for MarkerExemptRules, where "matched" means "skip the marker
// check": a malformed entry would silently exempt every path in the repo
// instead of none, so it must fail loudly here instead of ever reaching
// ClassifyPath.
func malformedPrivacyMarkerExempt(prefixes []string) (string, bool) {
	for _, p := range prefixes {
		if !doublestar.ValidatePattern(strings.TrimSuffix(p, "/")) {
			return p, true
		}
	}
	return "", false
}

// privacyMarkerExemptRules converts cfg's privacy_marker_exempt path-prefix
// list into the fsx.Rule ruleset githooks.PrivacyOptions.MarkerExemptRules
// expects, ahead of which it always includes codeMarkerExemptRules: a source
// file is marker-exempt in every repo, with no config needed, regardless of
// what privacy_marker_exempt names. Each configured prefix exempts both
// itself, as a single named file, and everything under it, as a directory —
// a consuming repo's test/eval fixture path can name either shape without
// knowing which glob githooks needs. This only ever feeds MarkerExemptRules,
// never SkipRules: an exempt path still gets the secret and
// internal-identifier scan, just not the frontmatter-marker check.
func privacyMarkerExemptRules(prefixes []string) []fsx.Rule {
	rules := append([]fsx.Rule(nil), codeMarkerExemptRules...)
	for _, p := range prefixes {
		trimmed := strings.TrimSuffix(p, "/")
		rules = append(rules,
			fsx.Rule{Pattern: trimmed, Class: githooks.SkipClass},
			fsx.Rule{Pattern: trimmed + "/**", Class: githooks.SkipClass},
		)
	}
	return rules
}

// malformedSecretScanExempt reports the first configured secret_scan_exempt
// entry that does not compile as a doublestar glob, so a caller can refuse
// the invocation outright rather than let fsx.ClassifyPath's fail-closed
// treatment of a broken pattern quietly hand the (unrelated, SkipRules-style)
// meaning of "match everything" to a rule meant to name one exact file. See
// malformedPrivacyMarkerExempt for the same failure this mirrors.
func malformedSecretScanExempt(paths []string) (string, bool) {
	for _, p := range paths {
		if !doublestar.ValidatePattern(p) {
			return p, true
		}
	}
	return "", false
}

// secretExemptRules converts cfg's secret_scan_exempt entries into the
// fsx.Rule ruleset githooks.ScanSecrets' secretExemptRules parameter and
// githooks.PrivacyOptions.SecretExemptRules both expect. Unlike
// privacyMarkerExemptRules, each entry is used verbatim, with no implicit
// "+ /**" directory expansion: bypassing secret detection is a higher-risk
// exemption than skipping the privacy-marker check, so naming one file must
// exempt only that file. A config author who does want a whole directory has
// to write that glob out explicitly, rather than have the scope silently
// widen past the path they named.
func secretExemptRules(paths []string) []fsx.Rule {
	var rules []fsx.Rule
	for _, p := range paths {
		rules = append(rules, fsx.Rule{Pattern: p, Class: githooks.SkipClass})
	}
	return rules
}

// employeeEmailCheck converts cfg's allowed_email_domains entries into the
// githooks.EmployeeEmailCheck PrivacyOptions.EmployeeEmail expects. The
// public tier's employee-email check always runs; this only widens which
// domains it exempts beyond githooks' own hardcoded example.com default, for
// exactly the domains a repo's own git-tools.yaml names. With none
// configured — the default — it returns the zero value, and only
// example.com stays exempt: this CLI serves any repo and ships as public
// source, so it holds no organization's domains of its own to fall back on.
func employeeEmailCheck(cfg *Config) githooks.EmployeeEmailCheck {
	return githooks.EmployeeEmailCheck{AllowedDomains: cfg.AllowedEmailDomains}
}

// emitScan hands outcome to githooks' own result-builder, which produces the
// full clikit envelope (success/caveats/precondition_unmet), adds this CLI's
// own category-grouped counts (see addCategoryCounts) on top, then emits it.
func emitScan(cmd *cobra.Command, outcome githooks.ScanOutcome) error {
	result, err := githooks.BuildHookResult(commandPath(cmd), outcome)
	if err != nil {
		return finishErr(cmd, nil, "internal.result.build_failed", "build scan result", err)
	}
	addCategoryCounts(result, outcome.Secrets)
	if err := clikit.Emit(cmd.OutOrStdout(), result); err != nil {
		return finishErr(cmd, nil, "internal.result.build_failed", "build scan result", err)
	}
	return finishCode(result.ExitCode)
}

// scanTree runs every scanner over dir and folds the results into one
// ScanOutcome — the single computation "scan all" and scanGate share, so a
// rule added to any of the three underlying scanners covers every caller
// without a second copy of the sequence to keep in sync. staged narrows only
// the raw-binary candidate list to git's staged diff instead of the full
// tracked tree; secrets and privacy always scan the full tracked tree
// regardless (see newScanAllCmd's --staged flag help for why).
func scanTree(ctx context.Context, dir string, cfg *Config, staged bool) (githooks.ScanOutcome, error) {
	secrets, err := githooks.ScanSecrets(dir, gitToolsSkipRules, secretExemptRules(cfg.SecretScanExempt))
	if err != nil {
		return githooks.ScanOutcome{}, fmt.Errorf("scan for secrets: %w", err)
	}
	credFindings, err := scanCredentials(dir, cfg)
	if err != nil {
		return githooks.ScanOutcome{}, fmt.Errorf("scan for credentials: %w", err)
	}
	secrets = append(secrets, credFindings...)
	candidates, err := listCandidates(ctx, dir, staged)
	if err != nil {
		return githooks.ScanOutcome{}, fmt.Errorf("list candidate files: %w", err)
	}
	rawBinary, err := githooks.ScanRawBinary(dir, candidates, gitToolsSkipRules, cfg.MaxBinaryBytes, lfsRouteChecker(ctx, dir))
	if err != nil {
		return githooks.ScanOutcome{}, fmt.Errorf("scan for raw binaries: %w", err)
	}
	failures, warnings, err := githooks.ScanPrivacy(dir, githooks.PrivacyTier(cfg.PrivacyTier), githooks.PrivacyOptions{
		SkipRules:         gitToolsSkipRules,
		MarkerExemptRules: privacyMarkerExemptRules(cfg.PrivacyMarkerExempt),
		SecretExemptRules: secretExemptRules(cfg.SecretScanExempt),
		EmployeeEmail:     employeeEmailCheck(cfg),
	})
	if err != nil {
		return githooks.ScanOutcome{}, fmt.Errorf("scan for privacy violations: %w", err)
	}
	return githooks.ScanOutcome{
		Secrets:         secrets,
		RawBinary:       rawBinary,
		PrivacyFailures: failures,
		PrivacyWarnings: warnings,
		Strict:          cfg.Strict,
	}, nil
}

// scanGate runs scanTree over dir's full tracked tree — the content
// guardrails an installed pre-commit hook already applies to every commit —
// and refuses with a precondition_unmet result on any finding: merge, push
// and rebase each call this one entry point before they act, rather than
// each carrying its own copy of the scan, so a rule scanTree gains covers
// all three write verbs at once. It scans the full tree rather than a staged
// diff: none of the three write verbs acts against a pending index change,
// they act on committed history that is about to be landed, published, or
// replayed. verb names the action being gated ("merge", "push", "rebase")
// for the refusal's remedy text; data seeds the refusal's result data. It
// returns nil once the tree scans clean.
func scanGate(cmd *cobra.Command, cfg *Config, dir, verb string, data map[string]any) error {
	tier := githooks.PrivacyTier(cfg.PrivacyTier)
	if !tier.Known() {
		return finishUsage(cmd, data, "usage.cli.invalid_privacy_tier", fmt.Sprintf("--privacy-tier %q is not one of public, confidential, private", cfg.PrivacyTier))
	}
	if bad, ok := malformedPrivacyMarkerExempt(cfg.PrivacyMarkerExempt); ok {
		return finishUsage(cmd, data, "usage.cli.invalid_privacy_marker_exempt", fmt.Sprintf("privacy_marker_exempt entry %q is not a valid glob pattern", bad))
	}
	if bad, ok := malformedSecretScanExempt(cfg.SecretScanExempt); ok {
		return finishUsage(cmd, data, "usage.cli.invalid_secret_scan_exempt", fmt.Sprintf("secret_scan_exempt entry %q is not a valid glob pattern", bad))
	}

	outcome, err := scanTree(cmd.Context(), dir, cfg, false)
	if err != nil {
		if errors.Is(err, errBetterleaksUnconfigured) {
			return credentialScannerUnconfiguredDiagnostic(cmd, data, pastTense(verb))
		}
		return finishErr(cmd, data, "internal.githooks.scan_failed", "run the content scan", err)
	}
	result, err := githooks.BuildHookResult(commandPath(cmd), outcome)
	if err != nil {
		return finishErr(cmd, data, "internal.result.build_failed", "build scan result", err)
	}
	if result.Status != clikit.StatusPreconditionUnmet {
		return nil
	}
	finding, ok := result.Governing()
	if !ok {
		return nil
	}
	path, _ := finding.Context["path"].(string)
	rule, _ := finding.Context["rule"].(string)
	// category is "" for every finding kind outside the credentials/pii/
	// financial taxonomy (see githooks' Finding.Category) — carried through
	// only when the governing finding's own context already names one.
	//
	// Known gap, deliberately deferred: the governing finding is whichever
	// error BuildHookResult emits first, and scanTree appends the categorized
	// credential findings behind the uncategorized ScanSecrets ones, so a tree
	// tripping both reports "" here even though a categorized finding sits
	// further down the same result. A refusal collapses to this one
	// diagnostic, so unlike the scan subcommands — which carry every finding's
	// own category plus the per-category counts — the gate has no second place
	// to read the taxonomy from. Closing it means ordering findings by
	// category before choosing the governing one, which changes which path
	// every existing refusal names; that is its own task, not this one's.
	category, _ := finding.Context["category"].(string)
	return finishDiagnostic(cmd, data, clikit.NewPreconditionUnmet, finding.Code, finding.Message,
		clikit.Manual(fmt.Sprintf("fix or remove the flagged content at %s and re-run; nothing was %s", path, pastTense(verb))),
		map[string]any{"path": path, "rule": rule, "findings": len(result.Errors), "category": category})
}

// pastTense renders verb ("merge", "push", "rebase", "tag") as the participle
// a scanGate refusal reports nothing was.
func pastTense(verb string) string {
	switch verb {
	case "push":
		return "pushed"
	case "tag":
		return "tagged"
	default:
		return verb + "d"
	}
}

// listCandidates enumerates the files a scan should consider: the full
// tracked tree, or (staged) only what's staged for commit.
func listCandidates(ctx context.Context, dir string, staged bool) ([]string, error) {
	args := []string{"ls-files"}
	if staged {
		args = []string{"diff", "--cached", "--name-only", "--diff-filter=ACM"}
	}
	res, err := sysops.Run(ctx, "git", args, sysops.Options{Dir: dir})
	if err != nil {
		return nil, fmt.Errorf("list candidates: %w", err)
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("git %s: exit %d: %s", strings.Join(args, " "), res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	out := strings.TrimSpace(string(res.Stdout))
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// lfsRouteChecker builds the LFSRouteChecker ScanRawBinary needs from
// `git check-attr`, run against dir.
func lfsRouteChecker(ctx context.Context, dir string) githooks.LFSRouteChecker {
	return func(rel string) (bool, error) {
		res, err := sysops.Run(ctx, "git", []string{"check-attr", "filter", "--", rel}, sysops.Options{Dir: dir})
		if err != nil {
			return false, fmt.Errorf("check LFS routing for %s: %w", rel, err)
		}
		return strings.Contains(string(res.Stdout), "filter: lfs"), nil
	}
}
