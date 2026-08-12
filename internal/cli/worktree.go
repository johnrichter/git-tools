package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
	"github.com/johnrichter/claude-shared-tooling/go/git"
	"github.com/johnrichter/git-tools/internal/worktreeclean"
)

// worktreeRegisteredAt reports whether list contains an entry at path,
// comparing resolved (symlink-free) absolute paths so a caller-supplied
// relative path still matches what `git worktree list` reports. A relative
// path is resolved against repoDir (the repository's working tree), not the
// process's current directory, since git itself interprets `worktree add`'s
// <path> relative to the repository, not the caller's cwd.
func worktreeRegisteredAt(list []git.WorktreeInfo, repoDir, path string) bool {
	want := worktreeclean.ResolvedPath(repoDir, path)
	for _, wt := range list {
		if worktreeclean.ResolvedPath(repoDir, wt.Path) == want {
			return true
		}
	}
	return false
}

// worktreeEntry renders a git.WorktreeInfo with the snake_case field names
// clikit's own conventions use, rather than the library type's Go-exported
// field names.
type worktreeEntry struct {
	Path   string `json:"path"`
	Head   string `json:"head"`
	Branch string `json:"branch,omitempty"`
}

func newWorktreeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "worktree",
		Short: "Add, remove, and list linked worktrees",
	}
	cmd.AddCommand(newWorktreeAddCmd())
	cmd.AddCommand(newWorktreeRemoveCmd())
	cmd.AddCommand(newWorktreeListCmd())
	return cmd
}

func newWorktreeAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "add <path> <ref>",
		Short:   "Create a linked worktree at path checked out to ref",
		Args:    cobra.ExactArgs(2),
		Example: "  git-tools worktree add ../review origin/main --branch review",
		RunE: func(cmd *cobra.Command, args []string) error {
			path, ref := args[0], args[1]
			cfg, err := loadConfig(cmd.Flags())
			if err != nil {
				return finishErr(cmd, "internal.config.load_failed", "load configuration", err)
			}
			repo, repoErr := requireRepo(cmd, cfg)
			if repo == nil {
				return repoErr
			}

			branch, _ := cmd.Flags().GetString("branch")
			force, _ := cmd.Flags().GetBool("force")
			dryRun, _ := cmd.Flags().GetBool("dry-run")

			if err := repo.WorktreeAdd(cmd.Context(), path, ref, git.WorktreeAddOptions{NewBranch: branch, Force: force, DryRun: dryRun}); err != nil {
				return handleGitError(cmd, err, "internal.git.worktree_add_failed", fmt.Sprintf("add worktree %s at %s", path, ref))
			}

			if !dryRun {
				// Self-verify: `git worktree add` reporting success is not
				// itself proof of a correctly isolated worktree. Re-read the
				// registered list and confirm the new path is actually there
				// before telling a caller it can rely on it.
				list, listErr := repo.WorktreeList(cmd.Context())
				if listErr != nil {
					return finishErr(cmd, "internal.git.worktree_verify_failed", "verify the worktree was created", listErr)
				}
				if !worktreeRegisteredAt(list, repo.Dir, path) {
					return finishDiagnostic(cmd, clikit.NewInternal, "internal.git.worktree_add_unverified",
						fmt.Sprintf("verify the worktree was created: git worktree add reported success but %s does not appear in `git worktree list`", path),
						clikit.Manual(fmt.Sprintf("run `git -C %s worktree list` to see what git actually registered, and re-check the path passed to `worktree add`", repo.Dir)),
						nil)
				}
			}

			data := map[string]any{"path": path, "ref": ref, "dry_run": dryRun}
			if branch != "" {
				data["branch"] = branch
			}
			result, buildErr := clikitSuccess(cmd, data)
			if buildErr != nil {
				return finishErr(cmd, "internal.result.build_failed", "build result", buildErr)
			}
			return finish(cmd, result)
		},
	}
	cmd.Flags().String("branch", "", "create and check out a new branch at ref in the new worktree")
	cmd.Flags().Bool("force", false, "reuse a branch already checked out elsewhere, or overwrite a path git would refuse")
	cmd.Flags().Bool("dry-run", false, "validate the request without creating anything")
	return cmd
}

func newWorktreeRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "remove <path>",
		Short: "Delete a linked worktree, refusing to discard unmerged work",
		Long: `remove deletes the linked worktree at path after proving its work is
safe to discard. It refuses -- removing nothing and returning a named non-zero
error -- when the worktree's checked-out branch (or a nested worktree's branch)
carries commits unreachable from its landing target, when that target cannot be
resolved from local refs, when the worktree's own tree has an untracked or
modified path (commit it, ignore it, or delete it deliberately), when a live
sub-worktree nests under it, or when its HEAD is detached. No flag overrides
any of these refusals -- each condition must be resolved on its own terms
before the worktree can go. The landing target is --landing-target if given,
else the branch's upstream, else the local record of the remote's default
branch; every step is answered from local refs, never the network.`,
		Args:    cobra.ExactArgs(1),
		Example: "  git-tools worktree remove ../review --landing-target main",
		RunE: func(cmd *cobra.Command, args []string) error {
			path := args[0]
			cfg, err := loadConfig(cmd.Flags())
			if err != nil {
				return finishErr(cmd, "internal.config.load_failed", "load configuration", err)
			}
			repo, repoErr := requireRepo(cmd, cfg)
			if repo == nil {
				return repoErr
			}

			dryRun, _ := cmd.Flags().GetBool("dry-run")
			landing, _ := cmd.Flags().GetString("landing-target")

			out, err := worktreeclean.Cleanup(cmd.Context(), repo, path, worktreeclean.Options{
				LandingTarget: landing,
				Remote:        cfg.Remote,
				DryRun:        dryRun,
			})
			if err != nil {
				return handleGitError(cmd, err, "internal.git.worktree_remove_failed", fmt.Sprintf("remove worktree %s", path))
			}

			data := cleanupData(out, dryRun)
			if out.Refusal != "" {
				// The standalone path treats a refusal as a hard error: nothing
				// was removed, and the operator must resolve the named condition
				// before the worktree can go.
				if out.RefusalKind == worktreeclean.RefusalNotRegistered {
					return finishDiagnostic(cmd, clikit.NewNotFound, "not_found.git.worktree_not_registered",
						out.Refusal, clikit.RunTool("git-tools", "worktree", "list"), data)
				}
				return finishDiagnostic(cmd, clikit.NewPreconditionUnmet, cleanupRefusalCode(out.RefusalKind),
					out.Refusal, clikit.Manual("resolve the named condition"), data)
			}

			result, buildErr := clikitSuccess(cmd, data)
			if buildErr != nil {
				return finishErr(cmd, "internal.result.build_failed", "build result", buildErr)
			}
			return finish(cmd, result)
		},
	}
	cmd.Flags().Bool("dry-run", false, "run every rule and report the verdict without removing anything")
	cmd.Flags().String("landing-target", "", "ref the worktree's work must already be reachable from (default: the branch's upstream, else the remote's recorded default)")
	return cmd
}

// cleanupData renders a worktreeclean.Result as the success/diagnostic data map
// both entry points emit.
func cleanupData(out *worktreeclean.Result, dryRun bool) map[string]any {
	data := map[string]any{"path": out.Path, "dry_run": dryRun, "removed": out.Removed}
	if len(out.Branches) > 0 {
		data["branches"] = out.Branches
	}
	if out.Unmerged > 0 {
		data["unmerged_commits"] = out.Unmerged
	}
	if len(out.UntrackedPaths) > 0 {
		data["untracked_paths"] = out.UntrackedPaths
	}
	if len(out.ModifiedPaths) > 0 {
		data["modified_paths"] = out.ModifiedPaths
	}
	return data
}

// cleanupRefusalCode maps a refusal kind to its stable precondition_unmet
// diagnostic code.
func cleanupRefusalCode(kind worktreeclean.RefusalKind) string {
	switch kind {
	case worktreeclean.RefusalDetachedHead:
		return "precondition_unmet.git.worktree_detached_head"
	case worktreeclean.RefusalBranchNotMerged:
		return "precondition_unmet.git.worktree_branch_not_merged"
	case worktreeclean.RefusalLandingUnresolved:
		return "precondition_unmet.git.worktree_landing_unresolved"
	case worktreeclean.RefusalDirtyTree:
		return "precondition_unmet.git.worktree_dirty_tree"
	case worktreeclean.RefusalUnmergedWork:
		return "precondition_unmet.git.worktree_unmerged_work"
	case worktreeclean.RefusalLiveSubWorktree:
		return "precondition_unmet.git.worktree_live_sub_worktree"
	default:
		return "precondition_unmet.git.worktree_cleanup_refused"
	}
}

func newWorktreeListCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "list",
		Short:   "List every worktree linked to the repository, including the main one",
		Args:    cobra.NoArgs,
		Example: "  git-tools worktree list",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(cmd.Flags())
			if err != nil {
				return finishErr(cmd, "internal.config.load_failed", "load configuration", err)
			}
			repo, repoErr := requireRepo(cmd, cfg)
			if repo == nil {
				return repoErr
			}

			list, err := repo.WorktreeList(cmd.Context())
			if err != nil {
				return finishErr(cmd, "internal.git.worktree_list_failed", "list worktrees", err)
			}

			entries := make([]worktreeEntry, len(list))
			for i, wt := range list {
				entries[i] = worktreeEntry{Path: wt.Path, Head: wt.Head, Branch: wt.Branch}
			}
			data := map[string]any{"count": len(entries)}
			if len(entries) > 0 {
				data["worktrees"] = entries
			}
			result, buildErr := clikitSuccess(cmd, data)
			if buildErr != nil {
				return finishErr(cmd, "internal.result.build_failed", "build result", buildErr)
			}
			return finish(cmd, result)
		},
	}
	return cmd
}
