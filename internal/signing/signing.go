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
// here with their backup refs rather than unwound.
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
// Gate itself signs only the incoming commits each source carries. The merge
// commit is covered too, by the caller: WillMintCommit settles whether the
// merge will mint one, and the shared Prober proves git can sign it before
// the merge runs, so the caller asks for that signature explicitly instead of
// depending on commit.gpgsign. Together, Gate and the caller's use of
// WillMintCommit and Prober mean nothing a merge lands — incoming or
// minted — goes out unsigned.
//
// dryRun mirrors the merge's own --dry-run: the gate reports the rewrite it
// would apply and moves no ref, so a dry-run merge stays free of side effects.
//
// Re-signing a branch that is checked out in a linked worktree does not
// disturb it: Resign preserves each commit's tree object exactly, so that
// worktree's files and index still match the branch's new tip.
//
// The caller supplies the prober so the gate's availability check shares one
// probe with the caller's own — a run probes signing at most once.
//
// It returns the per-source signing_gate report, or a *Refusal that stops the
// merge. Refusal messages are raw here; the caller sanitizes them when it emits
// them.
func Gate(ctx context.Context, repo *git.Repo, target string, sources []string, dryRun bool, prober *Prober) ([]map[string]any, *Refusal) {
	var (
		gated     []map[string]any
		rewritten []map[string]any
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

		available, detail, err := prober.Available(ctx)
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
				clikit.Manual("nothing was merged; recover any listed rewrite from its backup ref if the rewrite is unwanted, then re-run"))
		}
		record["action"] = ActionResigned
		record["new_head"] = applied.NewHead
		record["backup_ref"] = applied.BackupRef
		gated = append(gated, record)
		rewritten = append(rewritten, map[string]any{"source": source, "old_head": applied.OldHead, "new_head": applied.NewHead, "backup_ref": applied.BackupRef})
	}
	return gated, nil
}

// WillMintCommit reports whether merging sources into target will mint a commit
// of its own — the merge commit, which git signs only when asked. It mints one
// whenever fast-forward is forbidden, whenever there are two or more sources
// (an octopus always commits), or whenever the single source is not a
// fast-forward of target (target is not already its ancestor). A single source
// target already contains fast-forwards instead and mints nothing.
//
// It reads refs as they stand and is meant to run before the merge, so the
// caller can require and pass a signing key exactly when a commit will be
// minted, and never issue `git merge -S` in a repository that cannot sign.
func WillMintCommit(ctx context.Context, repo *git.Repo, target string, sources []string, ff git.FastForward) (bool, error) {
	if ff == git.FastForwardNever || len(sources) >= 2 {
		return true, nil
	}
	if len(sources) == 0 {
		return false, nil
	}
	ancestor, err := isAncestor(ctx, repo.Dir, target, sources[0])
	if err != nil {
		return false, err
	}
	return !ancestor, nil
}

// Prober answers "can git actually sign a commit in this repository?" and
// memoizes the answer so a single merge run probes at most once. The probe
// signs a throwaway commit object — the definitive test — and commit-tree
// always writes, so it leaves one unreferenced commit object behind for a
// future git gc; memoizing keeps that at most one such object per repository
// per run no matter how many callers ask. The gate (before it re-signs a
// range) and the merge verb (before it mints a signed merge commit) share one
// Prober, so their two needs never cost a second probe.
type Prober struct {
	dir       string
	probed    bool
	available bool
	detail    string
	err       error
}

// NewProber returns a Prober for repo that has not probed yet.
func NewProber(repo *git.Repo) *Prober { return &Prober{dir: repo.Dir} }

// Available reports whether git can produce a signature in the repository,
// running the underlying probe on the first call and returning the cached
// answer on every later one. detail carries git's own reason when signing is
// unavailable; err is set only when the probe could not be run at all.
func (p *Prober) Available(ctx context.Context) (available bool, detail string, err error) {
	if !p.probed {
		p.available, p.detail, p.err = probeSigning(ctx, p.dir)
		p.probed = true
	}
	return p.available, p.detail, p.err
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

// isAncestor reports whether commit a is an ancestor of commit b in dir, via
// `git merge-base --is-ancestor`: exit 0 is yes, exit 1 is no, anything else a
// real failure. A yes means a merge of b into a can fast-forward.
func isAncestor(ctx context.Context, dir, a, b string) (bool, error) {
	args := []string{"merge-base", "--is-ancestor", a, b}
	res, err := gitexec.RunGit(ctx, dir, args...)
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

// probeSigning reports whether git can actually produce a signature in dir, by
// signing a throwaway commit object with the same machinery the rewrite uses —
// a definitive answer, where reading configuration would only guess whether a
// named key resolves. commit-tree always writes, so each probe leaves one
// unreferenced commit object behind for a future git gc; Prober memoizes this
// so a single run probes at most once, leaving at most one such object. detail
// carries git's own reason when signing is unavailable.
func probeSigning(ctx context.Context, dir string) (available bool, detail string, err error) {
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
