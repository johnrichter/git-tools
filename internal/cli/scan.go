package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/johnrichter/claude-shared-tooling/go/githooks"
	"github.com/johnrichter/claude-shared-tooling/go/sysops"
)

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
			findings, err := githooks.ScanSecrets(cfg.Repo, githooks.DefaultSkipRules)
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
			findings, err := githooks.ScanRawBinary(cfg.Repo, candidates, githooks.DefaultSkipRules, cfg.MaxBinaryBytes, lfsRouteChecker(cmd.Context(), cfg.Repo))
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
		Example: "  git-tools scan privacy --privacy-tier datadog",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(cmd.Flags())
			if err != nil {
				return finishErr(cmd, nil, "internal.config.load_failed", "load configuration", err)
			}
			tier := githooks.PrivacyTier(cfg.PrivacyTier)
			if !tier.Known() {
				return finishUsage(cmd, nil, "usage.cli.invalid_privacy_tier", fmt.Sprintf("--privacy-tier %q is not one of public, datadog, personal", cfg.PrivacyTier))
			}
			failures, warnings, err := githooks.ScanPrivacy(cfg.Repo, tier, githooks.PrivacyOptions{SkipRules: githooks.DefaultSkipRules})
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
				return finishUsage(cmd, nil, "usage.cli.invalid_privacy_tier", fmt.Sprintf("--privacy-tier %q is not one of public, datadog, personal", cfg.PrivacyTier))
			}
			staged, _ := cmd.Flags().GetBool("staged")

			secrets, err := githooks.ScanSecrets(cfg.Repo, githooks.DefaultSkipRules)
			if err != nil {
				return finishErr(cmd, nil, "internal.githooks.scan_secrets_failed", "scan for secrets", err)
			}
			candidates, err := listCandidates(cmd.Context(), cfg.Repo, staged)
			if err != nil {
				return finishErr(cmd, nil, "internal.git.list_candidates_failed", "list candidate files", err)
			}
			rawBinary, err := githooks.ScanRawBinary(cfg.Repo, candidates, githooks.DefaultSkipRules, cfg.MaxBinaryBytes, lfsRouteChecker(cmd.Context(), cfg.Repo))
			if err != nil {
				return finishErr(cmd, nil, "internal.githooks.scan_raw_binary_failed", "scan for raw binaries", err)
			}
			failures, warnings, err := githooks.ScanPrivacy(cfg.Repo, tier, githooks.PrivacyOptions{SkipRules: githooks.DefaultSkipRules})
			if err != nil {
				return finishErr(cmd, nil, "internal.githooks.scan_privacy_failed", "scan for privacy violations", err)
			}

			return emitScan(cmd, githooks.ScanOutcome{
				Secrets:         secrets,
				RawBinary:       rawBinary,
				PrivacyFailures: failures,
				PrivacyWarnings: warnings,
				Strict:          cfg.Strict,
			})
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

// emitScan hands outcome to githooks' own result-builder, which produces the
// full clikit envelope (success/caveats/precondition_unmet) in one call.
func emitScan(cmd *cobra.Command, outcome githooks.ScanOutcome) error {
	code, err := githooks.EmitHookResult(cmd.OutOrStdout(), commandPath(cmd), outcome)
	if err != nil {
		return finishErr(cmd, nil, "internal.result.build_failed", "build scan result", err)
	}
	return finishCode(code)
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
