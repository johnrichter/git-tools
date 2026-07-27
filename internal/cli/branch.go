package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/johnrichter/claude-shared-tooling/go/git"
	gitresult "github.com/johnrichter/git-tools/internal/result"
)

func newBranchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "branch",
		Short: "Create and delete branches",
	}
	cmd.AddCommand(newBranchCreateCmd())
	cmd.AddCommand(newBranchDeleteCmd())
	return cmd
}

func newBranchCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "create <name> <start-point>",
		Short:   "Create name pointing at start-point",
		Args:    cobra.ExactArgs(2),
		Example: "  git-tools branch create feature/x origin/main",
		RunE: func(cmd *cobra.Command, args []string) error {
			name, startPoint := args[0], args[1]
			cfg, err := loadConfig(cmd.Flags())
			if err != nil {
				return finishErr(cmd, "internal.config.load_failed", "load configuration", err)
			}
			repo, repoErr := requireRepo(cmd, cfg)
			if repo == nil {
				return repoErr
			}

			force, _ := cmd.Flags().GetBool("force")
			dryRun, _ := cmd.Flags().GetBool("dry-run")

			if err := repo.CreateBranch(cmd.Context(), name, startPoint, git.BranchOptions{Force: force, DryRun: dryRun}); err != nil {
				return finishErr(cmd, "internal.git.branch_create_failed", fmt.Sprintf("create branch %s at %s", name, startPoint), err)
			}

			result, buildErr := clikitSuccess(cmd, map[string]any{"branch": name, "start_point": startPoint, "dry_run": dryRun})
			if buildErr != nil {
				return finishErr(cmd, "internal.result.build_failed", "build result", buildErr)
			}
			return finish(cmd, result)
		},
	}
	cmd.Flags().Bool("force", false, "move an existing branch of the same name instead of refusing")
	cmd.Flags().Bool("dry-run", false, "validate that start-point resolves without creating the branch")
	return cmd
}

func newBranchDeleteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "delete <name> <expected-head>",
		Short:   "Delete name as a compare-and-swap against expected-head, tagging expected-head for recovery first",
		Args:    cobra.ExactArgs(2),
		Example: "  git-tools branch delete feature/x $(git rev-parse feature/x)",
		RunE: func(cmd *cobra.Command, args []string) error {
			name, expectedHead := args[0], args[1]
			cfg, err := loadConfig(cmd.Flags())
			if err != nil {
				return finishErr(cmd, "internal.config.load_failed", "load configuration", err)
			}
			repo, repoErr := requireRepo(cmd, cfg)
			if repo == nil {
				return repoErr
			}

			dryRun, _ := cmd.Flags().GetBool("dry-run")

			outcome, err := repo.DeleteBranch(cmd.Context(), name, expectedHead, dryRun)
			if err != nil {
				return handleGitError(cmd, err, "internal.git.branch_delete_failed", fmt.Sprintf("delete branch %s", name))
			}

			result, buildErr := clikitSuccess(cmd, gitresult.RewriteOutcomeData(outcome))
			if buildErr != nil {
				return finishErr(cmd, "internal.result.build_failed", "build result", buildErr)
			}
			return finish(cmd, result)
		},
	}
	cmd.Flags().Bool("dry-run", false, "confirm the compare-and-swap without deleting the branch")
	return cmd
}
