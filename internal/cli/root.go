// Package cli wires git-tools' cobra command tree onto the shared git,
// githooks and clikit libraries. Every command emits exactly one
// clikit.Result to stdout and exits with that result's exit code — cobra's
// own usage/error printing is silenced so it never competes with that one
// record.
package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
	"github.com/johnrichter/claude-shared-tooling/go/git"
	gitresult "github.com/johnrichter/git-tools/internal/result"
)

// exitError carries a clikit-derived exit code up through cobra's error
// return path without cobra printing anything itself — the command that
// raised it has already emitted its clikit.Result.
type exitError struct{ code int }

func (e *exitError) Error() string { return fmt.Sprintf("exit code %d", e.code) }

// newRootCmd builds the command tree: sign/resign, worktree, branch, merge,
// rebase, content scans and installable git hooks.
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "git-tools",
		Short: "Signing, rewrite, worktree/branch/merge/rebase and content-guardrail operations over a git repository",
		Long: `git-tools composes the shared git, githooks, fsx, sysops and clikit
libraries into one CLI: re-sign commit ranges, manage worktrees and branches,
merge and rebase, scan for secrets/raw-binaries/privacy violations, and
install those scans as git hooks.`,
		Example: strings.TrimLeft(`
  git-tools resign --base main --repo . HEAD
  git-tools scan all --staged --strict
  git-tools hooks install --hook pre-commit
`, "\n"),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().String("config", "", "path to a YAML config file (flag > env > file > default)")
	root.PersistentFlags().String("repo", "", "git working tree to operate on (default \".\")")
	root.PersistentFlags().String("remote", "", "remote name a force-with-lease push targets (default \"origin\")")
	root.PersistentFlags().String("privacy-tier", "", "privacy scan posture: public, datadog, or personal (default \"public\")")
	root.PersistentFlags().Bool("strict", false, "escalate privacy warnings to failures")
	root.PersistentFlags().Int64("max-binary-bytes", 0, "raw-binary scan size threshold in bytes (default githooks.DefaultMaxBytes)")

	root.AddCommand(newSignCmd())
	root.AddCommand(newResignCmd())
	root.AddCommand(newWorktreeCmd())
	root.AddCommand(newBranchCmd())
	root.AddCommand(newMergeCmd())
	root.AddCommand(newRebaseCmd())
	root.AddCommand(newScanCmd())
	root.AddCommand(newHooksCmd())
	return root
}

// Execute runs the command tree and returns the process exit code —
// clikit's, for anything that reached a subcommand, or a usage code for an
// invocation cobra itself rejected before that (e.g. an unknown flag).
func Execute() int {
	root := newRootCmd()
	ranCmd, err := root.ExecuteC()
	if err == nil {
		return 0
	}
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	return emitUsageError(ranCmd, err)
}

// commandPath renders cmd's full invocation ("git-tools scan secrets") as
// the token slice clikit.Result.Command requires.
func commandPath(cmd *cobra.Command) []string {
	return strings.Fields(cmd.CommandPath())
}

// sanitizeMessage collapses msg to the single control-character-free line
// clikit.NewError requires: an underlying git/OS error's text is free-form
// and, e.g. `git`'s own multi-line advice output, may contain newlines and
// other control characters a diagnostic message may not.
func sanitizeMessage(msg string) string {
	folded := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, msg)
	joined := strings.Join(strings.Fields(folded), " ")
	const max = 4096
	if len(joined) > max {
		joined = joined[:max]
	}
	return joined
}

// emitUsageError handles an error cobra raised before any subcommand's RunE
// ran (bad flag, unknown subcommand) — no clikit.Result has been emitted
// yet, so this is the one place that builds one for that case.
func emitUsageError(cmd *cobra.Command, err error) int {
	diag, buildErr := clikit.NewError(
		"usage.cli.invalid_invocation",
		sanitizeMessage(err.Error()),
		clikit.Manual("run `git-tools --help` (or `git-tools <command> --help`) for valid flags and usage"),
		nil,
	)
	if buildErr != nil {
		fmt.Fprintln(os.Stderr, err)
		return clikit.StatusInternal.ExitCode()
	}
	result, buildErr := clikit.NewUsage(commandPath(cmd), nil, []clikit.Diagnostic{diag}, nil)
	if buildErr != nil {
		fmt.Fprintln(os.Stderr, err)
		return clikit.StatusInternal.ExitCode()
	}
	if emitErr := clikit.Emit(os.Stdout, result); emitErr != nil {
		fmt.Fprintln(os.Stderr, emitErr)
	}
	return result.ExitCode
}

// finish emits result and turns it into cobra's error-return path: nil for
// success, an *exitError carrying result.ExitCode otherwise.
func finish(cmd *cobra.Command, result *clikit.Result) error {
	if err := clikit.Emit(cmd.OutOrStdout(), result); err != nil {
		return err
	}
	if result.ExitCode == 0 {
		return nil
	}
	return &exitError{code: result.ExitCode}
}

// finishCode turns an already-emitted exit code (e.g. from
// githooks.EmitHookResult, which emits its own result record) into cobra's
// error-return path.
func finishCode(code int) error {
	if code == 0 {
		return nil
	}
	return &exitError{code: code}
}

// finishErr builds and emits a clikit.StatusInternal result for err — an
// infrastructure failure from this CLI itself, not a diagnostic the
// underlying tool reported. code must be in the "internal" class.
func finishErr(cmd *cobra.Command, code, message string, err error) error {
	diag, buildErr := clikit.NewError(code, sanitizeMessage(fmt.Sprintf("%s: %s", message, err)), clikit.Manual("retry; if this persists, file an issue with the log output"), nil)
	if buildErr != nil {
		return buildErr
	}
	result, buildErr := clikit.NewInternal(commandPath(cmd), nil, []clikit.Diagnostic{diag}, nil)
	if buildErr != nil {
		return buildErr
	}
	return finish(cmd, result)
}

// finishUsage builds and emits a clikit.StatusUsage result: the invocation
// itself is wrong (a required setting missing, an unparseable value) and
// nothing was attempted. code must be in the "usage" class.
func finishUsage(cmd *cobra.Command, code, message string) error {
	diag, buildErr := clikit.NewError(
		code, sanitizeMessage(message),
		clikit.Manual(fmt.Sprintf("run `%s --help` for valid flags and usage", cmd.CommandPath())),
		nil,
	)
	if buildErr != nil {
		return buildErr
	}
	result, buildErr := clikit.NewUsage(commandPath(cmd), nil, []clikit.Diagnostic{diag}, nil)
	if buildErr != nil {
		return buildErr
	}
	return finish(cmd, result)
}

// clikitSuccess builds a clikit.StatusSuccess result for cmd carrying data.
func clikitSuccess(cmd *cobra.Command, data map[string]any) (*clikit.Result, error) {
	return clikit.NewSuccess(commandPath(cmd), data)
}

// requireRepo opens cfg.Repo as a git working tree, or finishes cmd with a
// not_found result and returns a nil *git.Repo if it isn't one. A caller
// checks for a nil repo and returns the accompanying error immediately —
// the result (or a genuine error) has already been handled.
func requireRepo(cmd *cobra.Command, cfg *Config) (*git.Repo, error) {
	repo, err := git.Open(cmd.Context(), cfg.Repo)
	if err == nil {
		return repo, nil
	}
	diag, buildErr := clikit.NewError(
		"not_found.git.repo_not_found",
		sanitizeMessage(err.Error()),
		clikit.Manual(fmt.Sprintf("point --repo at a git working tree (got %q)", cfg.Repo)),
		map[string]any{"repo": cfg.Repo},
	)
	if buildErr != nil {
		return nil, finishErr(cmd, "internal.result.build_failed", "build diagnostic", buildErr)
	}
	result, buildErr := clikit.NewNotFound(commandPath(cmd), nil, []clikit.Diagnostic{diag}, nil)
	if buildErr != nil {
		return nil, finishErr(cmd, "internal.result.build_failed", "build result", buildErr)
	}
	return nil, finish(cmd, result)
}

// handleGitError classifies err from a git library call: a stale
// compare-and-swap or an aborted merge/rebase conflict becomes a
// clikit.StatusConflict result; anything else falls back to a
// clikit.StatusInternal result under fallbackCode/fallbackMessage.
func handleGitError(cmd *cobra.Command, err error, fallbackCode, fallbackMessage string) error {
	diag, ok, buildErr := gitresult.ConflictDiagnostic(err)
	if buildErr != nil {
		return finishErr(cmd, "internal.result.build_failed", "build diagnostic", buildErr)
	}
	if !ok {
		return finishErr(cmd, fallbackCode, fallbackMessage, err)
	}
	result, buildErr := clikit.NewConflict(commandPath(cmd), nil, []clikit.Diagnostic{diag}, nil)
	if buildErr != nil {
		return finishErr(cmd, "internal.result.build_failed", "build result", buildErr)
	}
	return finish(cmd, result)
}
