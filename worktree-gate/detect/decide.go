package detect

import (
	"crypto/sha256"
	"encoding/hex"
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

	// ProvisionedBinPath and ProvisionedBinDigest carry SC15's landing-verb
	// allowance inputs. Both arrive as ARGV -- never from the environment: the
	// absolute path of the plugin-provisioned CLI, and the expected sha256 of
	// the binary at that path (the host's per-(os,arch) row). Either one empty
	// disables the allowance, so a call that would otherwise qualify is denied
	// on its own merits.
	ProvisionedBinPath   string
	ProvisionedBinDigest string
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
// the call was already independently resolved. On the Bash axis the verdict
// also judges the paths a write-class piece names, not just its effective cwd
// (SC20), and exempts the digest-verified provisioned landing CLI (SC15).
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

	cwd, cwdUnresolvable := effectiveBashCWD(in.CWD, in.Command)

	// Classification runs AHEAD of the two cwd short-circuits below (outside
	// any repo, and inside a worktree). SC15's per-piece allowance (evaluated
	// first) and SC20's named-path rule are settled here, so a write-class
	// piece that names a path resolving into a primary checkout is denied
	// however its cwd lands, while the class the remaining pieces carry feeds
	// the cwd-based verdict. A broken classifier can't resolve write-class, so
	// classification is skipped and the fail-closed cwd leg governs alone.
	var class BashClass
	if verbsErr == nil {
		var deny *Decision
		class, deny = scanBash(lstat, readFile, verbs, in.ProvisionedBinPath, in.ProvisionedBinDigest, in.Command, cwd, cwdUnresolvable, 0)
		if deny != nil {
			return *deny
		}
	}

	if cwdUnresolvable {
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
		// Confidently outside any repo: out of scope. A write-class piece
		// naming a primary-checkout path was already denied above, so a
		// ClassUncertain piece run from here staying allowed is OQ19's
		// disclosed residual, not a bypass.
		return Decision{}
	}

	kind := ClassifyGitEntry(lstat, readFile, gitEntry)
	if kind == KindWorktree {
		// Already an allowed location -- nothing to deny regardless of the
		// remaining pieces' classification or classifier health.
		return degradedOnly(verbsErr)
	}

	if verbsErr != nil {
		// The classifier itself is broken and this location isn't already
		// independently safe: the defect could be masking a real write, so
		// deny rather than fail open on it.
		return Decision{Deny: true, Reason: fmt.Sprintf(
			"worktree-gate: cannot classify %q (%v); denying rather than risk an unisolated write", in.Command, verbsErr)}
	}

	switch class {
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

// scanBash walks a command's pieces the way ClassifyBash does -- including
// SC16's recursion into decomposable interiors and here-document bodies --
// but layers two per-piece rules ahead of the class tally. SC15's
// provisioned-CLI allowance, evaluated FIRST and only at the top level,
// exempts a qualifying piece from both the tally and the named-path rule
// (so `worktree add <primary>/…` is not denied by the very rule shipped
// alongside it). SC20's named-path rule then denies a write-class piece that
// names a path resolving into a primary checkout, however that path is
// spelled. It returns the strictest class over the non-exempt pieces and the
// first named-path denial found; unlike ClassifyBash it does not stop at the
// first write, since a later piece may name a primary-checkout path the rule
// must still catch (the allowance is per piece, never per command).
func scanBash(lstat LstatFunc, readFile ReadFileFunc, verbs Verbs, sc15Path, sc15Digest, command, cwd string, cwdUnresolvable bool, depth int) (BashClass, *Decision) {
	worst := ClassRead
	for _, p := range decompose(command) {
		// SC15's exemption waives only this piece's own class tally and
		// named-path rule -- not the SC16 recursion below. A qualifying
		// landing invocation carries no decomposable interior, so recursing an
		// exempt piece never denies a legitimate call; but a command
		// substitution or backtick smuggled into its argv
		// (`<bin> merge $(rm -rf <primary>/x)`) is shell-level and runs
		// regardless of the CLI, so it must still face the same interior scan
		// every other piece does, or the exemption becomes a smuggling hole.
		exempt := depth == 0 && sc15Exempt(readFile, sc15Path, sc15Digest, p)
		if !exempt {
			pc := classifyPiece(verbs, p)
			if pc == ClassWrite {
				if d := namedPathDenial(lstat, readFile, p, cwd, cwdUnresolvable); d != nil {
					return ClassWrite, d
				}
			}
			worst = stricter(worst, pc)
		}
		if depth >= maxInteriorDepth {
			continue
		}
		for _, interior := range decomposableInteriors(p.raw) {
			ic, d := scanBash(lstat, readFile, verbs, sc15Path, sc15Digest, interior, cwd, cwdUnresolvable, depth+1)
			if d != nil {
				return ClassWrite, d
			}
			worst = stricter(worst, ic)
		}
		for _, body := range p.heredocs {
			worst = stricter(worst, boundedVerdict(verbs, body))
		}
	}
	return worst, nil
}

// namedPathDenial applies SC20's rule to a piece already resolved write-class:
// each path the piece names is resolved (an absolute target as-is, a relative
// one against the effective cwd) and denied if it lands in a primary checkout,
// however it was spelled. An unexpandable target (a variable, glob, or
// ~-prefix) is denied outright on SC5's precedent, since the gate cannot rule
// out a primary checkout. A target confidently outside any repo, or inside a
// worktree, is not this rule's concern; an indeterminate one fails closed. A
// relative target with no static cwd is left to the caller's cwd leg.
func namedPathDenial(lstat LstatFunc, readFile ReadFileFunc, p piece, cwd string, cwdUnresolvable bool) *Decision {
	for _, raw := range namedPaths(p) {
		t := stripQuotes(raw)
		if t == "" {
			continue
		}
		if isUnexpandable(t) {
			return &Decision{Deny: true, Reason: fmt.Sprintf(
				"worktree-gate: %q names a write target this gate cannot resolve statically; denying rather than risk a write into a primary checkout", t)}
		}
		var abs string
		switch {
		case filepath.IsAbs(t):
			abs = t
		case cwdUnresolvable || cwd == "":
			continue // relative target, no static cwd: the cwd leg governs
		default:
			abs = filepath.Join(cwd, t)
		}
		kind, found, err := namedPathKind(lstat, readFile, abs)
		if err != nil {
			return &Decision{Deny: true, Reason: fmt.Sprintf(
				"worktree-gate: cannot determine whether the write target %q is inside a git repository (%v); denying rather than risk an unisolated write", abs, err)}
		}
		if !found {
			continue // confidently outside any repo
		}
		switch kind {
		case KindWorktree:
			continue // writing into a worktree is already isolated
		case KindPrimary:
			return &Decision{Deny: true, Reason: fmt.Sprintf(
				"worktree-gate: this command writes into the primary checkout via %q, not a worktree; create one and retry", abs)}
		default:
			return &Decision{Deny: true, Reason: fmt.Sprintf(
				"worktree-gate: cannot determine worktree membership of the write target %q; denying rather than risk an unisolated write", abs)}
		}
	}
	return nil
}

// namedPaths returns the path candidates a write-class piece names: its argv
// words (redirect operators and their targets already delimited out by
// decompose) plus every file-writing redirect target recovered from the raw
// segment. Both halves are tokenized by whitespace and by redirect operator,
// so a target glued to the word beside it is still seen as a path rather than
// missed by a fields-only split.
func namedPaths(p piece) []string {
	out := shellTokens(p.argv)
	return append(out, outputRedirectTargets(p.raw)...)
}

// outputRedirectTargets recovers the file-writing redirect targets from a raw
// segment, using the same longest-first operator predicate and fd-dup
// exclusion the decomposition applies, so `echo x>&2` yields no target while
// `echo x>&/p/f` yields `/p/f`. Quotes and backslashes are honored so a `>`
// inside "a > b" never reads as an operator.
func outputRedirectTargets(raw string) []string {
	var targets []string
	i, n := 0, len(raw)
	atWordStart := true
	inSingle, inDouble := false, false
	for i < n {
		c := raw[i]
		switch {
		case inSingle:
			if c == '\'' {
				inSingle = false
			}
			i++
			atWordStart = false
			continue
		case inDouble:
			if c == '"' {
				inDouble = false
			}
			i++
			atWordStart = false
			continue
		case c == '\\':
			i += 2
			atWordStart = false
			continue
		case c == '\'':
			inSingle = true
			i++
			atWordStart = false
			continue
		case c == '"':
			inDouble = true
			i++
			atWordStart = false
			continue
		}
		if length, output, ok := redirectOperatorAt(raw, i, atWordStart); ok {
			i += length
			for i < n && raw[i] == ' ' {
				i++
			}
			target, next := readTarget(raw, i)
			i = next
			if output && target != "" && !isFdDupTarget(target) {
				targets = append(targets, target)
			}
			atWordStart = true
			continue
		}
		atWordStart = c == ' '
		i++
	}
	return targets
}

// sc15Exempt reports whether a top-level piece is SC15's sanctioned landing
// invocation: its leading token is the argv-supplied provisioned binary path,
// the binary at that path re-hashes to the argv-supplied expected digest, its
// verb is one of the three landing verbs, and it carries no repo-retargeting
// flag or redirect. The allowance is keyed on BINARY IDENTITY, not a command
// word, so a bare/relative/PATH-resolved name, a wrong digest, or a missing
// argv parameter all fall through to the fail-closed verdict.
func sc15Exempt(readFile ReadFileFunc, verifiedPath, expectedDigest string, p piece) bool {
	expected := strings.ToLower(strings.TrimSpace(expectedDigest))
	if verifiedPath == "" || expected == "" {
		return false // both parameters arrive as argv; absence of either denies
	}
	if p.hasRedirect || len(p.heredocs) > 0 {
		return false // a redirect on the CLI piece is a smuggled write, not the allowance
	}
	toks := shellTokens(p.argv)
	for i, tok := range toks {
		toks[i] = stripQuotes(tok)
	}
	if len(toks) < 2 || toks[0] != verifiedPath {
		return false
	}
	if !sc15VerbAllowed(toks[1:]) || sc15Retargets(toks[1:]) {
		return false
	}
	b, err := readFile(verifiedPath)
	if err != nil {
		return false // cannot re-verify the binary's identity: not the allowance
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]) == expected
}

// sc15VerbAllowed reports whether the tokens after the binary name one of the
// three landing verbs -- merge, push, or worktree add. resign is deliberately
// excluded.
func sc15VerbAllowed(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "merge", "push":
		return true
	case "worktree":
		return len(args) >= 2 && args[1] == "add"
	default:
		return false
	}
}

// sc15Retargets reports whether any token is a repo-retargeting flag, which
// voids the allowance -- the sanctioned channel acts on the repo it is invoked
// in, never one it is pointed at.
func sc15Retargets(args []string) bool {
	for _, a := range args {
		if a == "--repo" || a == "--config" ||
			strings.HasPrefix(a, "--repo=") || strings.HasPrefix(a, "--config=") {
			return true
		}
	}
	return false
}

func degradedOnly(verbsErr error) Decision {
	if verbsErr == nil {
		return Decision{}
	}
	return Decision{Degraded: verbsErr.Error()}
}
