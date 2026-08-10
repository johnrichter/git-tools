package cli

import (
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
	"github.com/johnrichter/claude-shared-tooling/go/git"
)

// worktreeRegisteredAt reports whether list contains an entry at path,
// comparing resolved (symlink-free) absolute paths so a caller-supplied
// relative path still matches what `git worktree list` reports. A relative
// path is resolved against repoDir (the repository's working tree), not the
// process's current directory, since git itself interprets `worktree add`'s
// <path> relative to the repository, not the caller's cwd.
func worktreeRegisteredAt(list []git.WorktreeInfo, repoDir, path string) bool {
	want := resolvedPath(repoDir, path)
	for _, wt := range list {
		if resolvedPath(repoDir, wt.Path) == want {
			return true
		}
	}
	return false
}

// resolvedPath returns p's absolute, symlink-resolved form. A relative p is
// joined against base first; an already-absolute p is used as-is. It falls
// back to the absolute (unresolved) form, then p itself, when resolution
// fails.
func resolvedPath(base, p string) string {
	if !filepath.IsAbs(p) {
		p = filepath.Join(base, p)
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		return resolved
	}
	return abs
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
		Use:     "remove <path>",
		Short:   "Delete a linked worktree",
		Args:    cobra.ExactArgs(1),
		Example: "  git-tools worktree remove ../review",
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

			force, _ := cmd.Flags().GetBool("force")
			dryRun, _ := cmd.Flags().GetBool("dry-run")

			if err := repo.WorktreeRemove(cmd.Context(), path, git.WorktreeRemoveOptions{Force: force, DryRun: dryRun}); err != nil {
				return handleGitError(cmd, err, "internal.git.worktree_remove_failed", fmt.Sprintf("remove worktree %s", path))
			}

			result, buildErr := clikitSuccess(cmd, map[string]any{"path": path, "dry_run": dryRun})
			if buildErr != nil {
				return finishErr(cmd, "internal.result.build_failed", "build result", buildErr)
			}
			return finish(cmd, result)
		},
	}
	cmd.Flags().Bool("force", false, "remove even with uncommitted changes or untracked files")
	cmd.Flags().Bool("dry-run", false, "confirm path is a registered worktree without removing it")
	return cmd
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
