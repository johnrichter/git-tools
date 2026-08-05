package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
	"github.com/johnrichter/claude-shared-tooling/go/git"
	"github.com/johnrichter/claude-shared-tooling/go/sysops"
)

// newPushCmd builds the "push" verb — the sanctioned replacement for a raw
// `git push`, which the worktree gate denies outright. See its Long help
// text for the full refusal/exit-code contract; the short version: every
// check here reads state at invocation time (tree, flags, refs), never
// commit history, because history alone cannot tell a fast-forward merge
// landing apart from a commit authored directly on the branch.
func newPushCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "push <ref>",
		Short: "Publish the currently checked-out branch, or a tag, to the sanctioned remote",
		Long: `push is the sanctioned channel for advancing a protected branch or a
tag: the worktree gate refuses a raw "git push" outright, and this verb is
what it expects in its place.

It refuses anything it cannot verify as sanctioned at the moment it runs:
tracked modifications or staged changes, --repo/--config (either would
retarget the push away from the invoking process's own working directory,
which push always operates on and never lets a flag change), or <ref>
naming a branch other than the one currently checked out. A tag push is
exempt from that last check.

It never inspects commit history to tell a commit that landed via
"git-tools merge" apart from one authored directly on the branch — a commit
object carries no such provenance, and a fast-forward merge leaves no trace
distinguishing the two. Every check here is a snapshot of state at
invocation time, never a walk of history.

Exit codes:
  0  success              ref advanced (or already matched) on the remote
  10 caveats              remote already has ref at this commit; no-op
  30 precondition_unmet   working tree has tracked or staged changes
  40 not_found            ref is neither a local branch nor a local tag
  41 conflict             ref names a branch, but HEAD is not on it
  50 usage                --repo or --config was passed
  60 transient            remote rejected the push; re-run to retry
  90 internal             an underlying git command failed unexpectedly`,
		Args: cobra.ExactArgs(1),
		Example: strings.TrimLeft(`
  git-tools push main
  git-tools push v1.4.0
  git-tools push main --dry-run
`, "\n"),
		RunE: func(cmd *cobra.Command, args []string) error {
			ref := args[0]

			// --repo/--config retarget every other verb at a different
			// working directory or settings file. push refuses both
			// unconditionally rather than trying to sanction some values
			// and not others.
			if cmd.Flags().Changed("repo") || cmd.Flags().Changed("config") {
				return finishUsage(cmd, "usage.cli.push_retargeting_flag",
					"push always operates on the invoking process's own working directory; --repo/--config are refused")
			}

			cfg, err := loadConfig(cmd.Flags())
			if err != nil {
				return finishErr(cmd, "internal.config.load_failed", "load configuration", err)
			}
			if err := openHere(cmd); err != nil {
				return err
			}

			ctx := cmd.Context()

			dirty, err := treeDirty(ctx, ".")
			if err != nil {
				return finishErr(cmd, "internal.git.diff_check_failed", "check working tree state", err)
			}
			if dirty {
				return finishDiagnostic(cmd, clikit.NewPreconditionUnmet, "precondition_unmet.git.dirty_tree",
					"the working tree has tracked modifications or staged changes relative to HEAD",
					clikit.Manual("commit or stash the pending changes, then re-run"), nil)
			}

			isBranch, err := refExists(ctx, ".", "heads", ref)
			if err != nil {
				return finishErr(cmd, "internal.git.show_ref_failed", fmt.Sprintf("check whether %s is a local branch", ref), err)
			}
			refKind := "branch"
			if !isBranch {
				isTag, err := refExists(ctx, ".", "tags", ref)
				if err != nil {
					return finishErr(cmd, "internal.git.show_ref_failed", fmt.Sprintf("check whether %s is a local tag", ref), err)
				}
				if !isTag {
					return finishDiagnostic(cmd, clikit.NewNotFound, "not_found.git.ref_not_found",
						fmt.Sprintf("%q is neither a local branch nor a local tag", ref),
						clikit.Manual(fmt.Sprintf("create or check out %q locally, then re-run", ref)),
						map[string]any{"ref": ref})
				}
				refKind = "tag"
			}
			fullRef := "refs/heads/" + ref
			if refKind == "tag" {
				fullRef = "refs/tags/" + ref
			}

			if refKind == "branch" {
				head, err := currentBranch(ctx, ".")
				if err != nil {
					return finishErr(cmd, "internal.git.head_check_failed", "resolve the current branch", err)
				}
				if head != ref {
					reported := head
					if reported == "" {
						reported = "detached HEAD"
					}
					return finishDiagnostic(cmd, clikit.NewConflict, "conflict.git.head_mismatch",
						fmt.Sprintf("HEAD is on %s, not %s; push only advances the branch currently checked out", reported, ref),
						clikit.RunTool("git", "checkout", ref),
						map[string]any{"ref": ref, "head": reported})
				}
			}

			localSHA, err := localRefSHA(ctx, ".", fullRef)
			if err != nil {
				return finishErr(cmd, "internal.git.resolve_ref_failed", fmt.Sprintf("resolve %s", fullRef), err)
			}
			remoteSHA, hadRemote, err := remoteRefSHA(ctx, ".", cfg.Remote, fullRef)
			if err != nil {
				return finishErr(cmd, "internal.git.query_remote_failed", fmt.Sprintf("query %s on %s", fullRef, cfg.Remote), err)
			}

			dryRun, _ := cmd.Flags().GetBool("dry-run")
			data := map[string]any{"ref": ref, "kind": refKind, "remote": cfg.Remote, "dry_run": dryRun}

			if hadRemote && remoteSHA == localSHA {
				data["head"] = localSHA
				return finishCaveat(cmd, data, "caveats.git.push_already_current",
					fmt.Sprintf("%s already matches %s at %s; nothing to push", fullRef, cfg.Remote, localSHA),
					clikit.Manual("no action needed"), map[string]any{"ref": ref})
			}

			refspec := fullRef + ":" + fullRef
			if dryRun {
				data["would_push"] = refspec
				clikitResult, buildErr := clikitSuccess(cmd, data)
				if buildErr != nil {
					return finishErr(cmd, "internal.result.build_failed", "build result", buildErr)
				}
				return finish(cmd, clikitResult)
			}

			res, err := runGit(ctx, ".", "push", cfg.Remote, refspec)
			if err != nil {
				return finishErr(cmd, "internal.git.push_failed", fmt.Sprintf("push %s to %s", fullRef, cfg.Remote), err)
			}
			if res.ExitCode != 0 {
				return finishDiagnostic(cmd, clikit.NewTransient, "transient.git.push_rejected",
					strings.TrimSpace(string(res.Stderr)),
					clikit.Reinvoke("git-tools", "push", ref),
					map[string]any{"ref": ref, "remote": cfg.Remote})
			}

			if hadRemote {
				data["old_head"] = remoteSHA
			}
			data["new_head"] = localSHA
			clikitResult, buildErr := clikitSuccess(cmd, data)
			if buildErr != nil {
				return finishErr(cmd, "internal.result.build_failed", "build result", buildErr)
			}
			return finish(cmd, clikitResult)
		},
	}
	cmd.Flags().Bool("dry-run", false, "report what would be pushed without pushing anything")
	return cmd
}

// openHere confirms "." is a git working tree, or finishes cmd with a
// not_found result and returns the accompanying error. push never opens
// cfg.Repo — see newPushCmd's Long text for why --repo can't retarget it.
func openHere(cmd *cobra.Command) error {
	if _, err := git.Open(cmd.Context(), "."); err != nil {
		return finishDiagnostic(cmd, clikit.NewNotFound, "not_found.git.repo_not_found",
			err.Error(),
			clikit.Manual("run git-tools push from inside a git working tree"), nil)
	}
	return nil
}

// runGit runs a git subcommand rooted at dir and returns its result for the
// caller to interpret. Only a spawn failure becomes a Go error here — a
// non-zero exit is git's ordinary way of answering a yes/no or reporting a
// real failure, and each caller knows which exit codes mean what for the
// subcommand it ran.
func runGit(ctx context.Context, dir string, args ...string) (*sysops.Result, error) {
	res, err := sysops.Run(ctx, "git", args, sysops.Options{Dir: dir})
	if err != nil {
		return nil, fmt.Errorf("exec git %s: %w", strings.Join(args, " "), err)
	}
	return res, nil
}

// treeDirty reports whether the working tree has tracked modifications or
// staged changes relative to HEAD. `git diff` never considers untracked or
// ignored paths, so neither counts as dirtiness here.
func treeDirty(ctx context.Context, dir string) (bool, error) {
	args := []string{"diff", "--quiet", "HEAD"}
	res, err := runGit(ctx, dir, args...)
	if err != nil {
		return false, err
	}
	switch res.ExitCode {
	case 0:
		return false, nil
	case 1:
		return true, nil
	default:
		return false, &git.CommandError{Args: args, ExitCode: res.ExitCode, Stderr: strings.TrimSpace(string(res.Stderr))}
	}
}

// currentBranch returns the short name of the branch HEAD is on, or "" for
// a detached HEAD.
func currentBranch(ctx context.Context, dir string) (string, error) {
	res, err := runGit(ctx, dir, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", nil
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}

// refExists reports whether refs/<namespace>/<ref> (namespace "heads" or
// "tags") exists locally.
func refExists(ctx context.Context, dir, namespace, ref string) (bool, error) {
	args := []string{"show-ref", "--verify", "--quiet", "refs/" + namespace + "/" + ref}
	res, err := runGit(ctx, dir, args...)
	if err != nil {
		return false, err
	}
	switch res.ExitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, &git.CommandError{Args: args, ExitCode: res.ExitCode, Stderr: strings.TrimSpace(string(res.Stderr))}
	}
}

// localRefSHA resolves fullRef (e.g. "refs/heads/main") to the object id it
// currently names, unpeeled — an annotated tag resolves to its tag object,
// matching what remoteRefSHA reports for the same ref.
func localRefSHA(ctx context.Context, dir, fullRef string) (string, error) {
	args := []string{"rev-parse", "--verify", fullRef}
	res, err := runGit(ctx, dir, args...)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", &git.CommandError{Args: args, ExitCode: res.ExitCode, Stderr: strings.TrimSpace(string(res.Stderr))}
	}
	return strings.TrimSpace(string(res.Stdout)), nil
}

// remoteRefSHA looks up fullRef on remote without fetching, reporting ok as
// false when the remote has no such ref (distinct from a lookup failure).
func remoteRefSHA(ctx context.Context, dir, remote, fullRef string) (sha string, ok bool, err error) {
	args := []string{"ls-remote", "--exit-code", remote, fullRef}
	res, runErr := runGit(ctx, dir, args...)
	if runErr != nil {
		return "", false, runErr
	}
	switch res.ExitCode {
	case 0:
		fields := strings.Fields(string(res.Stdout))
		if len(fields) == 0 {
			return "", false, fmt.Errorf("git ls-remote %s %s: no output", remote, fullRef)
		}
		return fields[0], true, nil
	case 2:
		return "", false, nil
	default:
		return "", false, &git.CommandError{Args: args, ExitCode: res.ExitCode, Stderr: strings.TrimSpace(string(res.Stderr))}
	}
}
