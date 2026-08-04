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
)

// Input is the subset of a PreToolUse payload the gate needs, plus the
// environment signal its tracking-doc exemption reads.
type Input struct {
	ToolName string // "Write", "Edit", or "Bash"
	CWD      string // session working directory, used for Bash
	FilePath string // Write/Edit target
	Command  string // Bash command

	// ProjectDir is CLAUDE_PROJECT_DIR, empty when unset. Feeds the
	// tracking-doc exemption in decideFileWrite.
	ProjectDir string
}

// Decision is the gate's verdict for one tool call.
type Decision struct {
	// Deny blocks the call. Reason is the operator-facing explanation.
	Deny   bool
	Reason string
	// Degraded is non-empty when a data-artifact defect was detected but
	// didn't change this Decision -- the call was already resolved on its
	// own merits (e.g. confirmed inside a worktree) without needing the
	// broken artifact. A caller should still surface Degraded loudly (e.g.
	// on stderr), since it names a packaging defect worth fixing, but must
	// never read it as "this call was allowed because of the defect": a
	// defect that could have changed the verdict denies instead (fail
	// closed), it never fails open.
	Degraded string
}

// Decide evaluates one PreToolUse call against the worktree-isolation
// invariant: a repo-modifying write outside a worktree is denied, a call
// this gate cannot resolve confidently is denied too (fail closed), and a
// classifier or tracking-doc data-artifact defect that could have affected
// the verdict denies as well rather than failing open. The one exception is
// Decision.Degraded: a defect surfaced without changing the verdict, because
// the call was already independently resolved.
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
			// artifact -- deny rather than risk allowing an unisolated
			// write on a packaging defect.
			return Decision{Deny: true, Reason: fmt.Sprintf(
				"worktree-gate: cannot verify the tracking-doc exemption for %q (%v); denying rather than risk an unisolated write", filePath, trackingDocsErr)}
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

	cwd, unresolvable := effectiveBashCWD(in.CWD, in.Command)
	if unresolvable {
		return Decision{Deny: true, Reason: fmt.Sprintf(
			"worktree-gate: %q resolves to a working directory this gate cannot determine statically; denying rather than risk an unisolated write", in.Command)}
	}
	if cwd == "" {
		return Decision{Deny: true, Reason: "worktree-gate: no working directory reported for this Bash call; denying rather than risk an unisolated write"}
	}

	root, gitEntry, found, err := FindRepoRoot(lstat, cwd)
	if err != nil {
		return Decision{Deny: true, Reason: fmt.Sprintf(
			"worktree-gate: cannot determine whether %q is inside a git repository (%v); denying rather than risk an unisolated write", cwd, err)}
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

	if verbsErr != nil {
		// The classifier itself is broken and this location isn't already
		// independently safe: the defect could be masking a real write, so
		// deny rather than fail open on it.
		return Decision{Deny: true, Reason: fmt.Sprintf(
			"worktree-gate: cannot classify %q (%v); denying rather than risk an unisolated write", in.Command, verbsErr)}
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
