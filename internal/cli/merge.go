package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
	"github.com/johnrichter/claude-shared-tooling/go/git"
)

func newMergeCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "merge <branch>...",
		Short: "Merge one or more branches into the currently checked-out branch (two or more performs an octopus merge)",
		Long: `merge is the sanctioned channel for landing a branch, and it signs the
history it lands: before anything is merged, the signing gate re-signs every
commit each source branch carries beyond its fork point with the branch being
merged into. The gate runs on every merge — this verb is the landing channel
by construction, and nothing here knows which branches are protected.

Per source, in order: the fork point is computed rather than supplied, a
range with no commits or one whose every commit already verifies is skipped,
and a rewrite is computed as a dry run before it is applied. A source
carrying unsigned commits that cannot be signed is refused, never merged
unsigned. A source that is not a local branch, or that shares no history with
the branch being merged into, is refused rather than merged: neither gives the
gate a range it can re-sign.

The gate covers the incoming commits, not the merge commit. A merge that does
not fast-forward mints a merge commit of its own, and git signs that one only
when commit.gpgsign is set — which this verb neither sets nor checks. Enable
it wherever an unsigned merge commit on the target branch is unacceptable.

Exit codes:
  0  success              the sources merged (with any re-signing reported)
  10 caveats              the merge landed, but an opted-in cleanup did not complete
  30 precondition_unmet   the signing gate refused; nothing was merged
  40 not_found            --repo is not a git working tree
  41 conflict             the merge would conflict; it was aborted
  50 usage                a flag value is not valid
  90 internal             an underlying git command failed unexpectedly`,
		Args:    cobra.MinimumNArgs(1),
		Example: "  git-tools merge --message \"merge release\" release/1.2",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(cmd.Flags())
			if err != nil {
				return finishErr(cmd, "internal.config.load_failed", "load configuration", err)
			}
			repo, repoErr := requireRepo(cmd, cfg)
			if repo == nil {
				return repoErr
			}

			message, _ := cmd.Flags().GetString("message")
			ffMode, _ := cmd.Flags().GetString("fast-forward")
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			cleanup, _ := cmd.Flags().GetBool("cleanup")
			force, _ := cmd.Flags().GetBool("force")

			ff, err := parseFastForward(ffMode)
			if err != nil {
				return finishUsage(cmd, "usage.cli.invalid_fast_forward", err.Error())
			}

			// The signing gate runs before the merge, so a refusal leaves the
			// checked-out branch exactly where it was.
			target, err := currentBranch(cmd.Context(), repo.Dir)
			if err != nil {
				return finishErr(cmd, "internal.git.head_check_failed", "resolve the branch being merged into", err)
			}
			if target == "" {
				target = "HEAD"
			}
			gated, refusal := gateSigning(cmd.Context(), repo, target, args, dryRun)
			if refusal != nil {
				return finishDiagnostic(cmd, clikit.NewPreconditionUnmet, refusal.code, refusal.message, refusal.advice, refusal.context())
			}

			result, err := repo.Merge(cmd.Context(), args, git.MergeOptions{Message: message, FastForward: ff, DryRun: dryRun})
			if err != nil {
				return handleGitError(cmd, err, "internal.git.merge_failed", fmt.Sprintf("merge %s", strings.Join(args, " ")))
			}

			data := map[string]any{"dry_run": result.DryRun, "signing_gate": gated}
			if result.DryRun {
				data["would_merge"] = result.WouldMerge
			} else {
				data["new_head"] = result.NewHead
			}

			// Cleanup runs only on a real, successful merge, and only when opted
			// in. It shares one rule set with the standalone `worktree remove`
			// verb, so the no-work-loss guard cannot diverge between the paths. A
			// cleanup that refuses or fails never unwinds the merge that already
			// landed: it is reported as a caveat on the success.
			if cleanup && !result.DryRun {
				cleaned, unremoved := cleanupMergedWorktrees(cmd.Context(), repo, args, cfg.Remote, force)
				if len(cleaned) > 0 {
					data["cleaned_worktrees"] = cleaned
				}
				if len(unremoved) > 0 {
					data["unremoved_worktrees"] = unremoved
					reason, _ := unremoved[0]["reason"].(string)
					return finishCaveat(cmd, data, "caveats.git.worktree_cleanup_incomplete",
						fmt.Sprintf("the merge landed, but %d worktree(s) were not removed: %s", len(unremoved), reason),
						clikit.Manual("remove the named worktree(s) manually, or re-run with --force to discard the named work"),
						map[string]any{"unremoved": len(unremoved)})
				}
			}

			clikitResult, buildErr := clikitSuccess(cmd, data)
			if buildErr != nil {
				return finishErr(cmd, "internal.result.build_failed", "build result", buildErr)
			}
			return finish(cmd, clikitResult)
		},
	}
	cmd.Flags().String("message", "", "commit message for a real (non-fast-forward) merge")
	cmd.Flags().String("fast-forward", "allow", "fast-forward behavior: allow, never, or only")
	cmd.Flags().Bool("dry-run", false, "merge into the index and report clean-mergeability, always aborting afterward")
	cmd.Flags().Bool("cleanup", false, "after a successful merge, remove each merged branch's worktree once its work has safely landed")
	cmd.Flags().Bool("force", false, "with --cleanup, override the no-work-loss and cardinality refusals, discarding the named work")
	return cmd
}

// What the signing gate did to one merge source, as reported in the result's
// signing_gate list.
const (
	gateResigned      = "resigned"       // the range was rewritten into signed equivalents
	gateWouldResign   = "would_resign"   // a dry-run merge: the rewrite was computed, not applied
	gateAlreadySigned = "already_signed" // every commit in range already verifies
	gateEmptyRange    = "empty_range"    // the source is already contained in the target
)

// signingGateError is a refusal from the signing gate: a merge that must not
// proceed. rewritten names the sources already re-signed before the refusal —
// an octopus merge gates its sources in turn, so a late refusal can follow
// earlier rewrites, and those are reported with their backup tags rather than
// unwound.
type signingGateError struct {
	code      string
	message   string
	advice    clikit.Triage
	source    string
	rewritten []map[string]any
}

func (e *signingGateError) context() map[string]any {
	ctx := map[string]any{"source": e.source}
	if len(e.rewritten) > 0 {
		ctx["rewritten"] = e.rewritten
	}
	return ctx
}

// gateSigning re-signs, or refuses, every source of a merge before the merge
// itself runs. Each source is gated independently and in order: its fork point
// with target is computed here rather than supplied, an empty or
// already-verifying range is skipped, and the rewrite is computed as a dry run
// before it is applied. A source that carries unsigned commits and cannot be
// signed is refused — the merge verb never lands unsigned incoming commits.
//
// The merge commit itself is outside this gate: git.MergeOptions exposes no
// signing option, so `git merge` signs the commit it mints only when
// commit.gpgsign is set in the repository's own configuration. A non-fast-
// forward merge can therefore leave an unsigned tip on the target branch even
// though every commit this gate saw was signed.
//
// dryRun mirrors the merge's own --dry-run: the gate reports the rewrite it
// would apply and moves no ref, so a dry-run merge stays free of side effects.
//
// Re-signing a branch that is checked out in a linked worktree does not
// disturb it: Resign preserves each commit's tree object exactly, so that
// worktree's files and index still match the branch's new tip.
func gateSigning(ctx context.Context, repo *git.Repo, target string, sources []string, dryRun bool) ([]map[string]any, *signingGateError) {
	var (
		gated     []map[string]any
		rewritten []map[string]any
		signable  bool // whether signing has already been proven available here
	)
	refuse := func(source, code, message string, advice clikit.Triage) *signingGateError {
		return &signingGateError{code: code, message: message, advice: advice, source: source, rewritten: rewritten}
	}

	for _, source := range sources {
		ref := "refs/heads/" + source
		record := map[string]any{"source": source}

		isBranch, err := refExists(ctx, repo.Dir, "heads", source)
		if err != nil {
			return nil, refuse(source, "precondition_unmet.git.signing_gate_failed",
				sanitizeMessage(fmt.Sprintf("could not check whether %s is a local branch: %v", source, err)),
				clikit.Manual("nothing was merged; resolve the underlying git failure and re-run"))
		}
		if !isBranch {
			return nil, refuse(source, "precondition_unmet.git.merge_source_not_branch",
				fmt.Sprintf("%q is not a local branch, so the signing gate cannot re-sign what the merge would land", source),
				clikit.Manual(fmt.Sprintf("create a local branch at %s and merge that instead", source)))
		}

		base, hasBase, err := mergeBase(ctx, repo.Dir, target, source)
		if err != nil {
			return nil, refuse(source, "precondition_unmet.git.signing_gate_failed",
				sanitizeMessage(fmt.Sprintf("could not compute the fork point of %s with %s: %v", source, target, err)),
				clikit.Manual("nothing was merged; resolve the underlying git failure and re-run"))
		}
		if !hasBase {
			return nil, refuse(source, "precondition_unmet.git.merge_no_fork_point",
				fmt.Sprintf("%s and %s share no common ancestor, so there is no range for the signing gate to sign", source, target),
				clikit.Manual("rebase the unrelated history onto the target branch first, then merge"))
		}
		record["base"] = base

		codes, err := rangeSigStates(ctx, repo.Dir, base, ref)
		if err != nil {
			return nil, refuse(source, "precondition_unmet.git.signing_gate_failed",
				sanitizeMessage(fmt.Sprintf("could not read signature status over %s..%s: %v", base, source, err)),
				clikit.Manual("nothing was merged; resolve the underlying git failure and re-run"))
		}
		record["commits"] = len(codes)

		// An empty range is a skip, not a failure: the source is already
		// contained in the target, and Resign rejects an empty range outright.
		if len(codes) == 0 {
			record["action"] = gateEmptyRange
			gated = append(gated, record)
			continue
		}
		if allVerify(codes) {
			record["action"] = gateAlreadySigned
			gated = append(gated, record)
			continue
		}

		if !signable {
			available, detail, err := signingAvailable(ctx, repo.Dir)
			if err != nil {
				return nil, refuse(source, "precondition_unmet.git.signing_gate_failed",
					sanitizeMessage(fmt.Sprintf("could not test whether git can sign in %s: %v", repo.Dir, err)),
					clikit.Manual("nothing was merged; resolve the underlying git failure and re-run"))
			}
			if !available {
				return nil, refuse(source, "precondition_unmet.git.signing_key_unresolved",
					sanitizeMessage(fmt.Sprintf("no key resolved for commit signing, so merging %s would land unsigned commits: %s", source, detail)),
					clikit.Manual("configure a signing key (gpg.format plus user.signingkey, or this environment's signing setup) and re-run; nothing was merged"))
			}
			signable = true
		}

		plan, err := repo.Resign(ctx, ref, git.ResignOptions{Base: base, DryRun: true})
		if err != nil {
			return nil, refuse(source, "precondition_unmet.git.signing_gate_failed",
				sanitizeMessage(fmt.Sprintf("re-signing %s..%s could not be computed: %v", base, source, err)),
				clikit.Manual("nothing was merged; resolve the underlying git failure and re-run"))
		}
		record["old_head"] = plan.OldHead
		record["new_head"] = plan.NewHead
		if dryRun {
			record["action"] = gateWouldResign
			gated = append(gated, record)
			continue
		}

		applied, err := repo.Resign(ctx, ref, git.ResignOptions{Base: base})
		if err != nil {
			return nil, refuse(source, "precondition_unmet.git.signing_gate_failed",
				sanitizeMessage(fmt.Sprintf("re-signing %s..%s failed: %v", base, source, err)),
				clikit.Manual("nothing was merged; recover any listed rewrite from its backup tag if the rewrite is unwanted, then re-run"))
		}
		record["action"] = gateResigned
		record["new_head"] = applied.NewHead
		record["backup_tag"] = applied.BackupTag
		gated = append(gated, record)
		rewritten = append(rewritten, map[string]any{"source": source, "old_head": applied.OldHead, "new_head": applied.NewHead, "backup_tag": applied.BackupTag})
	}
	return gated, nil
}

// mergeBase computes the fork point of two committish arguments. ok is false
// when they share no common ancestor (merge-base's exit code 1), which is a
// real answer rather than a failure.
func mergeBase(ctx context.Context, dir, a, b string) (sha string, ok bool, err error) {
	args := []string{"merge-base", a, b}
	res, err := runGit(ctx, dir, args...)
	if err != nil {
		return "", false, err
	}
	switch res.ExitCode {
	case 0:
		return strings.TrimSpace(string(res.Stdout)), true, nil
	case 1:
		return "", false, nil
	default:
		return "", false, &git.CommandError{Args: args, ExitCode: res.ExitCode, Stderr: strings.TrimSpace(string(res.Stderr))}
	}
}

// rangeSigStates returns git's own signature-status code (%G?) for every
// commit in base..ref, oldest last, one per commit. It doubles as the range's
// commit count: git emits exactly one code per commit, so an empty result
// means an empty range.
func rangeSigStates(ctx context.Context, dir, base, ref string) ([]string, error) {
	args := []string{"log", "--format=%G?", base + ".." + ref}
	res, err := runGit(ctx, dir, args...)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, &git.CommandError{Args: args, ExitCode: res.ExitCode, Stderr: strings.TrimSpace(string(res.Stderr))}
	}
	out := strings.TrimSpace(string(res.Stdout))
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// allVerify reports whether every code is a signature git verified: G (good)
// or U (good, but the signer carries no trust path — irrelevant to whether the
// commit is signed). N (unsigned), E (key unavailable) and the bad/expired/
// revoked codes are all work for the gate.
func allVerify(codes []string) bool {
	for _, code := range codes {
		if strings.TrimSpace(code) != "G" && strings.TrimSpace(code) != "U" {
			return false
		}
	}
	return true
}

// signingAvailable reports whether git can actually produce a signature in
// dir, by signing a throwaway commit object with the same machinery the
// rewrite uses — a definitive answer, where reading configuration would only
// be a guess at whether a named key resolves. commit-tree always writes, so
// the probe leaves one unreferenced commit object behind for a future git gc,
// exactly as a dry-run re-sign does. detail carries git's own reason when
// signing is unavailable.
func signingAvailable(ctx context.Context, dir string) (available bool, detail string, err error) {
	tree, err := runGit(ctx, dir, "rev-parse", "--verify", "HEAD^{tree}")
	if err != nil {
		return false, "", err
	}
	if tree.ExitCode != 0 {
		return false, "", &git.CommandError{Args: []string{"rev-parse", "HEAD^{tree}"}, ExitCode: tree.ExitCode, Stderr: strings.TrimSpace(string(tree.Stderr))}
	}
	probe, err := runGit(ctx, dir, "commit-tree", "-S", "-m", "git-tools signing probe", strings.TrimSpace(string(tree.Stdout)))
	if err != nil {
		return false, "", err
	}
	if probe.ExitCode != 0 {
		return false, strings.TrimSpace(string(probe.Stderr)), nil
	}
	return true, "", nil
}

// cleanupMergedWorktrees removes the worktree of each just-merged branch,
// through the shared cleanupWorktree rule set. It never returns an error: a
// merge has already landed, so a refusal or an infrastructure failure is folded
// into the unremoved list for the caller to report as a caveat rather than
// unwound. cleaned holds the paths removed; each unremoved entry names a path
// (when known) and the reason it stayed.
func cleanupMergedWorktrees(ctx context.Context, repo *git.Repo, mergedBranches []string, remote string, force bool) (cleaned []string, unremoved []map[string]any) {
	list, err := repo.WorktreeList(ctx)
	if err != nil {
		return nil, []map[string]any{{"reason": sanitizeMessage(fmt.Sprintf("list worktrees: %v", err))}}
	}
	for _, wt := range list {
		if wt.Branch == "" || !branchAmong(wt.Branch, mergedBranches) {
			continue
		}
		out, err := cleanupWorktree(ctx, repo, wt.Path, cleanupOptions{
			MergedBranches: mergedBranches,
			Remote:         remote,
			Force:          force,
		})
		switch {
		case err != nil:
			unremoved = append(unremoved, map[string]any{"path": wt.Path, "reason": sanitizeMessage(err.Error())})
		case out.Refusal != "":
			unremoved = append(unremoved, map[string]any{"path": out.Path, "reason": out.Refusal})
		case out.Removed:
			cleaned = append(cleaned, out.Path)
		}
	}
	return cleaned, unremoved
}

// parseFastForward maps the --fast-forward flag's string value onto
// git.FastForward.
func parseFastForward(s string) (git.FastForward, error) {
	switch s {
	case "allow":
		return git.FastForwardAllow, nil
	case "never":
		return git.FastForwardNever, nil
	case "only":
		return git.FastForwardOnly, nil
	default:
		return 0, fmt.Errorf("--fast-forward %q is not one of allow, never, only", s)
	}
}
