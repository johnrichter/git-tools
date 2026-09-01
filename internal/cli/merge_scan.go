package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/johnrichter/claude-shared-tooling/go/git"
	"github.com/johnrichter/git-tools/internal/gitexec"
)

// prospectiveMergeScanDir prepares the tree merging sources into target
// would actually produce, not target's current, pre-merge tree, so the
// caller's own scanGate call can scan that result instead. This is what
// makes it possible for a merge to land a remediation into a target whose
// current tree already carries pre-existing findings: scanning whatever the
// target currently has checked out can never distinguish that remediating
// merge from one that makes things worse, because it never looks at either
// merge's actual result.
//
// It works by trial-merging sources into a disposable, detached scratch
// worktree checked out at target's current tip (oldHead) -- a linked worktree
// shares this repository's own object and ref database, so every named source
// resolves exactly as it would for the real merge.
//
// The trial is deliberately hermetic, because the caller treats a failed
// trial as "nothing to scan" and falls through to the real merge: any way the
// trial can fail where the real merge would still succeed is a way for
// content to land unscanned. So the trial commits nothing (--no-commit) and
// runs with hooks disabled, which removes every difference that could make
// the two disagree -- it mints no commit, so it needs no committer identity,
// no signing key and no commit message, and it fires none of the merge hooks
// that would otherwise judge git's own default merge message rather than the
// operator's --message. Not committing costs nothing: only the resulting tree
// content is ever read. --no-ff keeps a fast-forwardable source going through
// the same path, which yields the same tree; a fast-forward-only merge keeps
// its own mode so a source the target cannot fast-forward to fails the trial
// exactly as it will fail the real merge.
//
// What is left is a trial that fails only where the real merge cannot
// succeed either: a genuine conflict, or a source the merge cannot use at
// all. Both already have precise, established handling further down -- the
// signing gate refuses a source that is not a mergeable branch, and the real
// merge reports the conflict -- so a failed trial returns dir "" and lets
// them diagnose it, rather than adding a second, less-informed verdict.
//
// cleanup discards the scratch worktree and must be called exactly once by
// the caller (typically deferred), regardless of dir or err -- it is always
// non-nil, and safe to call even when this function itself failed early.
func prospectiveMergeScanDir(cmd *cobra.Command, repo *git.Repo, oldHead string, sources []string, ff git.FastForward, data map[string]any) (dir string, cleanup func(), err error) {
	noop := func() {}

	// The scratch worktree lives one level inside a directory this process
	// owns, so its own path is free without ever deleting a path some other
	// process could win the race to recreate.
	tempRoot, mkErr := os.MkdirTemp("", "git-tools-merge-scan-")
	if mkErr != nil {
		return "", noop, finishErr(cmd, data, "internal.git.merge_scan_worktree_failed", "prepare a scratch worktree to test the merge result", mkErr)
	}
	discardTempRoot := func() { os.RemoveAll(tempRoot) }
	scratchDir := filepath.Join(tempRoot, "worktree")
	// A hooks directory that is deliberately never created: git finds no hook
	// to run there, for this one invocation only, without touching the
	// repository's shared configuration.
	noHooksDir := filepath.Join(tempRoot, "no-hooks")

	if err := repo.WorktreeAdd(cmd.Context(), scratchDir, oldHead, git.WorktreeAddOptions{}); err != nil {
		return "", discardTempRoot, finishErr(cmd, data, "internal.git.merge_scan_worktree_failed", "prepare a scratch worktree to test the merge result", err)
	}

	cleanup = func() {
		// An uncommitted trial merge leaves the scratch worktree modified,
		// and removing a modified worktree is refused (no call site here may
		// force one), so undo the merge first. A scratch worktree with no
		// merge left in it simply has nothing to undo.
		_, _ = gitexec.RunGit(cmd.Context(), scratchDir, "merge", "--abort")
		if rmErr := repo.WorktreeRemove(cmd.Context(), scratchDir, git.WorktreeRemoveOptions{}); rmErr != nil {
			fmt.Fprintf(os.Stderr, "warning: could not remove scratch merge-scan worktree %s: %v\n", scratchDir, rmErr)
		}
		discardTempRoot()
	}

	args := []string{"-c", "core.hooksPath=" + noHooksDir, "merge"}
	if ff == git.FastForwardOnly {
		args = append(args, "--ff-only")
	} else {
		args = append(args, "--no-ff", "--no-commit")
	}
	args = append(args, sources...)
	res, runErr := gitexec.RunGit(cmd.Context(), scratchDir, args...)
	if runErr != nil {
		return "", cleanup, finishErr(cmd, data, "internal.git.merge_scan_trial_failed", "test the merge result", runErr)
	}
	if res.ExitCode != 0 {
		return "", cleanup, nil
	}
	return scratchDir, cleanup, nil
}
