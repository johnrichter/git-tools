// Package worktreeclean holds the one rule set that decides whether a linked
// worktree may be removed. Both cleanup paths -- the standalone `worktree
// remove` verb and `merge --cleanup` -- call Cleanup, so the no-work-loss,
// cardinality, wrong-branch, detached-head, force, and partial-failure rules
// cannot diverge between them: there is a single choke point, not two copies
// that drift.
//
// Every reachability question is answered from LOCAL refs alone -- the landing
// target and the count of unreachable commits are resolved before anything is
// removed -- so the decision never touches the network and stays hermetic.
package worktreeclean

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/johnrichter/claude-shared-tooling/go/git"

	"github.com/johnrichter/git-tools/internal/gitexec"
)

// Options configures Cleanup. The two entry points -- the standalone `worktree
// remove` verb and `merge --cleanup` -- differ only in these inputs; the rules
// themselves live in Cleanup so they cannot diverge between the paths.
type Options struct {
	// LandingTarget, when set, is the ref every branch checked out inside the
	// target must already be reachable from (the standalone path's explicit
	// override). Empty resolves the target from the branch's upstream, then the
	// local record of the remote's default branch.
	LandingTarget string
	// Remote names the remote whose recorded default branch is the last-resort
	// landing target when a branch has no upstream (standalone path only).
	Remote string
	// MergedBranches selects the merge path when non-nil: cleanup refuses unless
	// the target's checked-out branch is among them, and the landing target is
	// the branch the merge landed onto (the branch checked out in repo.Dir).
	MergedBranches []string
	// Force overrides every refusal -- the one override -- and still reports the
	// branches weighed and the unmerged-commit count it proceeded past.
	Force bool
	// DryRun runs every rule and reports the verdict without removing anything.
	DryRun bool
}

// RefusalKind names why Cleanup removed nothing, so a caller can map it to a
// stable diagnostic code and status.
type RefusalKind int

// The refusal kinds Cleanup can report. RefusalNone is the zero value: no
// refusal was raised.
const (
	RefusalNone RefusalKind = iota
	RefusalNotRegistered
	RefusalDetachedHead
	RefusalBranchNotMerged
	RefusalLandingUnresolved
	RefusalUnmergedWork
	RefusalLiveSubWorktree
)

// Result is what Cleanup resolved for one target worktree.
type Result struct {
	Path     string   // resolved target path
	Branches []string // short names of the branches whose reachability was weighed
	Unmerged int      // commits that would be lost (0 unless an unmerged-work refusal, or a forced override past one)
	Removed  bool     // the worktree was removed (false on a dry run or a refusal)
	Forced   bool     // a refusal was overridden by Force
	// Refusal, when non-empty, is the named reason cleanup removed nothing. A
	// caller renders it as a hard error (standalone path) or a caveat on an
	// already successful merge (merge path); the reason string is the same
	// either way.
	Refusal     string
	RefusalKind RefusalKind
}

// Cleanup is the single choke point both worktree-cleanup paths call, so the
// no-work-loss, cardinality, wrong-branch, detached-head, force, and
// partial-failure rules cannot diverge between them. It removes the linked
// worktree at target only once it has proven, from LOCAL refs alone, that no
// commit checked out anywhere inside target would be lost -- or once Force
// waives that proof. A returned error is an infrastructure failure (a git
// command that could not run); a refusal is carried on the result, never as an
// error, so the merge path can report it as a caveat without unwinding a merge
// that already landed.
func Cleanup(ctx context.Context, repo *git.Repo, target string, opts Options) (*Result, error) {
	list, err := repo.WorktreeList(ctx)
	if err != nil {
		return nil, err
	}
	res := &Result{Path: ResolvedPath(repo.Dir, target)}

	var entry *git.WorktreeInfo
	for i := range list {
		if ResolvedPath(repo.Dir, list[i].Path) == res.Path {
			entry = &list[i]
			break
		}
	}
	if entry == nil {
		res.Refusal = fmt.Sprintf("%q is not a registered worktree of this repository", target)
		res.RefusalKind = RefusalNotRegistered
		return res, nil
	}

	// Every branch whose checkout lies within the target subtree -- the target's
	// own branch plus any nested worktree's -- and whether a live sub-worktree
	// nests under it. Both the no-work-loss and the cardinality rule read this.
	subtreeBranches, nested := subtreeState(list, repo.Dir, res.Path)
	res.Branches = shortRefs(subtreeBranches)

	// No-work-loss is resolved up front, from local refs only, so the check
	// stays hermetic: the landing target and the count of unreachable commits
	// are computed before anything is removed and regardless of Force (which
	// still reports the count it overrode).
	landing, landingOK, err := resolveLandingTarget(ctx, repo, entry, opts)
	if err != nil {
		return nil, err
	}
	if landingOK {
		unmerged, err := countUnmerged(ctx, repo, subtreeBranches, landing.sha)
		if err != nil {
			return nil, err
		}
		res.Unmerged = unmerged
	}

	// Force is the one override: it has already weighed and recorded the
	// branches and the unmerged-commit count above, and now proceeds past every
	// refusal.
	if opts.Force {
		res.Forced = true
		return removeTarget(ctx, repo, target, opts, res)
	}

	switch {
	case entry.Branch == "":
		res.RefusalKind = RefusalDetachedHead
		res.Refusal = "the worktree's HEAD is detached; cleanup cannot prove its work is reachable from a landing target"
	case opts.MergedBranches != nil && !BranchAmong(entry.Branch, opts.MergedBranches):
		res.RefusalKind = RefusalBranchNotMerged
		res.Refusal = fmt.Sprintf("the worktree is on %s, which is not among the branches just merged (%s)",
			shortRef(entry.Branch), strings.Join(opts.MergedBranches, ", "))
	case !landingOK:
		res.RefusalKind = RefusalLandingUnresolved
		res.Refusal = fmt.Sprintf("cannot resolve a landing target for %s from local refs; pass --landing-target to name one",
			shortRef(entry.Branch))
	case res.Unmerged > 0:
		res.RefusalKind = RefusalUnmergedWork
		res.Refusal = fmt.Sprintf("%d commit(s) on %s are not reachable from %s and would be lost",
			res.Unmerged, strings.Join(res.Branches, ", "), landing.name)
	case nested != "":
		res.RefusalKind = RefusalLiveSubWorktree
		res.Refusal = fmt.Sprintf("a live sub-worktree at %s nests under the target; remove it first", nested)
	default:
		return removeTarget(ctx, repo, target, opts, res)
	}
	return res, nil
}

// removeTarget performs the actual removal (or, on a dry run, reports that it
// would), recording it on res.
func removeTarget(ctx context.Context, repo *git.Repo, target string, opts Options, res *Result) (*Result, error) {
	if opts.DryRun {
		return res, nil
	}
	if err := repo.WorktreeRemove(ctx, target, git.WorktreeRemoveOptions{Force: opts.Force}); err != nil {
		return nil, err
	}
	res.Removed = true
	return res, nil
}

// landingRef is a resolved landing target: the ref name used to report it, and
// the SHA reachability is measured against.
type landingRef struct {
	name string
	sha  string
}

// resolveLandingTarget resolves the ref the target's work must already be
// reachable from, using only local refs. On the merge path it is the branch the
// merge landed onto (the branch checked out in repo.Dir). On the standalone
// path it is the explicit LandingTarget, else the branch's upstream, else the
// local record of the remote's default branch. ok is false when none
// resolves -- a refusal, not an error.
func resolveLandingTarget(ctx context.Context, repo *git.Repo, entry *git.WorktreeInfo, opts Options) (landingRef, bool, error) {
	if opts.MergedBranches != nil {
		branch, err := gitexec.CurrentBranch(ctx, repo.Dir)
		if err != nil {
			return landingRef{}, false, err
		}
		if branch == "" {
			return landingRef{}, false, nil
		}
		sha, ok, err := revParseLocal(ctx, repo.Dir, "refs/heads/"+branch)
		return landingRef{name: branch, sha: sha}, ok, err
	}
	if opts.LandingTarget != "" {
		sha, ok, err := revParseLocal(ctx, repo.Dir, opts.LandingTarget)
		return landingRef{name: opts.LandingTarget, sha: sha}, ok, err
	}
	branch := shortRef(entry.Branch)
	if sha, ok, err := revParseLocal(ctx, repo.Dir, branch+"@{upstream}"); err != nil {
		return landingRef{}, false, err
	} else if ok {
		return landingRef{name: branch + "@{upstream}", sha: sha}, true, nil
	}
	remote := opts.Remote
	if remote == "" {
		remote = "origin"
	}
	if sha, ok, err := revParseLocal(ctx, repo.Dir, "refs/remotes/"+remote+"/HEAD"); err != nil {
		return landingRef{}, false, err
	} else if ok {
		return landingRef{name: remote + "/HEAD", sha: sha}, true, nil
	}
	return landingRef{}, false, nil
}

// countUnmerged sums, across branches, the commits reachable from each branch
// but not from landingSHA -- the work that removing the worktree would lose.
func countUnmerged(ctx context.Context, repo *git.Repo, branches []string, landingSHA string) (int, error) {
	total := 0
	for _, b := range branches {
		res, err := gitexec.RunGit(ctx, repo.Dir, "rev-list", "--count", landingSHA+".."+b)
		if err != nil {
			return 0, err
		}
		if res.ExitCode != 0 {
			return 0, &git.CommandError{Args: []string{"rev-list", "--count", landingSHA + ".." + b}, ExitCode: res.ExitCode, Stderr: strings.TrimSpace(string(res.Stderr))}
		}
		n, convErr := strconv.Atoi(strings.TrimSpace(string(res.Stdout)))
		if convErr != nil {
			return 0, fmt.Errorf("parse rev-list count for %s: %w", b, convErr)
		}
		total += n
	}
	return total, nil
}

// subtreeState returns the branches checked out at or under targetResolved and
// the path of the first live sub-worktree strictly under it, if any. A branch
// can be checked out in only one worktree, so a branch found here is one whose
// only checkout lies inside the target subtree.
func subtreeState(list []git.WorktreeInfo, base, targetResolved string) (branches []string, nested string) {
	for _, wt := range list {
		p := ResolvedPath(base, wt.Path)
		switch {
		case p == targetResolved:
			if wt.Branch != "" {
				branches = append(branches, wt.Branch)
			}
		case pathUnder(targetResolved, p):
			if wt.Branch != "" {
				branches = append(branches, wt.Branch)
			}
			if nested == "" {
				nested = wt.Path
			}
		}
	}
	return branches, nested
}

// revParseLocal resolves ref to a SHA using only local refs (it never touches
// the network), reporting ok=false when ref does not resolve rather than
// erroring, so an absent upstream or remote-default record is a clean "no".
func revParseLocal(ctx context.Context, dir, ref string) (sha string, ok bool, err error) {
	res, runErr := gitexec.RunGit(ctx, dir, "rev-parse", "--verify", "-q", ref)
	if runErr != nil {
		return "", false, runErr
	}
	if res.ExitCode != 0 {
		return "", false, nil
	}
	return strings.TrimSpace(string(res.Stdout)), true, nil
}

// ResolvedPath returns p's absolute, symlink-resolved form. A relative p is
// joined against base first; an already-absolute p is used as-is. It falls
// back to the absolute (unresolved) form, then p itself, when resolution
// fails.
func ResolvedPath(base, p string) string {
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

// pathUnder reports whether p sits strictly under root.
func pathUnder(root, p string) bool {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// shortRef strips a refs/heads/ prefix, so a WorktreeInfo.Branch and a
// caller-supplied branch name compare as the same short name.
func shortRef(ref string) string {
	return strings.TrimPrefix(ref, "refs/heads/")
}

// shortRefs maps shortRef over refs.
func shortRefs(refs []string) []string {
	out := make([]string, len(refs))
	for i, r := range refs {
		out[i] = shortRef(r)
	}
	return out
}

// BranchAmong reports whether branchRef's short name matches any of names
// (compared as short names).
func BranchAmong(branchRef string, names []string) bool {
	want := shortRef(branchRef)
	for _, n := range names {
		if shortRef(n) == want {
			return true
		}
	}
	return false
}
