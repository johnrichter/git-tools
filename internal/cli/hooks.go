package cli

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
	"github.com/johnrichter/git-tools/internal/hooks"
)

func newHooksCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "hooks",
		Short: "Install git-tools' content scans as a git hook",
	}
	cmd.AddCommand(newHooksInstallCmd())
	return cmd
}

func newHooksInstallCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "install",
		Short:   "Install a hook script that runs `scan all` and point core.hooksPath at it",
		Args:    cobra.NoArgs,
		Example: "  git-tools hooks install --hook pre-commit --strict",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(cmd.Flags())
			if err != nil {
				return finishErr(cmd, "internal.config.load_failed", "load configuration", err)
			}

			hooksDir, _ := cmd.Flags().GetString("hooks-dir")
			hookName, _ := cmd.Flags().GetString("hook")
			binary, _ := cmd.Flags().GetString("binary")
			force, _ := cmd.Flags().GetBool("force")
			dryRun, _ := cmd.Flags().GetBool("dry-run")

			result, err := hooks.Install(cmd.Context(), hooks.InstallOptions{
				RepoDir:     cfg.Repo,
				HooksDir:    hooksDir,
				HookName:    hookName,
				Binary:      binary,
				PrivacyTier: cfg.PrivacyTier,
				Strict:      cfg.Strict,
				Force:       force,
				DryRun:      dryRun,
			})
			if err != nil {
				if errors.Is(err, hooks.ErrHookExists) {
					return finishHookConflict(cmd, err)
				}
				return finishErr(cmd, "internal.hooks.install_failed", "install hook", err)
			}

			clikitResult, buildErr := clikitSuccess(cmd, map[string]any{
				"path":        result.Path,
				"hooks_dir":   result.HooksDir,
				"overwritten": result.Overwritten,
				"dry_run":     result.DryRun,
			})
			if buildErr != nil {
				return finishErr(cmd, "internal.result.build_failed", "build result", buildErr)
			}
			return finish(cmd, clikitResult)
		},
	}
	cmd.Flags().String("hooks-dir", ".githooks", "tracked hook script directory, relative to --repo")
	cmd.Flags().String("hook", "pre-commit", "git hook name to install (e.g. pre-commit, pre-push)")
	cmd.Flags().String("binary", "git-tools", "git-tools executable the installed hook invokes")
	cmd.Flags().Bool("force", false, "overwrite an existing hook script at the target path")
	cmd.Flags().Bool("dry-run", false, "report the install plan without writing anything")
	return cmd
}

// finishHookConflict reports an already-installed hook script as a
// clikit.StatusConflict result: the subject (the hook script path) exists in
// a state incompatible with the request, not an infrastructure failure.
func finishHookConflict(cmd *cobra.Command, err error) error {
	diag, buildErr := clikit.NewError(
		"conflict.hooks.already_installed",
		sanitizeMessage(err.Error()),
		clikit.Manual("re-run with --force to overwrite, or --dry-run to inspect the existing script first"),
		nil,
	)
	if buildErr != nil {
		return finishErr(cmd, "internal.result.build_failed", "build diagnostic", buildErr)
	}
	result, buildErr := clikit.NewConflict(commandPath(cmd), nil, []clikit.Diagnostic{diag}, nil)
	if buildErr != nil {
		return finishErr(cmd, "internal.result.build_failed", "build result", buildErr)
	}
	return finish(cmd, result)
}
