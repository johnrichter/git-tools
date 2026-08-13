package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/johnrichter/claude-shared-tooling/go/git"
	gitresult "github.com/johnrichter/git-tools/internal/result"
)

// newSignCmd builds the single-commit convenience over Resign: sign just ref
// itself (Base is ref^), for the common case of re-signing the tip commit
// after amending or importing it unsigned.
func newSignCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "sign [ref]",
		Short:   "Re-sign a single commit (default: HEAD) with an identical tree",
		Args:    cobra.MaximumNArgs(1),
		Example: "  git-tools sign HEAD",
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := "HEAD"
			if len(args) == 1 {
				ref = args[0]
			}
			return runResign(cmd, ref, ref+"^")
		},
	}
	addSyncFlags(cmd)
	return cmd
}

// newResignCmd builds the full-range Resign command: every commit in
// --base..ref gets an identical-tree, newly signed replacement.
func newResignCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "resign [ref]",
		Short:   "Re-sign every commit in --base..ref with identical trees",
		Args:    cobra.MaximumNArgs(1),
		Example: "  git-tools resign --base origin/main HEAD",
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := "HEAD"
			if len(args) == 1 {
				ref = args[0]
			}
			base, _ := cmd.Flags().GetString("base")
			if base == "" {
				return finishUsage(cmd, nil, "usage.cli.missing_base", "--base is required")
			}
			return runResign(cmd, ref, base)
		},
	}
	cmd.Flags().String("base", "", "range start, exclusive (resign rewrites base..ref)")
	addSyncFlags(cmd)
	return cmd
}

// addSyncFlags adds the flags sign and resign share beyond their own
// base/ref handling.
func addSyncFlags(cmd *cobra.Command) {
	cmd.Flags().Bool("push-force-with-lease", false, "report the force-with-lease push argv for a ref that is already shared with a remote (never pushes itself)")
	cmd.Flags().Bool("dry-run", false, "compute the rewrite and report the resulting head without moving the ref")
}

func runResign(cmd *cobra.Command, ref, base string) error {
	cfg, err := loadConfig(cmd.Flags())
	if err != nil {
		return finishErr(cmd, nil, "internal.config.load_failed", "load configuration", err)
	}
	repo, repoErr := requireRepo(cmd, cfg)
	if repo == nil {
		return repoErr
	}

	sync, _ := cmd.Flags().GetBool("push-force-with-lease")
	dryRun, _ := cmd.Flags().GetBool("dry-run")
	syncMode := git.SyncLocalOnly
	if sync {
		syncMode = git.SyncEmitForceWithLease
	}

	outcome, err := repo.Resign(cmd.Context(), ref, git.ResignOptions{
		Base:   base,
		Sync:   syncMode,
		Remote: cfg.Remote,
		DryRun: dryRun,
	})
	if err != nil {
		if msg, ok := unresolvedRangeMessage(err); ok {
			return finishUsage(cmd, nil, "usage.cli.invalid_range", msg)
		}
		return handleGitError(cmd, nil, err, "internal.git.resign_failed", fmt.Sprintf("resign %s..%s", base, ref))
	}

	result, buildErr := clikitSuccess(cmd, gitresult.RewriteOutcomeData(outcome))
	if buildErr != nil {
		return finishErr(cmd, nil, "internal.result.build_failed", "build result", buildErr)
	}
	return finish(cmd, result)
}

// unresolvedRangeMessage recognizes a *git.CommandError from resolving the
// base..ref commit range itself (git rev-list) as a caller mistake — an
// unresolvable base or ref, e.g. `sign` deriving ref^ for a root commit that
// has no parent — rather than an internal git failure.
func unresolvedRangeMessage(err error) (string, bool) {
	var cmdErr *git.CommandError
	if !errors.As(err, &cmdErr) || len(cmdErr.Args) == 0 || cmdErr.Args[0] != "rev-list" {
		return "", false
	}
	return fmt.Sprintf("could not resolve the commit range: %s", cmdErr.Stderr), true
}
