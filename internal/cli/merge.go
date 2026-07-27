package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/johnrichter/claude-shared-tooling/go/git"
)

func newMergeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "merge <branch>...",
		Short:   "Merge one or more branches into the currently checked-out branch (two or more performs an octopus merge)",
		Args:    cobra.MinimumNArgs(1),
		Example: "  git-tools merge --message \"merge release\" release/1.2",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(cmd.Flags())
			if err != nil {
				return finishErr(cmd, "internal.config.load_failed", "load configuration", err)
			}
			repo, repoErr := requireRepo(cmd, cfg)
			if repo == nil {
				return repoErr
			}

			message, _ := cmd.Flags().GetString("message")
			ffMode, _ := cmd.Flags().GetString("fast-forward")
			dryRun, _ := cmd.Flags().GetBool("dry-run")

			ff, err := parseFastForward(ffMode)
			if err != nil {
				return finishUsage(cmd, "usage.cli.invalid_fast_forward", err.Error())
			}

			result, err := repo.Merge(cmd.Context(), args, git.MergeOptions{Message: message, FastForward: ff, DryRun: dryRun})
			if err != nil {
				return handleGitError(cmd, err, "internal.git.merge_failed", fmt.Sprintf("merge %s", strings.Join(args, " ")))
			}

			data := map[string]any{"dry_run": result.DryRun}
			if result.DryRun {
				data["would_merge"] = result.WouldMerge
			} else {
				data["new_head"] = result.NewHead
			}
			clikitResult, buildErr := clikitSuccess(cmd, data)
			if buildErr != nil {
				return finishErr(cmd, "internal.result.build_failed", "build result", buildErr)
			}
			return finish(cmd, clikitResult)
		},
	}
	cmd.Flags().String("message", "", "commit message for a real (non-fast-forward) merge")
	cmd.Flags().String("fast-forward", "allow", "fast-forward behavior: allow, never, or only")
	cmd.Flags().Bool("dry-run", false, "merge into the index and report clean-mergeability, always aborting afterward")
	return cmd
}

// parseFastForward maps the --fast-forward flag's string value onto
// git.FastForward.
func parseFastForward(s string) (git.FastForward, error) {
	switch s {
	case "allow":
		return git.FastForwardAllow, nil
	case "never":
		return git.FastForwardNever, nil
	case "only":
		return git.FastForwardOnly, nil
	default:
		return 0, fmt.Errorf("--fast-forward %q is not one of allow, never, only", s)
	}
}
