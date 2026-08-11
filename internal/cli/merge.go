package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
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
			cleanup, _ := cmd.Flags().GetBool("cleanup")
			force, _ := cmd.Flags().GetBool("force")

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

			// Cleanup runs only on a real, successful merge, and only when opted
			// in. It shares one rule set with the standalone `worktree remove`
			// verb, so the no-work-loss guard cannot diverge between the paths. A
			// cleanup that refuses or fails never unwinds the merge that already
			// landed: it is reported as a caveat on the success.
			if cleanup && !result.DryRun {
				cleaned, unremoved := cleanupMergedWorktrees(cmd.Context(), repo, args, cfg.Remote, force)
				if len(cleaned) > 0 {
					data["cleaned_worktrees"] = cleaned
				}
				if len(unremoved) > 0 {
					data["unremoved_worktrees"] = unremoved
					reason, _ := unremoved[0]["reason"].(string)
					return finishCaveat(cmd, data, "caveats.git.worktree_cleanup_incomplete",
						fmt.Sprintf("the merge landed, but %d worktree(s) were not removed: %s", len(unremoved), reason),
						clikit.Manual("remove the named worktree(s) manually, or re-run with --force to discard the named work"),
						map[string]any{"unremoved": len(unremoved)})
				}
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
	cmd.Flags().Bool("cleanup", false, "after a successful merge, remove each merged branch's worktree once its work has safely landed")
	cmd.Flags().Bool("force", false, "with --cleanup, override the no-work-loss and cardinality refusals, discarding the named work")
	return cmd
}

// cleanupMergedWorktrees removes the worktree of each just-merged branch,
// through the shared cleanupWorktree rule set. It never returns an error: a
// merge has already landed, so a refusal or an infrastructure failure is folded
// into the unremoved list for the caller to report as a caveat rather than
// unwound. cleaned holds the paths removed; each unremoved entry names a path
// (when known) and the reason it stayed.
func cleanupMergedWorktrees(ctx context.Context, repo *git.Repo, mergedBranches []string, remote string, force bool) (cleaned []string, unremoved []map[string]any) {
	list, err := repo.WorktreeList(ctx)
	if err != nil {
		return nil, []map[string]any{{"reason": sanitizeMessage(fmt.Sprintf("list worktrees: %v", err))}}
	}
	for _, wt := range list {
		if wt.Branch == "" || !branchAmong(wt.Branch, mergedBranches) {
			continue
		}
		out, err := cleanupWorktree(ctx, repo, wt.Path, cleanupOptions{
			MergedBranches: mergedBranches,
			Remote:         remote,
			Force:          force,
		})
		switch {
		case err != nil:
			unremoved = append(unremoved, map[string]any{"path": wt.Path, "reason": sanitizeMessage(err.Error())})
		case out.Refusal != "":
			unremoved = append(unremoved, map[string]any{"path": out.Path, "reason": out.Refusal})
		case out.Removed:
			cleaned = append(cleaned, out.Path)
		}
	}
	return cleaned, unremoved
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
