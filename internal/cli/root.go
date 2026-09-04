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
	"path/filepath"
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
// rebase, publish, release tagging, and content scans. There is no installed
// git-hook path: a plain `git commit`, run outside any of this CLI's own
// write verbs, has no session to inherit a resolved betterleaks path from,
// so a hook invoking `scan all` there would refuse every commit fleet-wide
// the moment the credential scan became mandatory, with no way for an
// operator to fix it short of editing the hook script itself. `merge`,
// `push`, `rebase`, and `tag create` — this CLI's own sanctioned write
// verbs — are the only enforcement points now, and each already runs
// `scanGate` before it acts.
func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "git-tools",
		Short: "Signing, rewrite, worktree/branch/merge/rebase/push and content-guardrail operations over a git repository",
		Long: `git-tools composes the shared git, githooks, fsx, sysops and clikit
libraries into one CLI: re-sign commit ranges, manage worktrees and branches,
merge and rebase, publish a branch or cut and push a release tag, and scan
for secrets/raw-binaries/privacy violations.`,
		Example: strings.TrimLeft(`
  git-tools resign --base main --repo . HEAD
  git-tools push main
  git-tools tag create 1.4.0 --shape vX.Y.Z
  git-tools scan all --staged --strict
`, "\n"),
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().String("config", "", "path to a YAML config file (flag > env > file > default)")
	root.PersistentFlags().StringP("repo", "C", "", "git working tree to operate on (default \".\"); refused by push and tag create")
	root.PersistentFlags().String("worktree", "", "working tree to operate on, named as `git worktree list` names it, not by path; refused by push and tag create, and a usage error alongside -C/--repo; under gate governance, prefer -C instead: `git -C <worktree>` or `<git-tools-path> <verb> -C <worktree>`, since a bare name does not reach the gate's own directory check")
	root.PersistentFlags().String("remote", "", "remote name resign/rebase's force-with-lease report targets, and push publishes to (default \"origin\")")
	root.PersistentFlags().String("privacy-tier", "", "privacy scan posture: public, confidential, or private (default \"public\")")
	root.PersistentFlags().Bool("strict", false, "escalate privacy warnings to failures")
	root.PersistentFlags().Int64("max-binary-bytes", 0, "raw-binary scan size threshold in bytes (default githooks.DefaultMaxBytes)")

	root.AddCommand(newSignCmd())
	root.AddCommand(newResignCmd())
	root.AddCommand(newWorktreeCmd())
	root.AddCommand(newBranchCmd())
	root.AddCommand(newMergeCmd())
	root.AddCommand(newRebaseCmd())
	root.AddCommand(newPushCmd())
	root.AddCommand(newTagCmd())
	root.AddCommand(newScanCmd())
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
// underlying tool reported. code must be in the "internal" class. data
// becomes the result's data map (nil for a caller with nothing to report).
func finishErr(cmd *cobra.Command, data map[string]any, code, message string, err error) error {
	diag, buildErr := clikit.NewError(code, sanitizeMessage(fmt.Sprintf("%s: %s", message, err)), clikit.Manual("retry; if this persists, file an issue with the log output"), nil)
	if buildErr != nil {
		return buildErr
	}
	result, buildErr := clikit.NewInternal(commandPath(cmd), data, []clikit.Diagnostic{diag}, nil)
	if buildErr != nil {
		return buildErr
	}
	return finish(cmd, result)
}

// finishUsage builds and emits a clikit.StatusUsage result: the invocation
// itself is wrong (a required setting missing, an unparseable value) and
// nothing was attempted. code must be in the "usage" class. data becomes
// the result's data map (nil for a caller with nothing to report).
func finishUsage(cmd *cobra.Command, data map[string]any, code, message string) error {
	diag, buildErr := clikit.NewError(
		code, sanitizeMessage(message),
		clikit.Manual(fmt.Sprintf("run `%s --help` for valid flags and usage", cmd.CommandPath())),
		nil,
	)
	if buildErr != nil {
		return buildErr
	}
	result, buildErr := clikit.NewUsage(commandPath(cmd), data, []clikit.Diagnostic{diag}, nil)
	if buildErr != nil {
		return buildErr
	}
	return finish(cmd, result)
}

// clikitSuccess builds a clikit.StatusSuccess result for cmd carrying data.
func clikitSuccess(cmd *cobra.Command, data map[string]any) (*clikit.Result, error) {
	return clikit.NewSuccess(commandPath(cmd), data)
}

// finishResult builds and emits cmd's terminal result carrying data: a plain
// success with no caveats, or a StatusCaveats result once any are present.
// This is what a verb whose own content-guardrail scan (scanGate) returned
// warn-only caveats uses in place of a bare clikitSuccess/finish pair, so a
// categorized finding under Config.SecretScanCategorizedSeverity's "warn"
// posture actually surfaces on the command's own result rather than
// vanishing once the scan itself allows the command to proceed.
func finishResult(cmd *cobra.Command, data map[string]any, caveats []clikit.Diagnostic) error {
	var result *clikit.Result
	var buildErr error
	if len(caveats) == 0 {
		result, buildErr = clikit.NewSuccess(commandPath(cmd), data)
	} else {
		result, buildErr = clikit.NewCaveats(commandPath(cmd), data, caveats)
	}
	if buildErr != nil {
		return finishErr(cmd, data, "internal.result.build_failed", "build result", buildErr)
	}
	return finish(cmd, result)
}

// requireRepo opens cfg.Repo as a git working tree, or finishes cmd with a
// not_found result and returns a nil *git.Repo if it isn't one. A caller
// checks for a nil repo and returns the accompanying error immediately —
// the result (or a genuine error) has already been handled.
//
// When --worktree was given, it resolves that name against cfg.Repo's own
// worktree list and opens the match instead — the one place every caller
// that needs a working tree (every verb but push and tag create, which
// refuse the flag outright) picks up the selector with no change of its own.
func requireRepo(cmd *cobra.Command, cfg *Config) (*git.Repo, error) {
	base, err := git.Open(cmd.Context(), cfg.Repo)
	if err != nil {
		return nil, notFoundRepo(cmd, cfg.Repo, err)
	}
	dir, err := effectiveRepoDir(cmd, base, cfg)
	if err != nil {
		return nil, err
	}
	if dir == cfg.Repo {
		return base, nil
	}
	repo, err := git.Open(cmd.Context(), dir)
	if err != nil {
		return nil, notFoundRepo(cmd, dir, err)
	}
	return repo, nil
}

// effectiveRepoDir resolves the directory requireRepo should open: cfg.Repo
// (already -C/--repo/GITTOOLS_REPO/"."-resolved) by default, or a --worktree
// name resolved against base's own worktree list. -C/--repo beside
// --worktree is a usage error, since both name a directory and neither
// spelling takes priority over the other. A name that resolves to the
// repository's own main working tree — always list[0], per `git worktree
// list`'s own ordering — is also a usage error: it would silently retarget
// a caller who meant a linked worktree onto the primary checkout.
func effectiveRepoDir(cmd *cobra.Command, base *git.Repo, cfg *Config) (string, error) {
	if !cmd.Flags().Changed("worktree") {
		return cfg.Repo, nil
	}
	if cmd.Flags().Changed("repo") {
		return "", finishUsage(cmd, nil, "usage.cli.worktree_with_dash_c",
			"--worktree names a working tree by name; -C/--repo names one by path; pass only one")
	}
	name, err := cmd.Flags().GetString("worktree")
	if err != nil {
		return "", finishErr(cmd, nil, "internal.result.build_failed", "read --worktree", err)
	}
	list, err := base.WorktreeList(cmd.Context())
	if err != nil {
		return "", finishErr(cmd, nil, "internal.git.worktree_list_failed", "list worktrees", err)
	}
	for i, wt := range list {
		if filepath.Base(wt.Path) != name {
			continue
		}
		if i == 0 {
			return "", finishUsage(cmd, nil, "usage.cli.worktree_is_main",
				fmt.Sprintf("--worktree %q names this repository's own main working tree; name a linked worktree instead", name))
		}
		return wt.Path, nil
	}
	return "", finishUsage(cmd, nil, "usage.cli.worktree_not_found",
		fmt.Sprintf("no worktree named %q; `git worktree list` lists the ones that exist", name))
}

// notFoundRepo builds and emits requireRepo's not_found result for dir,
// whether dir came from cfg.Repo or from a resolved --worktree.
func notFoundRepo(cmd *cobra.Command, dir string, err error) error {
	diag, buildErr := clikit.NewError(
		"not_found.git.repo_not_found",
		sanitizeMessage(err.Error()),
		clikit.Manual(fmt.Sprintf("point --repo at a git working tree (got %q)", dir)),
		map[string]any{"repo": dir},
	)
	if buildErr != nil {
		return finishErr(cmd, nil, "internal.result.build_failed", "build diagnostic", buildErr)
	}
	result, buildErr := clikit.NewNotFound(commandPath(cmd), nil, []clikit.Diagnostic{diag}, nil)
	if buildErr != nil {
		return finishErr(cmd, nil, "internal.result.build_failed", "build result", buildErr)
	}
	return finish(cmd, result)
}

// handleGitError classifies err from a git library call: a stale
// compare-and-swap or an aborted merge/rebase conflict becomes a
// clikit.StatusConflict result carrying data; anything else falls back to a
// clikit.StatusInternal result under fallbackCode/fallbackMessage, also
// carrying data.
func handleGitError(cmd *cobra.Command, data map[string]any, err error, fallbackCode, fallbackMessage string) error {
	diag, ok, buildErr := gitresult.ConflictDiagnostic(err)
	if buildErr != nil {
		return finishErr(cmd, nil, "internal.result.build_failed", "build diagnostic", buildErr)
	}
	if !ok {
		return finishErr(cmd, data, fallbackCode, fallbackMessage, err)
	}
	result, buildErr := clikit.NewConflict(commandPath(cmd), data, []clikit.Diagnostic{diag}, nil)
	if buildErr != nil {
		return finishErr(cmd, nil, "internal.result.build_failed", "build result", buildErr)
	}
	return finish(cmd, result)
}

// finishDiagnostic builds a single-diagnostic result via build — one of
// clikit's non-success, non-caveats constructors (NewPreconditionUnmet,
// NewNotFound, NewConflict, NewUsage, NewTransient, ...) — and emits it.
// finishErr and finishUsage each hand-roll this shape for one fixed status;
// a caller needing a different status, triage or diagnostic context uses
// this instead of repeating it. data becomes the result's data map.
func finishDiagnostic(cmd *cobra.Command, data map[string]any, build func(command []string, data map[string]any, errors, caveats []clikit.Diagnostic) (*clikit.Result, error), code, message string, triage clikit.Triage, context map[string]any) error {
	diag, buildErr := clikit.NewError(code, sanitizeMessage(message), triage, context)
	if buildErr != nil {
		return finishErr(cmd, nil, "internal.result.build_failed", "build diagnostic", buildErr)
	}
	result, buildErr := build(commandPath(cmd), data, []clikit.Diagnostic{diag}, nil)
	if buildErr != nil {
		return finishErr(cmd, nil, "internal.result.build_failed", "build result", buildErr)
	}
	return finish(cmd, result)
}

// finishCaveatAlongside builds a clikit.StatusCaveats result carrying data
// and emits it: the command did what was asked, but the outcome needs
// qualifying — e.g. a no-op because the target already matched. earlier
// carries forward whatever caveats an earlier step (typically a
// content-guardrail scan that landed a warn-only finding) already produced,
// so they are never lost just because a later step also has something to
// caveat about; pass nil when there is nothing earlier to carry.
func finishCaveatAlongside(cmd *cobra.Command, data map[string]any, earlier []clikit.Diagnostic, code, message string, triage clikit.Triage, context map[string]any) error {
	caveat, buildErr := clikit.NewCaveat(code, sanitizeMessage(message), triage, context)
	if buildErr != nil {
		return finishErr(cmd, data, "internal.result.build_failed", "build caveat", buildErr)
	}
	return finishResult(cmd, data, append(append([]clikit.Diagnostic{}, earlier...), caveat))
}
