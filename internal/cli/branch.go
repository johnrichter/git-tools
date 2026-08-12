package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
	"github.com/johnrichter/claude-shared-tooling/go/git"
	gitresult "github.com/johnrichter/git-tools/internal/result"
	"github.com/johnrichter/git-tools/internal/worktreeclean"
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
		Use:   "delete <name> [expected-head]",
		Short: "Delete name as a compare-and-swap, refusing to discard commits unreachable from a landing target",
		Long: `delete removes name as a compare-and-swap against expected-head, tagging
the old head for recovery first. expected-head may be omitted, in which case
delete resolves name's current head itself so the compare-and-swap still has a
concrete value to check against; an expected-head that is supplied but no
longer current still fails that check.

Before touching the ref, delete proves name carries no commit unreachable from
a landing target, using local refs only: --landing-target if given, else
name's upstream, else the local record of the remote's default branch. There
is no override -- land the branch first, or point --landing-target at a ref
that already contains it.

Exit codes:
  0  success              name was deleted (or, on --dry-run, confirmed deletable)
  30 precondition_unmet   the landing target is unresolvable, or name carries
                           commits unreachable from it; the ref did not move
  40 not_found            --repo is not a git working tree, or name does not exist
  41 conflict             expected-head is stale; the compare-and-swap refused
  90 internal             an underlying git command failed unexpectedly`,
		Args:    cobra.RangeArgs(1, 2),
		Example: "  git-tools branch delete feature/x $(git rev-parse feature/x)",
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			cfg, err := loadConfig(cmd.Flags())
			if err != nil {
				return finishErr(cmd, "internal.config.load_failed", "load configuration", err)
			}
			repo, repoErr := requireRepo(cmd, cfg)
			if repo == nil {
				return repoErr
			}

			dryRun, _ := cmd.Flags().GetBool("dry-run")
			landingTarget, _ := cmd.Flags().GetString("landing-target")

			expectedHead := ""
			if len(args) == 2 {
				expectedHead = args[1]
			} else {
				head, ok, resolveErr := worktreeclean.RevParseLocal(cmd.Context(), repo.Dir, "refs/heads/"+name)
				if resolveErr != nil {
					return finishErr(cmd, "internal.git.branch_head_resolve_failed", fmt.Sprintf("resolve %s's current head", name), resolveErr)
				}
				if !ok {
					return finishDiagnostic(cmd, clikit.NewNotFound, "not_found.git.branch_not_found",
						fmt.Sprintf("branch %s does not exist", name),
						clikit.Manual(fmt.Sprintf("run `git -C %s branch --list` to see what exists", repo.Dir)), nil)
				}
				expectedHead = head
			}

			// The one no-work-loss guard on this verb, and the only one: it must
			// run -- and refuse -- before DeleteBranch's compare-and-swap ever
			// touches the ref, so a refusal leaves name byte-equal to its pre-run
			// value. It reuses worktreeclean's own reachability count and local
			// ref resolution rather than a second predicate; there is no flag
			// that skips it.
			landing, unmerged, ok, err := worktreeclean.ReachableFrom(cmd.Context(), repo, name, landingTarget, cfg.Remote)
			if err != nil {
				return finishErr(cmd, "internal.git.branch_reachability_check_failed", fmt.Sprintf("check %s's reachability", name), err)
			}
			if !ok {
				return finishDiagnostic(cmd, clikit.NewPreconditionUnmet, "precondition_unmet.git.branch_landing_unresolved",
					fmt.Sprintf("cannot resolve a landing target for %s from local refs; pass --landing-target to name one", name),
					clikit.Manual("pass --landing-target to name a ref this branch's work is already reachable from"), nil)
			}
			if unmerged > 0 {
				return finishDiagnostic(cmd, clikit.NewPreconditionUnmet, "precondition_unmet.git.branch_unmerged_work",
					fmt.Sprintf("%s carries %d commit(s) not reachable from %s and would be lost", name, unmerged, landing),
					clikit.Manual("land the branch onto the landing target first; there is no override"),
					map[string]any{"branch": name, "unmerged_commits": unmerged, "landing_target": landing})
			}

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
	cmd.Flags().String("landing-target", "", "ref name's work must already be reachable from (default: name's upstream, else the remote's recorded default)")
	return cmd
}
