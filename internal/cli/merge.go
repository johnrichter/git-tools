package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
	"github.com/johnrichter/claude-shared-tooling/go/git"
	"github.com/johnrichter/git-tools/internal/commitmsg"
	"github.com/johnrichter/git-tools/internal/gitexec"
	"github.com/johnrichter/git-tools/internal/signing"
	"github.com/johnrichter/git-tools/internal/worktreeclean"
)

// Result-data keys merge's result carries once the values they name are
// resolved. Both are strings.
//
// dataKeyRepo names the resolved absolute repository path. Present on every
// exit once --repo has resolved to a git working tree (requireRepo has
// returned a *git.Repo) — absent only on the two exits that run before that:
// a load-configuration failure, and requireRepo's own refusal.
//
// dataKeyTarget names the branch being merged into (empty for a detached
// HEAD, which is itself a value merge reports rather than omits). Present on
// every exit from the point the current branch has been read onward — absent,
// alongside dataKeyRepo, on the two exits above, and alongside dataKeyRepo's
// presence, on the two exits that run with the repository resolved but before
// that read: an invalid --fast-forward value, and a failure doing the read
// itself.
const (
	dataKeyRepo   = "repo"
	dataKeyTarget = "target"
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

The gate covers the incoming commits, and the merge commit is covered too. A
merge that will mint a commit of its own — a forbidden fast-forward, an octopus
of two or more sources, or a single source the target cannot fast-forward to —
is proven signable first and then merged with signing on, so the minted commit
is signed regardless of commit.gpgsign. If git cannot sign, the merge refuses
before touching the target branch rather than landing an unsigned tip. Once a
merge commit lands, its own signature is verified before the merge is
reported as success — a signature that fails that check is reported as a
caveat naming the merge unsigned, never silently as success.

A real merge's result reports commits_landed — the commits, via rev-list,
newly reachable from the resulting head — so a caller never recomputes it.
--push, opted in like --cleanup, publishes the target branch to the
sanctioned remote once the merge lands; the result's published field states
plainly whether a publish happened, true only when --push was given and a
real merge landed.

An explicit --message is checked against the repository's own configured
commit-msg hook before anything else runs: merge delegates to that hook
rather than judging the message itself, so a repository with no hook
configured sees no check at all, and a rejection there is reported as a
precondition, before the signing gate does any re-signing work.

Exit codes:
  0  success              the sources merged (with any re-signing reported)
  10 caveats              the merge landed; its signature did not verify, or a publish or cleanup did not complete
  20 gate_negative        every source was already in the target, so nothing landed
  30 precondition_unmet   a content guardrail flagged a file, the configured commit-msg hook rejected --message, or signing could not be satisfied; nothing was merged
  40 not_found            --repo is not a git working tree
  41 conflict             the merge would conflict; it was aborted
  50 usage                a flag value is not valid
  90 internal             an underlying git command failed unexpectedly

Exit 20 is an expected negative answer, not a failure: the merge asked to land
something and there was nothing to land. --dry-run is blind to this — it still
reports would_merge at exit 0 over an all-empty range, the accepted cost of a
preflight that does not detect the condition.

The worktree gate sanctions this verb only when git-tools is invoked by its
exact provisioned absolute path: a bare "git-tools", or a name resolved off
$PATH, does not satisfy it, and is denied from a primary checkout.`,
		Args:    cobra.MinimumNArgs(1),
		Example: "  git-tools merge --message \"merge release\" release/1.2",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := loadConfig(cmd.Flags())
			if err != nil {
				return finishErr(cmd, nil, "internal.config.load_failed", "load configuration", err)
			}
			repo, repoErr := requireRepo(cmd, cfg)
			if repo == nil {
				return repoErr
			}
			repoPath, pathErr := filepath.Abs(repo.Dir)
			if pathErr != nil {
				repoPath = repo.Dir
			}

			message, _ := cmd.Flags().GetString("message")
			ffMode, _ := cmd.Flags().GetString("fast-forward")
			dryRun, _ := cmd.Flags().GetBool("dry-run")
			cleanup, _ := cmd.Flags().GetBool("cleanup")
			push, _ := cmd.Flags().GetBool("push")

			ff, err := parseFastForward(ffMode)
			if err != nil {
				return finishUsage(cmd, map[string]any{dataKeyRepo: repoPath}, "usage.cli.invalid_fast_forward", err.Error())
			}

			// The target's own validity is a precondition, settled before the
			// signing gate and the merge, so a refusal here leaves every branch —
			// the one checked out and every named source — exactly where it was.
			target, err := gitexec.CurrentBranch(cmd.Context(), repo.Dir)
			if err != nil {
				return finishErr(cmd, map[string]any{dataKeyRepo: repoPath}, "internal.git.head_check_failed", "resolve the branch being merged into", err)
			}
			if target == "" {
				head, headErr := resolveCommit(cmd.Context(), repo.Dir, "HEAD")
				if headErr != nil {
					return finishErr(cmd, map[string]any{dataKeyRepo: repoPath, dataKeyTarget: target}, "internal.git.head_check_failed", "resolve HEAD", headErr)
				}
				return finishDiagnostic(cmd, map[string]any{dataKeyRepo: repoPath, dataKeyTarget: target}, clikit.NewPreconditionUnmet,
					"precondition_unmet.git.merge_target_detached_head",
					fmt.Sprintf("HEAD is detached at %s, not on a branch; merge needs a branch checked out to land sources into", head),
					clikit.Manual("check out a branch (e.g. `git switch -c <name>` or `git checkout <branch>`) and re-run; nothing was merged"),
					map[string]any{"head": head})
			}
			for _, source := range args {
				if source != target {
					continue
				}
				return finishDiagnostic(cmd, map[string]any{dataKeyRepo: repoPath, dataKeyTarget: target}, clikit.NewPreconditionUnmet,
					"precondition_unmet.git.merge_target_is_source",
					fmt.Sprintf("%s has %s checked out, and %s is also named as a source; a branch cannot be merged into itself", repoPath, target, source),
					clikit.Manual(fmt.Sprintf("check out a branch other than %s, or drop %s from the sources; nothing was merged", target, source)),
					map[string]any{"repo": repoPath, "target": target, "source": source})
			}

			// The resolved target itself is a precondition too, settled here
			// alongside the detached-head and self-target checks above and
			// before the signing gate touches anything: a linked worktree is
			// refused whether it got named by --repo or just inferred from the
			// process's own working directory, since landing a merge there is
			// the wrong operation, not just a risky one.
			linkedPath, primaryPath, wtErr := resolvedLinkedWorktree(cmd.Context(), repo.Dir)
			if wtErr != nil {
				return finishErr(cmd, map[string]any{dataKeyRepo: repoPath, dataKeyTarget: target}, "internal.git.worktree_check_failed", "check whether the resolved target is a linked worktree", wtErr)
			}
			if linkedPath != "" {
				return finishDiagnostic(cmd, map[string]any{dataKeyRepo: repoPath, dataKeyTarget: target}, clikit.NewPreconditionUnmet,
					"precondition_unmet.git.merge_target_is_linked_worktree",
					fmt.Sprintf("%s is a linked worktree, not the repository's primary checkout at %s; merge refuses to land there", linkedPath, primaryPath),
					clikit.Manual(fmt.Sprintf("run merge from the primary checkout at %s (or point --repo at it) instead of the linked worktree %s; nothing was merged", primaryPath, linkedPath)),
					map[string]any{"resolved_target": linkedPath, "primary_checkout": primaryPath})
			}

			// An explicit --message is the one commit message this verb
			// itself composes -- checked here, ahead of the signing gate and
			// the scan below, against whatever commit-msg hook the
			// repository already has configured, so a message a configured
			// hook would reject never pays for that gate's re-signing work
			// first. commitmsg.Check delegates to that hook rather than
			// judging the message itself, and is a no-op when no hook is
			// configured. An omitted --message leaves git to compose its own
			// default merge message, which the real "git merge" below still
			// runs past that same hook natively.
			if message != "" {
				refusal, err := commitmsg.Check(cmd.Context(), repo.Dir, message)
				if err != nil {
					return finishErr(cmd, map[string]any{dataKeyRepo: repoPath, dataKeyTarget: target}, "internal.git.commit_message_hook_failed", "run the configured commit-msg hook", err)
				}
				if refusal != nil {
					return finishDiagnostic(cmd, map[string]any{dataKeyRepo: repoPath, dataKeyTarget: target}, clikit.NewPreconditionUnmet, refusal.Code(), refusal.Message(), refusal.Advice(), nil)
				}
			}

			// The content-guardrail scan is the last read-only precondition
			// before anything mutates: it must run ahead of the signing gate
			// below, which can itself rewrite a source branch's history to
			// re-sign it, so a doomed merge never leaves that rewrite behind.
			if err := scanGate(cmd, cfg, repo.Dir, "merge", map[string]any{dataKeyRepo: repoPath, dataKeyTarget: target}); err != nil {
				return err
			}

			// One prober serves both the gate, which re-signs incoming ranges,
			// and the merge-commit check below, so a run probes signing at most
			// once.
			prober := signing.NewProber(repo)
			gated, refusal := signing.Gate(cmd.Context(), repo, target, args, dryRun, prober)
			if refusal != nil {
				return finishDiagnostic(cmd, map[string]any{dataKeyRepo: repoPath, dataKeyTarget: target}, clikit.NewPreconditionUnmet, refusal.Code(), refusal.Message(), refusal.Advice(), refusal.Context())
			}

			// A real merge whose every source range is empty lands nothing —
			// each source is already contained in the target — so it is an
			// expected negative (exit 20), not an empty success. This is settled
			// before the mint check below so an all-empty octopus reports the
			// negative rather than probing signing for a commit it will never
			// mint. A dry run is deliberately exempt: it cannot tell this case
			// from a would-merge and keeps reporting would_merge at exit 0.
			if !dryRun && allEmptyRange(gated) {
				diag, buildErr := clikit.NewError(
					"gate_negative.git.merge_all_sources_empty",
					fmt.Sprintf("every source (%s) is already contained in %s, so the merge would land nothing", strings.Join(args, " "), target),
					clikit.Manual("nothing was merged; there was nothing to land"),
					map[string]any{"sources": args, "target": target})
				if buildErr != nil {
					return finishErr(cmd, map[string]any{dataKeyRepo: repoPath, dataKeyTarget: target}, "internal.result.build_failed", "build diagnostic", buildErr)
				}
				result, buildErr := clikit.NewGateNegative(commandPath(cmd),
					map[string]any{dataKeyRepo: repoPath, dataKeyTarget: target, "dry_run": false, "signing_gate": gated},
					[]clikit.Diagnostic{diag}, nil)
				if buildErr != nil {
					return finishErr(cmd, map[string]any{dataKeyRepo: repoPath, dataKeyTarget: target}, "internal.result.build_failed", "build result", buildErr)
				}
				return finish(cmd, result)
			}

			// Decide before the merge whether it will mint a merge commit of its
			// own. That commit must be signed too, so its signability is a
			// precondition to settle up front — not something to stumble into
			// mid-merge, where a failing `git merge -S` looks like an internal
			// fault rather than an unresolved key. This runs after the gate so a
			// non-branch or unrelated source is caught by the gate's precondition
			// refusal rather than by a raw merge-base failure here; the gate's
			// re-signing preserves each source's fast-forward relation to the
			// target, so the minting decision is unaffected by its order.
			willMint, err := signing.WillMintCommit(cmd.Context(), repo, target, args, ff)
			if err != nil {
				return finishErr(cmd, map[string]any{dataKeyRepo: repoPath, dataKeyTarget: target}, "internal.git.merge_shape_check_failed", "determine whether the merge will mint a commit", err)
			}

			// A real merge that mints a commit signs it: prove git can sign
			// first, so a keyless repository refuses here (exit 30) rather than
			// letting `git merge -S` fail and surface as an internal error. A dry
			// run mints nothing, so it needs neither the proof nor the signature.
			sign := willMint && !dryRun
			if sign {
				available, detail, probeErr := prober.Available(cmd.Context())
				if probeErr != nil {
					return finishErr(cmd, map[string]any{dataKeyRepo: repoPath, dataKeyTarget: target}, "internal.git.signing_probe_failed", "test whether git can sign the merge commit", probeErr)
				}
				if !available {
					return finishDiagnostic(cmd, map[string]any{dataKeyRepo: repoPath, dataKeyTarget: target}, clikit.NewPreconditionUnmet,
						"precondition_unmet.git.signing_key_unresolved",
						fmt.Sprintf("no key resolved for commit signing, so the merge commit for %s would be unsigned: %s", strings.Join(args, " "), detail),
						clikit.Manual("configure a signing key (gpg.format plus user.signingkey, or this environment's signing setup) and re-run; nothing was merged"),
						map[string]any{"sources": args})
				}
			}

			// The target's pre-merge tip, captured immediately before the merge
			// runs (target is checked out and not detached, settled above), so
			// the count below reports exactly what this merge landed and
			// nothing an earlier step already moved.
			oldHead, err := resolveCommit(cmd.Context(), repo.Dir, "HEAD")
			if err != nil {
				return finishErr(cmd, map[string]any{dataKeyRepo: repoPath, dataKeyTarget: target}, "internal.git.head_check_failed", "resolve HEAD before merging", err)
			}

			result, err := repo.Merge(cmd.Context(), args, git.MergeOptions{Message: message, FastForward: ff, DryRun: dryRun, Sign: sign})
			if err != nil {
				conflictData := map[string]any{dataKeyRepo: repoPath, dataKeyTarget: target}
				if rewritten := rewrittenSources(gated); len(rewritten) > 0 {
					conflictData["rewritten"] = rewritten
				}
				return handleGitError(cmd, conflictData, err, "internal.git.merge_failed", fmt.Sprintf("merge %s", strings.Join(args, " ")))
			}

			data := map[string]any{dataKeyRepo: repoPath, dataKeyTarget: target, "dry_run": result.DryRun, "signing_gate": gated}
			if result.DryRun {
				data["would_merge"] = result.WouldMerge
			} else {
				data["new_head"] = result.NewHead
				commits, countErr := commitsLanded(cmd.Context(), repo.Dir, oldHead, result.NewHead)
				if countErr != nil {
					return finishErr(cmd, data, "internal.git.commit_count_failed", "count the commits this merge landed", countErr)
				}
				data["commits_landed"] = commits
			}

			// FB24: whether this invocation also published the target is
			// reported plainly rather than left for a caller to infer from
			// other fields — false unless --push was given and a real merge
			// landed. Set before the checks below so every exit from here on,
			// caveats included, carries the field rather than leaving a caller
			// to read its absence as either answer.
			data["published"] = false

			// The gate above re-signs every incoming range, and the probe before
			// the merge proves signing is possible before -S is passed — but
			// neither confirms the minted commit's own signature actually
			// verifies once it exists. A merge that mints a signed commit checks
			// that here, rather than trusting `git merge -S`'s exit code alone: a
			// signature that fails to verify is reported as a caveat naming the
			// merge unsigned, never silently as success, and nothing here
			// unwinds the merge that already landed.
			if sign {
				state, verifyErr := headSigState(cmd.Context(), repo.Dir, result.NewHead)
				if verifyErr != nil {
					return finishErr(cmd, data, "internal.git.merge_commit_verify_failed", "verify the merge commit's signature", verifyErr)
				}
				if state != "G" && state != "U" {
					return finishCaveat(cmd, data, "caveats.git.merge_commit_unsigned",
						fmt.Sprintf("the merge landed at %s, but its signature does not verify (state %q); treat the merge as unsigned", result.NewHead, state),
						clikit.Manual("investigate the signing setup, then re-sign or redo the merge; the merge that already landed is not unwound"),
						map[string]any{"new_head": result.NewHead, "sig_state": state})
				}
			}

			// A publish runs only on a real merge whose tip verified above: an
			// unsigned tip returns before this point, so nothing here can
			// publish a merge the check refused to call signed.
			if push && !result.DryRun {
				if pubErr := publishTarget(cmd.Context(), cfg, repo.Dir, target); pubErr != nil {
					return finishCaveat(cmd, data, "caveats.git.merge_publish_failed",
						fmt.Sprintf("the merge landed, but publishing %s to %s failed: %s", target, cfg.Remote, sanitizeMessage(pubErr.Error())),
						clikit.Manual(fmt.Sprintf("push %s to %s manually", target, cfg.Remote)),
						map[string]any{"target": target, "remote": cfg.Remote})
				}
				data["published"] = true
			}

			// Cleanup runs only on a real, successful merge, and only when opted
			// in. It shares one rule set with the standalone `worktree remove`
			// verb, so the no-work-loss guard cannot diverge between the paths. A
			// cleanup that refuses or fails never unwinds the merge that already
			// landed: it is reported as a caveat on the success.
			if cleanup && !result.DryRun {
				cleaned, unremoved := cleanupMergedWorktrees(cmd.Context(), repo, args, cfg.Remote)
				if len(cleaned) > 0 {
					data["cleaned_worktrees"] = cleaned
				}
				if len(unremoved) > 0 {
					data["unremoved_worktrees"] = unremoved
					reason, _ := unremoved[0]["reason"].(string)
					return finishCaveat(cmd, data, "caveats.git.worktree_cleanup_incomplete",
						fmt.Sprintf("the merge landed, but %d worktree(s) were not removed: %s", len(unremoved), reason),
						clikit.Manual("remove the named worktree(s) manually"),
						map[string]any{"unremoved": len(unremoved)})
				}
			}

			clikitResult, buildErr := clikitSuccess(cmd, data)
			if buildErr != nil {
				return finishErr(cmd, data, "internal.result.build_failed", "build result", buildErr)
			}
			return finish(cmd, clikitResult)
		},
	}
	cmd.Flags().String("message", "", "commit message for a real (non-fast-forward) merge")
	cmd.Flags().String("fast-forward", "allow", "fast-forward behavior: allow, never, or only")
	cmd.Flags().Bool("dry-run", false, "merge into the index and report clean-mergeability, always aborting afterward")
	cmd.Flags().Bool("cleanup", false, "after a successful merge, remove each merged branch's worktree once its work has safely landed")
	cmd.Flags().Bool("push", false, "after a successful merge, publish the target branch to the sanctioned remote")
	return cmd
}

// allEmptyRange reports whether every gated source's range was empty — each
// source already contained in the target, so the merge would land nothing. An
// empty gated slice is not all-empty: there was no source to gate.
func allEmptyRange(gated []map[string]any) bool {
	if len(gated) == 0 {
		return false
	}
	for _, record := range gated {
		if record["action"] != signing.ActionEmptyRange {
			return false
		}
	}
	return true
}

// rewrittenSources extracts, from the gate's per-source report, the entries
// the gate actually re-signed — in the same vocabulary (source, old_head,
// new_head, backup_ref) signing.Refusal's own rewritten context uses, so an
// operator sees one shape whether the gate stopped the merge or the merge
// itself aborted afterward. It returns nil when nothing was rewritten, so a
// caller with no rewrite to report omits the key rather than emitting an
// empty list.
func rewrittenSources(gated []map[string]any) []map[string]any {
	var rewritten []map[string]any
	for _, record := range gated {
		if record["action"] != signing.ActionResigned {
			continue
		}
		rewritten = append(rewritten, map[string]any{
			"source":     record["source"],
			"old_head":   record["old_head"],
			"new_head":   record["new_head"],
			"backup_ref": record["backup_ref"],
		})
	}
	return rewritten
}

// cleanupMergedWorktrees removes the worktree of each just-merged branch,
// through the shared worktreeclean.Cleanup rule set. It never returns an error: a
// merge has already landed, so a refusal or an infrastructure failure is folded
// into the unremoved list for the caller to report as a caveat rather than
// unwound. cleaned holds the paths removed; each unremoved entry names a path
// (when known) and the reason it stayed.
func cleanupMergedWorktrees(ctx context.Context, repo *git.Repo, mergedBranches []string, remote string) (cleaned []string, unremoved []map[string]any) {
	list, err := repo.WorktreeList(ctx)
	if err != nil {
		return nil, []map[string]any{{"reason": sanitizeMessage(fmt.Sprintf("list worktrees: %v", err))}}
	}
	for _, wt := range list {
		if wt.Branch == "" || !worktreeclean.BranchAmong(wt.Branch, mergedBranches) {
			continue
		}
		out, err := worktreeclean.Cleanup(ctx, repo, wt.Path, worktreeclean.Options{
			MergedBranches: mergedBranches,
			Remote:         remote,
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

// resolvedLinkedWorktree reports whether dir's checkout is a linked worktree
// rather than a repository's primary one. It reads git's own worktree
// bookkeeping -- a linked worktree's top-level ".git" is a file pointing back
// at the shared administrative area, where the primary checkout's is a
// directory -- rather than inferring the answer from where the path sits.
// linkedPath and primaryPath are both empty (with a nil error) when dir
// resolves to the primary checkout.
//
// This refusal is the condition SC-A6's corrected remedy text must never
// advise as the fix for the condition it addresses: telling an operator
// stuck at a linked worktree to "just merge from here" would just trade one
// refused target for another. That coupling has to survive in this comment
// so the L2 remedy task cannot lose track of it.
func resolvedLinkedWorktree(ctx context.Context, dir string) (linkedPath, primaryPath string, err error) {
	top, err := gitPathOutput(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", "", err
	}

	info, statErr := os.Lstat(filepath.Join(top, ".git"))
	if statErr != nil {
		return "", "", fmt.Errorf("stat %s: %w", filepath.Join(top, ".git"), statErr)
	}
	if info.IsDir() {
		return "", "", nil
	}

	common, err := gitPathOutput(ctx, dir, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", "", err
	}
	return top, filepath.Dir(common), nil
}

// gitPathOutput runs a git rev-parse subcommand that prints one path and
// returns it trimmed, surfacing a non-zero exit as *git.CommandError.
func gitPathOutput(ctx context.Context, dir string, args ...string) (string, error) {
	res, err := gitexec.RunGit(ctx, dir, args...)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", &git.CommandError{Args: args, ExitCode: res.ExitCode, Stderr: strings.TrimSpace(string(res.Stderr))}
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}

// resolveCommit resolves ref (e.g. "HEAD") to the object id it currently
// names in dir.
func resolveCommit(ctx context.Context, dir, ref string) (string, error) {
	res, err := gitexec.RunGit(ctx, dir, "rev-parse", "--verify", ref)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", &git.CommandError{Args: []string{"rev-parse", "--verify", ref}, ExitCode: res.ExitCode, Stderr: strings.TrimSpace(string(res.Stderr))}
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}

// commitsLanded reports the count of commits reachable from newHead but not
// from oldHead — the commits this merge actually landed, whether carried in
// verbatim (a fast-forward) or newly minted (the merge commit itself, on an
// octopus or a forced merge commit). The caller counts once here rather than
// leaving every caller of the result to recompute it from new_head/old ref
// state it may no longer have.
func commitsLanded(ctx context.Context, dir, oldHead, newHead string) (int, error) {
	args := []string{"rev-list", "--count", oldHead + ".." + newHead}
	res, err := gitexec.RunGit(ctx, dir, args...)
	if err != nil {
		return 0, err
	}
	if res.ExitCode != 0 {
		return 0, &git.CommandError{Args: args, ExitCode: res.ExitCode, Stderr: strings.TrimSpace(string(res.Stderr))}
	}
	n, convErr := strconv.Atoi(strings.TrimSpace(string(res.Stdout)))
	if convErr != nil {
		return 0, fmt.Errorf("parse rev-list count for %s..%s: %w", oldHead, newHead, convErr)
	}
	return n, nil
}

// headSigState reports git's own signature-status code (%G?) for a single
// commit — the same vocabulary the signing gate reads over a range, read
// here for just the minted merge commit.
func headSigState(ctx context.Context, dir, ref string) (string, error) {
	args := []string{"log", "-1", "--format=%G?", ref}
	res, err := gitexec.RunGit(ctx, dir, args...)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", &git.CommandError{Args: args, ExitCode: res.ExitCode, Stderr: strings.TrimSpace(string(res.Stderr))}
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}

// publishTarget pushes target's current tip to cfg.Remote once a real merge
// has landed, comparing local and remote first so a remote already at
// target's tip is treated as published without an unnecessary push.
func publishTarget(ctx context.Context, cfg *Config, dir, target string) error {
	fullRef := "refs/heads/" + target
	localSHA, err := localRefSHA(ctx, dir, fullRef)
	if err != nil {
		return fmt.Errorf("resolve %s: %w", fullRef, err)
	}
	remoteSHA, hadRemote, err := remoteRefSHA(ctx, dir, cfg.Remote, fullRef)
	if err != nil {
		return fmt.Errorf("query %s on %s: %w", fullRef, cfg.Remote, err)
	}
	if hadRemote && remoteSHA == localSHA {
		return nil
	}
	args := []string{"push", cfg.Remote, fullRef + ":" + fullRef}
	res, err := gitexec.RunGit(ctx, dir, args...)
	if err != nil {
		return fmt.Errorf("push %s to %s: %w", fullRef, cfg.Remote, err)
	}
	if res.ExitCode != 0 {
		return &git.CommandError{Args: args, ExitCode: res.ExitCode, Stderr: strings.TrimSpace(string(res.Stderr))}
	}
	return nil
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
