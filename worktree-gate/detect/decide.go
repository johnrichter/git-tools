package detect

import (
	"fmt"
	"path/filepath"
	"strings"
)

const (
	// ProjectDirEnvVar names the project root the tracking-doc exemption
	// checks a Write/Edit target against (see decideFileWrite).
	ProjectDirEnvVar = "CLAUDE_PROJECT_DIR"
	// MergeGateEnvVar opts a Bash call into the sanctioned-landing-merge
	// override (see decideBash). Only the exact value "1" opts in.
	MergeGateEnvVar = "DAT_MERGE_GATE"
)

// Input is the subset of a PreToolUse payload the gate needs, plus the two
// environment signals its allow-list overrides read.
type Input struct {
	ToolName string // "Write", "Edit", or "Bash"
	CWD      string // session working directory, used for Bash
	FilePath string // Write/Edit target
	Command  string // Bash command

	// ProjectDir is CLAUDE_PROJECT_DIR, empty when unset. Feeds the
	// tracking-doc exemption in decideFileWrite.
	ProjectDir string
	// MergeGateEnabled is true iff DAT_MERGE_GATE is set to exactly "1".
	// Feeds the sanctioned-landing-merge override in decideBash.
	MergeGateEnabled bool
}

// Decision is the gate's verdict for one tool call.
type Decision struct {
	// Deny blocks the call. Reason is the operator-facing explanation.
	Deny   bool
	Reason string
	// Degraded is non-empty when a data-artifact defect forced this
	// Decision to fail open rather than resolve normally. A caller must
	// still allow the call but should surface Degraded loudly (e.g. on
	// stderr), since it names a packaging defect worth fixing.
	Degraded string
}

// Decide evaluates one PreToolUse call against the worktree-isolation
// invariant: a repo-modifying write outside a worktree is denied, and a
// call this gate cannot resolve confidently is denied too (fail closed) --
// except when the classifier's own data artifact is the thing that failed,
// which fails open instead (see Decision.Degraded).
func Decide(lstat LstatFunc, readFile ReadFileFunc, verbs Verbs, verbsErr error, trackingDocs TrackingDocs, trackingDocsErr error, in Input) Decision {
	switch in.ToolName {
	case "Write", "Edit":
		return decideFileWrite(lstat, readFile, trackingDocs, trackingDocsErr, in)
	case "Bash":
		return decideBash(lstat, readFile, verbs, verbsErr, in)
	default:
		return Decision{}
	}
}

func decideFileWrite(lstat LstatFunc, readFile ReadFileFunc, trackingDocs TrackingDocs, trackingDocsErr error, in Input) Decision {
	filePath := in.FilePath
	if filePath == "" {
		return Decision{}
	}

	if underProjectDir(in.ProjectDir, filePath) {
		if trackingDocsErr != nil {
			// Can't verify tracking-doc membership without the data
			// artifact -- fail open rather than risk denying a legitimate
			// tracking-doc write on a packaging defect.
			return Decision{Degraded: trackingDocsErr.Error()}
		}
		if trackingDocs.has(filepath.Base(filePath)) {
			return Decision{}
		}
	}

	root, gitEntry, found, err := FindRepoRoot(lstat, filepath.Dir(filePath))
	if err != nil {
		return Decision{Deny: true, Reason: fmt.Sprintf(
			"worktree-gate: cannot determine whether %q is inside a git repository (%v); denying rather than risk an unisolated write", filePath, err)}
	}
	if !found {
		return Decision{} // confidently outside any repo: out of scope
	}

	switch ClassifyGitEntry(lstat, readFile, gitEntry) {
	case KindWorktree:
		return Decision{}
	case KindPrimary:
		return Decision{Deny: true, Reason: fmt.Sprintf(
			"worktree-gate: %q writes into the primary checkout of %q, not a worktree; create one and retry", filePath, root)}
	default:
		return Decision{Deny: true, Reason: fmt.Sprintf(
			"worktree-gate: cannot determine worktree membership of %q; denying rather than risk an unisolated write", root)}
	}
}

// underProjectDir reports whether filePath sits at any depth under
// projectDir. False when projectDir is empty (unset) or filePath resolves
// outside it, including a same-prefix sibling directory.
func underProjectDir(projectDir, filePath string) bool {
	if projectDir == "" {
		return false
	}
	rel, err := filepath.Rel(projectDir, filePath)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func decideBash(lstat LstatFunc, readFile ReadFileFunc, verbs Verbs, verbsErr error, in Input) Decision {
	if strings.TrimSpace(in.Command) == "" {
		return Decision{}
	}
	if in.CWD == "" {
		return Decision{Deny: true, Reason: "worktree-gate: no working directory reported for this Bash call; denying rather than risk an unisolated write"}
	}

	root, gitEntry, found, err := FindRepoRoot(lstat, in.CWD)
	if err != nil {
		return Decision{Deny: true, Reason: fmt.Sprintf(
			"worktree-gate: cannot determine whether %q is inside a git repository (%v); denying rather than risk an unisolated write", in.CWD, err)}
	}
	if !found {
		return Decision{} // confidently outside any repo: out of scope
	}

	kind := ClassifyGitEntry(lstat, readFile, gitEntry)
	if kind == KindWorktree {
		// Already an allowed location -- nothing to deny regardless of
		// command classification or classifier health.
		return degradedOnly(verbsErr)
	}

	if kind == KindPrimary && in.MergeGateEnabled && isSanctionedLandingCommand(in.Command) {
		// The documented landing-merge flow runs `git merge`/`git commit`
		// directly from the primary checkout -- an explicit, narrow
		// override of the primary-checkout deny, independent of classifier
		// health.
		return degradedOnly(verbsErr)
	}

	if verbsErr != nil {
		// The classifier itself is broken: a packaging defect, not a
		// signal about this command. Fail open, loud, never deny on it.
		return degradedOnly(verbsErr)
	}

	switch ClassifyBash(verbs, in.Command) {
	case ClassRead:
		return Decision{}
	default: // ClassWrite or ClassUncertain: the conservative over-approximation
		if kind == KindPrimary {
			return Decision{Deny: true, Reason: fmt.Sprintf(
				"worktree-gate: %q may modify %q outside a worktree; create one and retry", in.Command, root)}
		}
		return Decision{Deny: true, Reason: fmt.Sprintf(
			"worktree-gate: cannot determine worktree membership of %q for %q; denying rather than risk an unisolated write", root, in.Command)}
	}
}

func degradedOnly(verbsErr error) Decision {
	if verbsErr == nil {
		return Decision{}
	}
	return Decision{Degraded: verbsErr.Error()}
}
