package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
	"github.com/johnrichter/claude-shared-tooling/go/git"
	"github.com/johnrichter/git-tools/internal/gitexec"
	gitresult "github.com/johnrichter/git-tools/internal/result"
	"github.com/johnrichter/git-tools/internal/worktreeclean"
)

func newBranchCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "branch",
		Short: "Create, delete, and list branches",
	}
	cmd.AddCommand(newBranchCreateCmd())
	cmd.AddCommand(newBranchDeleteCmd())
	cmd.AddCommand(newBranchListCmd())
	return cmd
}

func newBranchCreateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create <name> <start-point>",
		Short: "Create name pointing at start-point",
		Long: `create makes name point at start-point. Without --force, name must not
already exist.

--force lets name already exist, but moves it only when the old tip stays
reachable from start-point -- the move loses no commit. When it would orphan
commits, create refuses instead: name is left byte-equal to its pre-run
value. There is no override; point start-point at a ref that already
contains the branch's current tip, or drop --force.`,
		Args:    cobra.ExactArgs(2),
		Example: "  git-tools branch create feature/x origin/main",
		RunE: func(cmd *cobra.Command, args []string) error {
			name, startPoint := args[0], args[1]
			cfg, err := loadConfig(cmd.Flags())
			if err != nil {
				return finishErr(cmd, nil, "internal.config.load_failed", "load configuration", err)
			}
			repo, repoErr := requireRepo(cmd, cfg)
			if repo == nil {
				return repoErr
			}

			force, _ := cmd.Flags().GetBool("force")
			dryRun, _ := cmd.Flags().GetBool("dry-run")

			if force {
				// The one no-work-loss guard on a forced move, and the only one: it
				// must run -- and refuse -- before CreateBranch ever touches the ref,
				// so a refusal leaves name byte-equal to its pre-run value. It counts
				// reachability with the same predicate the branch-delete guard uses
				// (commits reachable from one commit but not another), not a second
				// one, and only applies when name already exists: plain creation, and
				// --force naming a branch that does not exist, are untouched by it.
				oldTip, exists, resolveErr := worktreeclean.RevParseLocal(cmd.Context(), repo.Dir, "refs/heads/"+name)
				if resolveErr != nil {
					return finishErr(cmd, nil, "internal.git.branch_head_resolve_failed", fmt.Sprintf("resolve %s's current head", name), resolveErr)
				}
				if exists {
					newTip, resolved, resolveErr := worktreeclean.RevParseLocal(cmd.Context(), repo.Dir, startPoint)
					if resolveErr != nil {
						return finishErr(cmd, nil, "internal.git.branch_start_point_resolve_failed", fmt.Sprintf("resolve start point %s", startPoint), resolveErr)
					}
					// An unresolvable start-point is left to CreateBranch below, which
					// fails with git's own message -- unchanged from today.
					if resolved {
						orphaned, countErr := worktreeclean.CountUnmerged(cmd.Context(), repo, []string{oldTip}, newTip)
						if countErr != nil {
							return finishErr(cmd, nil, "internal.git.branch_reachability_check_failed", fmt.Sprintf("check %s's reachability from %s", name, startPoint), countErr)
						}
						if orphaned > 0 {
							return finishDiagnostic(cmd, nil, clikit.NewPreconditionUnmet, "precondition_unmet.git.branch_move_orphans_commits",
								fmt.Sprintf("moving %s from %s to %s would orphan %d commit(s) not reachable from the new start point", name, oldTip, startPoint, orphaned),
								clikit.Manual("point start-point at a ref that already contains the branch's current tip, or drop --force"),
								map[string]any{"branch": name, "current_tip": oldTip, "start_point": startPoint, "orphaned_commits": orphaned})
						}
					}
				}
			}

			if err := repo.CreateBranch(cmd.Context(), name, startPoint, git.BranchOptions{Force: force, DryRun: dryRun}); err != nil {
				return finishErr(cmd, nil, "internal.git.branch_create_failed", fmt.Sprintf("create branch %s at %s", name, startPoint), err)
			}

			result, buildErr := clikitSuccess(cmd, map[string]any{"branch": name, "start_point": startPoint, "dry_run": dryRun})
			if buildErr != nil {
				return finishErr(cmd, nil, "internal.result.build_failed", "build result", buildErr)
			}
			return finish(cmd, result)
		},
	}
	cmd.Flags().Bool("force", false, "move an existing branch of the same name instead of refusing, but only when the old tip stays reachable from start-point")
	cmd.Flags().Bool("dry-run", false, "validate that start-point resolves (and, with --force, that the move is reachable) without creating or moving the branch")
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
				return finishErr(cmd, nil, "internal.config.load_failed", "load configuration", err)
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
					return finishErr(cmd, nil, "internal.git.branch_head_resolve_failed", fmt.Sprintf("resolve %s's current head", name), resolveErr)
				}
				if !ok {
					return finishDiagnostic(cmd, nil, clikit.NewNotFound, "not_found.git.branch_not_found",
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
				return finishErr(cmd, nil, "internal.git.branch_reachability_check_failed", fmt.Sprintf("check %s's reachability", name), err)
			}
			if !ok {
				return finishDiagnostic(cmd, nil, clikit.NewPreconditionUnmet, "precondition_unmet.git.branch_landing_unresolved",
					fmt.Sprintf("cannot resolve a landing target for %s from local refs; pass --landing-target to name one", name),
					clikit.Manual("pass --landing-target to name a ref this branch's work is already reachable from"), nil)
			}
			if unmerged > 0 {
				return finishDiagnostic(cmd, nil, clikit.NewPreconditionUnmet, "precondition_unmet.git.branch_unmerged_work",
					fmt.Sprintf("%s carries %d commit(s) not reachable from %s and would be lost", name, unmerged, landing),
					clikit.Manual("land the branch onto the landing target first; there is no override"),
					map[string]any{"branch": name, "unmerged_commits": unmerged, "landing_target": landing})
			}

			outcome, err := repo.DeleteBranch(cmd.Context(), name, expectedHead, dryRun)
			if err != nil {
				return handleGitError(cmd, nil, err, "internal.git.branch_delete_failed", fmt.Sprintf("delete branch %s", name))
			}

			result, buildErr := clikitSuccess(cmd, gitresult.RewriteOutcomeData(outcome))
			if buildErr != nil {
				return finishErr(cmd, nil, "internal.result.build_failed", "build result", buildErr)
			}
			return finish(cmd, result)
		},
	}
	cmd.Flags().Bool("dry-run", false, "confirm the compare-and-swap without deleting the branch")
	cmd.Flags().String("landing-target", "", "ref name's work must already be reachable from (default: name's upstream, else the remote's recorded default)")
	return cmd
}

// branchEntry renders one local branch's for-each-ref row with the
// snake_case field names clikit's own conventions use.
type branchEntry struct {
	Name    string `json:"name"`
	Head    string `json:"head"`
	Current bool   `json:"current,omitempty"`
}

func newBranchListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List every local branch, with its head commit and whether it's checked out",
		Args:    cobra.NoArgs,
		Example: "  git-tools branch list",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(cmd.Flags())
			if err != nil {
				return finishErr(cmd, nil, "internal.config.load_failed", "load configuration", err)
			}
			repo, repoErr := requireRepo(cmd, cfg)
			if repo == nil {
				return repoErr
			}

			entries, err := listLocalBranches(cmd.Context(), repo.Dir)
			if err != nil {
				return finishErr(cmd, nil, "internal.git.branch_list_failed", "list branches", err)
			}

			data := map[string]any{"count": len(entries)}
			if len(entries) > 0 {
				data["branches"] = entries
			}
			result, buildErr := clikitSuccess(cmd, data)
			if buildErr != nil {
				return finishErr(cmd, nil, "internal.result.build_failed", "build result", buildErr)
			}
			return finish(cmd, result)
		},
	}
	return cmd
}

// listLocalBranches reads every local branch under refs/heads with
// for-each-ref -- a read-only ref walk, not git branch's own listing mode --
// so the verb's read-only contract holds independent of how git branch
// itself happens to classify a bare invocation.
func listLocalBranches(ctx context.Context, dir string) ([]branchEntry, error) {
	args := []string{"for-each-ref", "--format=%(refname:short)%09%(objectname)%09%(HEAD)", "refs/heads"}
	res, err := gitexec.RunGit(ctx, dir, args...)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, &git.CommandError{Args: args, ExitCode: res.ExitCode, Stderr: strings.TrimSpace(string(res.Stderr))}
	}
	out := strings.TrimSpace(string(res.Stdout))
	if out == "" {
		return nil, nil
	}
	lines := strings.Split(out, "\n")
	entries := make([]branchEntry, len(lines))
	for i, line := range lines {
		fields := strings.SplitN(line, "\t", 3)
		entries[i] = branchEntry{Name: fields[0], Head: fields[1], Current: fields[2] == "*"}
	}
	return entries, nil
}
