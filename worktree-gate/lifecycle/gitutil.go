package lifecycle

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/johnrichter/claude-shared-tooling/go/git"
	"github.com/johnrichter/claude-shared-tooling/go/sysops"
)

// runGit runs a git subcommand rooted at dir and returns its trimmed
// stdout, for the handful of plumbing reads (HEAD, current branch, dirty
// check) package git doesn't expose a method for.
func runGit(ctx context.Context, dir string, args ...string) (string, error) {
	res, err := sysops.Run(ctx, "git", args, sysops.Options{Dir: dir})
	if err != nil {
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("git %s: exit %d: %s", strings.Join(args, " "), res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}

// currentHead resolves the commit HEAD points at in dir.
func currentHead(ctx context.Context, dir string) (string, error) {
	return runGit(ctx, dir, "rev-parse", "--verify", "HEAD")
}

// currentBranchName resolves the branch currently checked out in dir, or
// "HEAD" if it is detached.
func currentBranchName(ctx context.Context, dir string) (string, error) {
	return runGit(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
}

// branchExists reports whether name resolves as a local branch in dir.
func branchExists(ctx context.Context, dir, name string) bool {
	_, err := runGit(ctx, dir, "rev-parse", "--verify", "--quiet", "refs/heads/"+name)
	return err == nil
}

// resolveRefSHA resolves ref to the commit SHA it currently points at.
func resolveRefSHA(ctx context.Context, dir, ref string) (string, error) {
	return runGit(ctx, dir, "rev-parse", "--verify", ref)
}

// isDirty reports whether dir's working tree has any uncommitted or
// untracked change — the single fact that must gate every removal in this
// package, since an aged or orphaned worktree may still hold work nothing
// else has a copy of.
func isDirty(ctx context.Context, dir string) (bool, error) {
	out, err := runGit(ctx, dir, "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return out != "", nil
}

// canonPath resolves symlinks so a caller-supplied path matches what `git
// worktree list` reports for the same location, falling back to the input
// when it cannot be resolved (e.g. it doesn't exist yet).
func canonPath(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return p
}

// findWorktree returns the entry in list whose path matches target, if any.
func findWorktree(list []git.WorktreeInfo, target string) (git.WorktreeInfo, bool) {
	want := canonPath(target)
	for _, wt := range list {
		if canonPath(wt.Path) == want {
			return wt, true
		}
	}
	return git.WorktreeInfo{}, false
}
