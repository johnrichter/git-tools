package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
	"github.com/spf13/cobra"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
	"github.com/johnrichter/claude-shared-tooling/go/fsx"
	"github.com/johnrichter/claude-shared-tooling/go/githooks"
	"github.com/johnrichter/claude-shared-tooling/go/sysops"
)

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
			return emitScan(cmd, githooks.ScanOutcome{Secrets: findings})
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

// employeeEmailCheck converts cfg's employee_email_domains and
// employee_email_allowlist entries into the githooks.EmployeeEmailCheck
// PrivacyOptions.EmployeeEmail expects, turning the public tier's optional
// employee-email check on for exactly the domains a repo's own git-tools.yaml
// names. With no domains configured — the default — it returns the zero value
// and the check stays off, the same posture githooks itself ships: this CLI
// serves any repo and ships as public source, so it holds no organization's
// domains of its own to fall back on.
func employeeEmailCheck(cfg *Config) githooks.EmployeeEmailCheck {
	check := githooks.EmployeeEmailCheck{Domains: cfg.EmployeeEmailDomains}
	if len(cfg.EmployeeEmailAllowlist) == 0 {
		return check
	}
	check.Allowlist = make(map[string]bool, len(cfg.EmployeeEmailAllowlist))
	for _, addr := range cfg.EmployeeEmailAllowlist {
		check.Allowlist[addr] = true
	}
	return check
}

// emitScan hands outcome to githooks' own result-builder, which produces the
// full clikit envelope (success/caveats/precondition_unmet) in one call.
func emitScan(cmd *cobra.Command, outcome githooks.ScanOutcome) error {
	code, err := githooks.EmitHookResult(cmd.OutOrStdout(), commandPath(cmd), outcome)
	if err != nil {
		return finishErr(cmd, nil, "internal.result.build_failed", "build scan result", err)
	}
	return finishCode(code)
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
	return finishDiagnostic(cmd, data, clikit.NewPreconditionUnmet, finding.Code, finding.Message,
		clikit.Manual(fmt.Sprintf("fix or remove the flagged content at %s and re-run; nothing was %s", path, pastTense(verb))),
		map[string]any{"path": path, "rule": rule, "findings": len(result.Errors)})
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
