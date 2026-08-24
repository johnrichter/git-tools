package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/johnrichter/claude-shared-tooling/go/git"
	gitresult "github.com/johnrichter/git-tools/internal/result"
)

func newRebaseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "rebase <upstream>",
		Short:   "Replay the currently checked-out branch's commits ahead of upstream",
		Args:    cobra.ExactArgs(1),
		Example: "  git-tools rebase origin/main --preserve-merges",
		RunE: func(cmd *cobra.Command, args []string) error {
			upstream := args[0]
			cfg, err := loadConfig(cmd.Flags())
			if err != nil {
				return finishErr(cmd, nil, "internal.config.load_failed", "load configuration", err)
			}
			repo, repoErr := requireRepo(cmd, cfg)
			if repo == nil {
				return repoErr
			}

			// The content-guardrail scan runs before the rebase touches
			// anything: a rebase rewrites the checked-out branch in place,
			// so a refusal here is what keeps a flagged commit from ever
			// being replayed.
			if err := scanGate(cmd, cfg, repo.Dir, "rebase", nil); err != nil {
				return err
			}

			onto, _ := cmd.Flags().GetString("onto")
			preserveMerges, _ := cmd.Flags().GetBool("preserve-merges")
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			pushForceWithLease, _ := cmd.Flags().GetBool("push-force-with-lease")
			sync := git.SyncLocalOnly
			if pushForceWithLease {
				sync = git.SyncEmitForceWithLease
			}

			result, err := repo.Rebase(cmd.Context(), upstream, git.RebaseOptions{
				Onto:           onto,
				PreserveMerges: preserveMerges,
				DryRun:         dryRun,
				Sync:           sync,
				Remote:         cfg.Remote,
			})
			if err != nil {
				return handleGitError(cmd, nil, err, "internal.git.rebase_failed", fmt.Sprintf("rebase onto %s", upstream))
			}

			data := gitresult.RewriteOutcomeData(result.RewriteOutcome)
			if len(result.Commits) > 0 {
				data["commits"] = result.Commits
			}
			clikitResult, buildErr := clikitSuccess(cmd, data)
			if buildErr != nil {
				return finishErr(cmd, nil, "internal.result.build_failed", "build result", buildErr)
			}
			return finish(cmd, clikitResult)
		},
	}
	cmd.Flags().String("onto", "", "replay onto a different base than upstream (git rebase --onto)")
	cmd.Flags().Bool("preserve-merges", false, "keep merge commits instead of linearizing history")
	cmd.Flags().Bool("dry-run", false, "report the commits that would be replayed without running anything")
	cmd.Flags().Bool("push-force-with-lease", false, "report the force-with-lease push argv for a ref that is already shared with a remote (never pushes itself)")
	return cmd
}
