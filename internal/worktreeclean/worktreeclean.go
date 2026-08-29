// Package worktreeclean holds the one rule set that decides whether a linked
// worktree may be removed. Both cleanup paths -- the standalone `worktree
// remove` verb and `merge --cleanup` -- call Cleanup, so the no-work-loss,
// cardinality, wrong-branch, dirty-tree, and partial-failure rules cannot
// diverge between them: there is a single choke point, not two copies that
// drift. A detached target is judged by the same no-work-loss rule as a
// branch: its checked-out commit stands in for a branch name.
//
// Every reachability question is answered from LOCAL refs alone -- the landing
// target and the count of unreachable commits are resolved before anything is
// removed -- so the decision never touches the network and stays hermetic.
package worktreeclean

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
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
	// DeleteBranch also deletes the target's checked-out branch once the
	// worktree itself has been removed (standalone path only; a no-op when
	// the target is detached, since there is no branch to delete). It is
	// safe precisely because a removal only reaches that point once this
	// same Cleanup call has already proven the branch's commits are
	// reachable from the landing target -- the identical no-work-loss check
	// `branch delete` runs on its own, not a second one invented here.
	DeleteBranch bool
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
	RefusalBranchNotMerged
	RefusalLandingUnresolved
	RefusalDirtyTree
	RefusalUnmergedWork
	RefusalLiveSubWorktree
)

// Result is what Cleanup resolved for one target worktree.
type Result struct {
	Path     string   // resolved target path
	Branches []string // short names of the branches whose reachability was weighed
	Unmerged int      // commits that would be lost (0 unless an unmerged-work refusal)
	// UntrackedPaths and ModifiedPaths are every dirty path found in the
	// target's own tree, relative to the target root and sorted for
	// deterministic rendering (empty unless a dirty-tree refusal).
	UntrackedPaths []string
	ModifiedPaths  []string
	Removed        bool // the worktree was removed (false on a dry run or a refusal)
	// DeletedBranch is the short name of the branch Cleanup deleted, when
	// Options.DeleteBranch asked for it and there was one to delete (empty on
	// a dry run, a refusal, or a detached target).
	DeletedBranch string
	// Refusal, when non-empty, is the named reason cleanup removed nothing. A
	// caller renders it as a hard error (standalone path) or a caveat on an
	// already successful merge (merge path); the reason string is the same
	// either way.
	Refusal     string
	RefusalKind RefusalKind
}

// Cleanup is the single choke point both worktree-cleanup paths call, so the
// no-work-loss, cardinality, wrong-branch, detached-head, dirty-tree, and
// partial-failure rules cannot diverge between them. It removes the linked
// worktree at target only once it has proven, from LOCAL refs and the
// target's own working tree alone, that no commit checked out anywhere
// inside target would be lost and no uncommitted work sits in the target's
// tree. A returned error is an infrastructure failure (a git command that
// could not run); a refusal is carried on the result, never as an error, so
// the merge path can report it as a caveat without unwinding a merge that
// already landed.
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
	// are computed before anything is removed.
	landing, landingOK, err := resolveLandingTarget(ctx, repo, entry, opts)
	if err != nil {
		return nil, err
	}
	if landingOK {
		unmerged, err := CountUnmerged(ctx, repo, subtreeBranches, landing.sha)
		if err != nil {
			return nil, err
		}
		res.Unmerged = unmerged
	}

	// The dirty-tree rule is resolved here too, before git is ever asked to
	// remove anything: git's own `worktree remove` refuses a dirty target with
	// unwrapped, path-free text, so the rule set has to answer this itself to
	// name every offending path. A nested worktree is its own repository
	// boundary -- `git status` reports it as one untracked directory entry --
	// and the cardinality rule above already names it, so it is excluded here
	// rather than double-counted as this target's own dirt.
	untracked, modified, err := dirtyTreeState(ctx, res.Path, nestedRelPaths(list, repo.Dir, res.Path))
	if err != nil {
		return nil, err
	}
	res.UntrackedPaths, res.ModifiedPaths = untracked, modified

	switch {
	case opts.MergedBranches != nil && !BranchAmong(entry.Branch, opts.MergedBranches):
		res.RefusalKind = RefusalBranchNotMerged
		res.Refusal = fmt.Sprintf("the worktree is on %s, which is not among the branches just merged (%s)",
			shortRef(entry.Branch), strings.Join(opts.MergedBranches, ", "))
	case !landingOK:
		res.RefusalKind = RefusalLandingUnresolved
		if opts.MergedBranches == nil {
			res.Refusal = landingUnresolvedMessage(entry, opts)
		} else {
			res.Refusal = fmt.Sprintf("cannot resolve a landing target for %s from local refs; pass --landing-target to name one",
				shortRef(entry.Branch))
		}
	case len(untracked) > 0 || len(modified) > 0:
		res.RefusalKind = RefusalDirtyTree
		res.Refusal = fmt.Sprintf("the worktree has uncommitted work (%s); commit it, ignore it, or delete it deliberately before removing the worktree",
			describeDirty(untracked, modified))
	case res.Unmerged > 0:
		res.RefusalKind = RefusalUnmergedWork
		res.Refusal = fmt.Sprintf("%d commit(s) on %s are not reachable from %s and would be lost",
			res.Unmerged, strings.Join(res.Branches, ", "), landing.name)
	case nested != "":
		res.RefusalKind = RefusalLiveSubWorktree
		res.Refusal = fmt.Sprintf("a live sub-worktree at %s nests under the target; remove it first", nested)
	default:
		return removeTarget(ctx, repo, target, entry, opts, res)
	}
	return res, nil
}

// landingUnresolvedMessage composes the standalone path's landing-target
// refusal. By the time it is called, resolveLandingTarget has already tried
// --landing-target (if given), the branch's own upstream, and the local
// record of the remote's default branch, in that order, and none of them
// resolved -- so the message names every source actually tried and states
// plainly that the repository has no upstream configured, rather than
// pointing at one flag with no explanation of why it is needed.
func landingUnresolvedMessage(entry *git.WorktreeInfo, opts Options) string {
	label := shortRef(entry.Branch)
	if label == "" {
		label = "the detached worktree"
	}
	remote := opts.Remote
	if remote == "" {
		remote = "origin"
	}
	var tried []string
	if opts.LandingTarget != "" {
		tried = append(tried, fmt.Sprintf("--landing-target %s", opts.LandingTarget))
	}
	if entry.Branch != "" {
		tried = append(tried, fmt.Sprintf("%s's upstream", label))
	}
	tried = append(tried, fmt.Sprintf("%s's recorded default branch", remote))
	return fmt.Sprintf("cannot resolve a landing target for %s: tried %s, and none resolved -- this repository has no upstream configured; pass --landing-target to name one",
		label, strings.Join(tried, ", "))
}

// removeTarget performs the actual removal (or, on a dry run, reports that it
// would), recording it on res, and -- when Options.DeleteBranch asked for it
// and there is a branch to delete -- deletes it too. Deleting the branch here
// is safe because the switch in Cleanup only reaches this point once it has
// already proven the branch's commits are reachable from the landing target;
// this is not a second reachability check, it relies on the one already run.
func removeTarget(ctx context.Context, repo *git.Repo, target string, entry *git.WorktreeInfo, opts Options, res *Result) (*Result, error) {
	if opts.DryRun {
		return res, nil
	}
	if err := repo.WorktreeRemove(ctx, target, git.WorktreeRemoveOptions{}); err != nil {
		return nil, err
	}
	res.Removed = true
	if opts.DeleteBranch && entry.Branch != "" {
		branch := shortRef(entry.Branch)
		if _, err := repo.DeleteBranch(ctx, branch, entry.Head, false); err != nil {
			return nil, err
		}
		res.DeletedBranch = branch
	}
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
		sha, ok, err := RevParseLocal(ctx, repo.Dir, "refs/heads/"+branch)
		return landingRef{name: branch, sha: sha}, ok, err
	}
	if opts.LandingTarget != "" {
		sha, ok, err := RevParseLocal(ctx, repo.Dir, opts.LandingTarget)
		return landingRef{name: opts.LandingTarget, sha: sha}, ok, err
	}
	branch := shortRef(entry.Branch)
	if sha, ok, err := RevParseLocal(ctx, repo.Dir, branch+"@{upstream}"); err != nil {
		return landingRef{}, false, err
	} else if ok {
		return landingRef{name: branch + "@{upstream}", sha: sha}, true, nil
	}
	remote := opts.Remote
	if remote == "" {
		remote = "origin"
	}
	if sha, ok, err := RevParseLocal(ctx, repo.Dir, "refs/remotes/"+remote+"/HEAD"); err != nil {
		return landingRef{}, false, err
	} else if ok {
		return landingRef{name: remote + "/HEAD", sha: sha}, true, nil
	}
	return landingRef{}, false, nil
}

// ReachableFrom is the branch-shaped counterpart to Cleanup's own no-work-loss
// check: it resolves the landing target for branch -- a ref that need not be
// checked out in any worktree -- and reports how many of its commits are
// unreachable from it. resolveLandingTarget takes a *git.WorktreeInfo, which a
// branch with no checkout doesn't have, so this builds the minimal one it
// actually reads (the branch name) instead of adding a second landing-target
// resolution chain or reachability predicate: the fallback order
// (explicitTarget, else branch's upstream, else remote's recorded default)
// and CountUnmerged stay the one implementation both entry points share.
//
// ok is false when the landing target does not resolve from local refs -- a
// refusal, not an error; landing and unmerged are only meaningful when ok is
// true.
func ReachableFrom(ctx context.Context, repo *git.Repo, branch, explicitTarget, remote string) (landing string, unmerged int, ok bool, err error) {
	short := shortRef(branch)
	entry := &git.WorktreeInfo{Branch: "refs/heads/" + short}
	target, ok, err := resolveLandingTarget(ctx, repo, entry, Options{LandingTarget: explicitTarget, Remote: remote})
	if err != nil || !ok {
		return "", 0, ok, err
	}
	unmerged, err = CountUnmerged(ctx, repo, []string{short}, target.sha)
	if err != nil {
		return "", 0, false, err
	}
	return target.name, unmerged, true, nil
}

// CountUnmerged sums, across branches, the commits reachable from each branch
// but not from landingSHA -- the work that removing the worktree would lose.
func CountUnmerged(ctx context.Context, repo *git.Repo, branches []string, landingSHA string) (int, error) {
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

// dirtyTreeState reports every untracked path and every modified tracked path
// in the worktree at dir, sorted and relative to dir, other than a path under
// exclude (each entry is a nested worktree's own boundary, "sub/dir/", that a
// different rule already names). An ignored file is never in either list --
// git status omits it unless asked with --ignored -- so an untracked,
// non-ignored file is the only kind of untracked signal this sees. Renames
// are reported as a plain delete plus add rather than paired, keeping each
// entry a single unambiguous path.
func dirtyTreeState(ctx context.Context, dir string, exclude []string) (untracked, modified []string, err error) {
	res, runErr := gitexec.RunGit(ctx, dir, "status", "--porcelain=v1", "-z", "--untracked-files=all", "--no-renames")
	if runErr != nil {
		return nil, nil, runErr
	}
	if res.ExitCode != 0 {
		return nil, nil, &git.CommandError{Args: []string{"status", "--porcelain=v1", "-z"}, ExitCode: res.ExitCode, Stderr: strings.TrimSpace(string(res.Stderr))}
	}
	for _, entry := range strings.Split(strings.TrimRight(string(res.Stdout), "\x00"), "\x00") {
		if entry == "" {
			continue
		}
		code, path := entry[:2], entry[3:]
		if pathExcluded(path, exclude) {
			continue
		}
		if code == "??" {
			untracked = append(untracked, path)
		} else {
			modified = append(modified, path)
		}
	}
	sort.Strings(untracked)
	sort.Strings(modified)
	return untracked, modified, nil
}

// pathExcluded reports whether path (as `git status` reported it) names, or
// falls under, one of exclude's nested-worktree boundaries.
func pathExcluded(path string, exclude []string) bool {
	for _, ex := range exclude {
		if path == ex || strings.HasPrefix(path, ex) {
			return true
		}
	}
	return false
}

// nestedRelPaths returns, for every worktree in list that sits strictly under
// targetResolved, its path relative to targetResolved with a trailing slash --
// the form `git status`, run at targetResolved, reports that boundary in.
func nestedRelPaths(list []git.WorktreeInfo, base, targetResolved string) []string {
	var rels []string
	for _, wt := range list {
		p := ResolvedPath(base, wt.Path)
		if p == targetResolved || !pathUnder(targetResolved, p) {
			continue
		}
		rel, err := filepath.Rel(targetResolved, p)
		if err != nil {
			continue
		}
		rels = append(rels, filepath.ToSlash(rel)+"/")
	}
	return rels
}

// describeDirty renders untracked and modified as the path lists a dirty-tree
// refusal names, omitting either category when it is empty.
func describeDirty(untracked, modified []string) string {
	var parts []string
	if len(untracked) > 0 {
		parts = append(parts, fmt.Sprintf("untracked: %s", strings.Join(untracked, ", ")))
	}
	if len(modified) > 0 {
		parts = append(parts, fmt.Sprintf("modified: %s", strings.Join(modified, ", ")))
	}
	return strings.Join(parts, "; ")
}

// subtreeState returns the commit-ish refs whose reachability must be proven
// -- the branch checked out at targetResolved itself, or its detached HEAD
// commit when it has no branch, plus any nested worktree's branch -- and the
// path of the first live sub-worktree strictly under targetResolved, if any.
// A branch can be checked out in only one worktree, so a branch found here is
// one whose only checkout lies inside the target subtree. Substituting the
// bare commit SHA for a detached target is what lets Cleanup weigh a
// detached HEAD's reachability the same way it weighs a branch's: the target
// itself always contributes something to check, branch or not.
func subtreeState(list []git.WorktreeInfo, base, targetResolved string) (branches []string, nested string) {
	for _, wt := range list {
		p := ResolvedPath(base, wt.Path)
		switch {
		case p == targetResolved:
			branches = append(branches, worktreeCommitRef(wt))
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

// worktreeCommitRef is the ref Cleanup measures a worktree's reachability
// against: its branch when it has one, else its checked-out commit SHA.
func worktreeCommitRef(wt git.WorktreeInfo) string {
	if wt.Branch != "" {
		return wt.Branch
	}
	return wt.Head
}

// RevParseLocal resolves ref to a SHA using only local refs (it never touches
// the network), reporting ok=false when ref does not resolve rather than
// erroring, so an absent upstream or remote-default record is a clean "no".
func RevParseLocal(ctx context.Context, dir, ref string) (sha string, ok bool, err error) {
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
