// Package signing is the merge landing channel's signing gate: given the
// branch a merge lands onto and the source branches it would land, it re-signs
// — or refuses — every source's incoming commits before the merge itself runs,
// so nothing ever lands unsigned. Gate is the entry point.
//
// Inputs: an open git.Repo, the branch being merged into, the source branches,
// and whether the merge is a dry run. Outputs: a per-source signing_gate
// report (each entry naming the source and the Action taken), or a *Refusal
// that stops the merge.
//
// Invariants:
//   - The gate never moves a ref on a dry run, and never lands a source it
//     could not sign.
//   - Sources are gated independently and in order; a late refusal reports the
//     rewrites that already landed on earlier sources rather than unwinding
//     them.
//   - Refusal messages are raw (unsanitized) at this package boundary; the
//     caller that emits a Refusal as a diagnostic sanitizes it there, exactly
//     once.
//   - This package depends only on gitexec and the shared git/clikit
//     libraries — never on CLI or worktree-cleanup code — so both a clikit
//     consumer and a plain-error consumer can call it.
package signing

import (
	"context"
	"fmt"
	"strings"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
	"github.com/johnrichter/claude-shared-tooling/go/git"

	"github.com/johnrichter/git-tools/internal/gitexec"
)

// Action names what the gate did to one merge source, as reported in the
// signing_gate record's "action" field.
const (
	ActionResigned      = "resigned"       // the range was rewritten into signed equivalents
	ActionWouldResign   = "would_resign"   // a dry-run merge: the rewrite was computed, not applied
	ActionAlreadySigned = "already_signed" // every commit in range already verifies
	ActionEmptyRange    = "empty_range"    // the source is already contained in the target
)

// Refusal is the signing gate's decision that a merge must not proceed. It
// implements error so a plain-error caller can return it directly, and it also
// carries the fields a clikit caller needs to emit it as a diagnostic: a code,
// a message, triage advice and a context map. The rewritten list names the
// sources already re-signed before this refusal — an octopus merge gates its
// sources in turn, so a late refusal can follow earlier rewrites, reported
// here with their backup tags rather than unwound.
type Refusal struct {
	code      string
	message   string
	advice    clikit.Triage
	source    string
	rewritten []map[string]any
}

// Error returns the refusal's raw, unsanitized message, satisfying the error
// interface so a Refusal can be returned wherever a plain error is expected.
func (r *Refusal) Error() string { return r.message }

// Code returns the refusal's diagnostic code (e.g.
// "precondition_unmet.git.merge_source_not_branch").
func (r *Refusal) Code() string { return r.code }

// Message returns the refusal's raw, unsanitized human-readable message. The
// caller that emits it as a diagnostic is responsible for sanitizing it.
func (r *Refusal) Message() string { return r.message }

// Advice returns the triage guidance for recovering from the refusal.
func (r *Refusal) Advice() clikit.Triage { return r.advice }

// Context returns the refusal's diagnostic context: the offending source
// always, and the list of sources already rewritten before the refusal only
// when that list is non-empty.
func (r *Refusal) Context() map[string]any {
	ctx := map[string]any{"source": r.source}
	if len(r.rewritten) > 0 {
		ctx["rewritten"] = r.rewritten
	}
	return ctx
}

// Gate re-signs, or refuses, every source of a merge before the merge itself
// runs. Each source is gated independently and in order: its fork point with
// target is computed here rather than supplied, an empty or already-verifying
// range is skipped, and the rewrite is computed as a dry run before it is
// applied. A source that carries unsigned commits and cannot be signed is
// refused — the merge verb never lands unsigned incoming commits.
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
//
// It returns the per-source signing_gate report, or a *Refusal that stops the
// merge. Refusal messages are raw here; the caller sanitizes them when it emits
// them.
func Gate(ctx context.Context, repo *git.Repo, target string, sources []string, dryRun bool) ([]map[string]any, *Refusal) {
	var (
		gated     []map[string]any
		rewritten []map[string]any
		signable  bool // whether signing has already been proven available here
	)
	refuse := func(source, code, message string, advice clikit.Triage) *Refusal {
		return &Refusal{code: code, message: message, advice: advice, source: source, rewritten: rewritten}
	}

	for _, source := range sources {
		ref := "refs/heads/" + source
		record := map[string]any{"source": source}

		isBranch, err := gitexec.RefExists(ctx, repo.Dir, "heads", source)
		if err != nil {
			return nil, refuse(source, "precondition_unmet.git.signing_gate_failed",
				fmt.Sprintf("could not check whether %s is a local branch: %v", source, err),
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
				fmt.Sprintf("could not compute the fork point of %s with %s: %v", source, target, err),
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
				fmt.Sprintf("could not read signature status over %s..%s: %v", base, source, err),
				clikit.Manual("nothing was merged; resolve the underlying git failure and re-run"))
		}
		record["commits"] = len(codes)

		// An empty range is a skip, not a failure: the source is already
		// contained in the target, and Resign rejects an empty range outright.
		if len(codes) == 0 {
			record["action"] = ActionEmptyRange
			gated = append(gated, record)
			continue
		}
		if allVerify(codes) {
			record["action"] = ActionAlreadySigned
			gated = append(gated, record)
			continue
		}

		if !signable {
			available, detail, err := signingAvailable(ctx, repo.Dir)
			if err != nil {
				return nil, refuse(source, "precondition_unmet.git.signing_gate_failed",
					fmt.Sprintf("could not test whether git can sign in %s: %v", repo.Dir, err),
					clikit.Manual("nothing was merged; resolve the underlying git failure and re-run"))
			}
			if !available {
				return nil, refuse(source, "precondition_unmet.git.signing_key_unresolved",
					fmt.Sprintf("no key resolved for commit signing, so merging %s would land unsigned commits: %s", source, detail),
					clikit.Manual("configure a signing key (gpg.format plus user.signingkey, or this environment's signing setup) and re-run; nothing was merged"))
			}
			signable = true
		}

		plan, err := repo.Resign(ctx, ref, git.ResignOptions{Base: base, DryRun: true})
		if err != nil {
			return nil, refuse(source, "precondition_unmet.git.signing_gate_failed",
				fmt.Sprintf("re-signing %s..%s could not be computed: %v", base, source, err),
				clikit.Manual("nothing was merged; resolve the underlying git failure and re-run"))
		}
		record["old_head"] = plan.OldHead
		record["new_head"] = plan.NewHead
		if dryRun {
			record["action"] = ActionWouldResign
			gated = append(gated, record)
			continue
		}

		applied, err := repo.Resign(ctx, ref, git.ResignOptions{Base: base})
		if err != nil {
			return nil, refuse(source, "precondition_unmet.git.signing_gate_failed",
				fmt.Sprintf("re-signing %s..%s failed: %v", base, source, err),
				clikit.Manual("nothing was merged; recover any listed rewrite from its backup tag if the rewrite is unwanted, then re-run"))
		}
		record["action"] = ActionResigned
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
	res, err := gitexec.RunGit(ctx, dir, args...)
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
	res, err := gitexec.RunGit(ctx, dir, args...)
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
	tree, err := gitexec.RunGit(ctx, dir, "rev-parse", "--verify", "HEAD^{tree}")
	if err != nil {
		return false, "", err
	}
	if tree.ExitCode != 0 {
		return false, "", &git.CommandError{Args: []string{"rev-parse", "HEAD^{tree}"}, ExitCode: tree.ExitCode, Stderr: strings.TrimSpace(string(tree.Stderr))}
	}
	probe, err := gitexec.RunGit(ctx, dir, "commit-tree", "-S", "-m", "git-tools signing probe", strings.TrimSpace(string(tree.Stdout)))
	if err != nil {
		return false, "", err
	}
	if probe.ExitCode != 0 {
		return false, strings.TrimSpace(string(probe.Stderr)), nil
	}
	return true, "", nil
}
