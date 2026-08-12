package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
	"github.com/johnrichter/claude-shared-tooling/go/git"
	"github.com/johnrichter/git-tools/internal/gitexec"
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
		Use:   "remove <path>",
		Short: "Delete a linked worktree, refusing to discard unmerged work",
		Long: `remove deletes the linked worktree at path after proving its work is
safe to discard. It refuses -- removing nothing and returning a named non-zero
error -- when the worktree's checked-out branch (or a nested worktree's branch)
carries commits unreachable from its landing target, when that target cannot be
resolved from local refs, when a live sub-worktree nests under it, or when its
HEAD is detached. The landing target is --landing-target if given, else the
branch's upstream, else the local record of the remote's default branch; every
step is answered from local refs, never the network.

--force is the one override: it names the branches and unmerged-commit count,
then removes anyway.`,
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

			force, _ := cmd.Flags().GetBool("force")
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			landing, _ := cmd.Flags().GetString("landing-target")

			out, err := cleanupWorktree(cmd.Context(), repo, path, cleanupOptions{
				LandingTarget: landing,
				Remote:        cfg.Remote,
				Force:         force,
				DryRun:        dryRun,
			})
			if err != nil {
				return handleGitError(cmd, err, "internal.git.worktree_remove_failed", fmt.Sprintf("remove worktree %s", path))
			}

			data := cleanupData(out, dryRun)
			if out.Refusal != "" {
				// The standalone path treats a refusal as a hard error: nothing
				// was removed, and the operator must resolve the named condition
				// (or force past it) before the worktree can go.
				if out.RefusalKind == refusalNotRegistered {
					return finishDiagnostic(cmd, clikit.NewNotFound, "not_found.git.worktree_not_registered",
						out.Refusal, clikit.RunTool("git-tools", "worktree", "list"), data)
				}
				return finishDiagnostic(cmd, clikit.NewPreconditionUnmet, cleanupRefusalCode(out.RefusalKind),
					out.Refusal, clikit.Manual("resolve the named condition, or re-run with --force to discard the named work"), data)
			}

			result, buildErr := clikitSuccess(cmd, data)
			if buildErr != nil {
				return finishErr(cmd, "internal.result.build_failed", "build result", buildErr)
			}
			return finish(cmd, result)
		},
	}
	cmd.Flags().Bool("force", false, "override the no-work-loss and cardinality refusals (and git's own dirty-tree refusal), discarding the named work")
	cmd.Flags().Bool("dry-run", false, "run every rule and report the verdict without removing anything")
	cmd.Flags().String("landing-target", "", "ref the worktree's work must already be reachable from (default: the branch's upstream, else the remote's recorded default)")
	return cmd
}

// cleanupOptions configures cleanupWorktree. The two entry points -- the
// standalone `worktree remove` verb and `merge --cleanup` -- differ only in
// these inputs; the rules themselves live in cleanupWorktree so they cannot
// diverge between the paths.
type cleanupOptions struct {
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

// cleanupRefusalKind names why cleanupWorktree removed nothing, so a caller can
// map it to a stable diagnostic code and status.
type cleanupRefusalKind int

const (
	refusalNone cleanupRefusalKind = iota
	refusalNotRegistered
	refusalDetachedHead
	refusalBranchNotMerged
	refusalLandingUnresolved
	refusalUnmergedWork
	refusalLiveSubWorktree
)

// cleanupResult is what cleanupWorktree resolved for one target worktree.
type cleanupResult struct {
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
	RefusalKind cleanupRefusalKind
}

// cleanupWorktree is the single choke point both worktree-cleanup paths call,
// so the no-work-loss, cardinality, wrong-branch, detached-head, force, and
// partial-failure rules cannot diverge between them. It removes the linked
// worktree at target only once it has proven, from LOCAL refs alone, that no
// commit checked out anywhere inside target would be lost -- or once --force
// waives that proof. A returned error is an infrastructure failure (a git
// command that could not run); a refusal is carried on the result, never as an
// error, so the merge path can report it as a caveat without unwinding a merge
// that already landed.
func cleanupWorktree(ctx context.Context, repo *git.Repo, target string, opts cleanupOptions) (*cleanupResult, error) {
	list, err := repo.WorktreeList(ctx)
	if err != nil {
		return nil, err
	}
	res := &cleanupResult{Path: resolvedPath(repo.Dir, target)}

	var entry *git.WorktreeInfo
	for i := range list {
		if resolvedPath(repo.Dir, list[i].Path) == res.Path {
			entry = &list[i]
			break
		}
	}
	if entry == nil {
		res.Refusal = fmt.Sprintf("%q is not a registered worktree of this repository", target)
		res.RefusalKind = refusalNotRegistered
		return res, nil
	}

	// Every branch whose checkout lies within the target subtree -- the target's
	// own branch plus any nested worktree's -- and whether a live sub-worktree
	// nests under it. Both the no-work-loss and the cardinality rule read this.
	subtreeBranches, nested := subtreeState(list, repo.Dir, res.Path)
	res.Branches = shortRefs(subtreeBranches)

	// No-work-loss is resolved up front, from local refs only, so the check
	// stays hermetic: the landing target and the count of unreachable commits
	// are computed before anything is removed and regardless of --force (which
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

	// --force is the one override: it has already weighed and recorded the
	// branches and the unmerged-commit count above, and now proceeds past every
	// refusal.
	if opts.Force {
		res.Forced = true
		return removeTarget(ctx, repo, target, opts, res)
	}

	switch {
	case entry.Branch == "":
		res.RefusalKind = refusalDetachedHead
		res.Refusal = "the worktree's HEAD is detached; cleanup cannot prove its work is reachable from a landing target"
	case opts.MergedBranches != nil && !branchAmong(entry.Branch, opts.MergedBranches):
		res.RefusalKind = refusalBranchNotMerged
		res.Refusal = fmt.Sprintf("the worktree is on %s, which is not among the branches just merged (%s)",
			shortRef(entry.Branch), strings.Join(opts.MergedBranches, ", "))
	case !landingOK:
		res.RefusalKind = refusalLandingUnresolved
		res.Refusal = fmt.Sprintf("cannot resolve a landing target for %s from local refs; pass --landing-target to name one",
			shortRef(entry.Branch))
	case res.Unmerged > 0:
		res.RefusalKind = refusalUnmergedWork
		res.Refusal = fmt.Sprintf("%d commit(s) on %s are not reachable from %s and would be lost",
			res.Unmerged, strings.Join(res.Branches, ", "), landing.name)
	case nested != "":
		res.RefusalKind = refusalLiveSubWorktree
		res.Refusal = fmt.Sprintf("a live sub-worktree at %s nests under the target; remove it first", nested)
	default:
		return removeTarget(ctx, repo, target, opts, res)
	}
	return res, nil
}

// removeTarget performs the actual removal (or, on a dry run, reports that it
// would), recording it on res.
func removeTarget(ctx context.Context, repo *git.Repo, target string, opts cleanupOptions, res *cleanupResult) (*cleanupResult, error) {
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
// path it is the explicit --landing-target, else the branch's upstream, else
// the local record of the remote's default branch. ok is false when none
// resolves -- a refusal, not an error.
func resolveLandingTarget(ctx context.Context, repo *git.Repo, entry *git.WorktreeInfo, opts cleanupOptions) (landingRef, bool, error) {
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
		p := resolvedPath(base, wt.Path)
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

// branchAmong reports whether branchRef's short name matches any of names
// (compared as short names).
func branchAmong(branchRef string, names []string) bool {
	want := shortRef(branchRef)
	for _, n := range names {
		if shortRef(n) == want {
			return true
		}
	}
	return false
}

// cleanupData renders a cleanupResult as the success/diagnostic data map both
// entry points emit.
func cleanupData(out *cleanupResult, dryRun bool) map[string]any {
	data := map[string]any{"path": out.Path, "dry_run": dryRun, "removed": out.Removed}
	if len(out.Branches) > 0 {
		data["branches"] = out.Branches
	}
	if out.Unmerged > 0 {
		data["unmerged_commits"] = out.Unmerged
	}
	if out.Forced {
		data["forced"] = true
	}
	return data
}

// cleanupRefusalCode maps a refusal kind to its stable precondition_unmet
// diagnostic code.
func cleanupRefusalCode(kind cleanupRefusalKind) string {
	switch kind {
	case refusalDetachedHead:
		return "precondition_unmet.git.worktree_detached_head"
	case refusalBranchNotMerged:
		return "precondition_unmet.git.worktree_branch_not_merged"
	case refusalLandingUnresolved:
		return "precondition_unmet.git.worktree_landing_unresolved"
	case refusalUnmergedWork:
		return "precondition_unmet.git.worktree_unmerged_work"
	case refusalLiveSubWorktree:
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
