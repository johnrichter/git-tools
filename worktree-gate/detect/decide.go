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
// but layers SC15's two provisioned-CLI allowances and SC20's named-path rule
// onto the class tally. The write allowance (sc15Exempt), evaluated FIRST and
// only at the top level, exempts a qualifying landing-write piece from both the
// tally and the named-path rule (so `worktree add <primary>/…` is not denied by
// the very rule shipped alongside it). The read allowance (sc15ReadAllowed) is
// applied instead only where a piece would otherwise be ClassUncertain -- after
// classifyPiece has resolved any file-opening redirect to write -- reclassifying
// the CLI's one read verb (worktree list) as read rather than waiving any rule.
// SC20's named-path rule then denies a write-class piece that names a path
// resolving into a primary checkout, however that path is spelled. It returns
// the strictest class over the non-exempt pieces and the first named-path denial
// found; unlike ClassifyBash it does not stop at the first write, since a later
// piece may name a primary-checkout path the rule must still catch (the
// allowances are per piece, never per command).
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
			if pc == ClassUncertain && depth == 0 &&
				sc15ReadAllowed(readFile, sc15Path, sc15Digest, p) {
				// SC15's read allowance: the digest-verified CLI's one read verb
				// (worktree list) writes nothing, so a piece that would otherwise
				// fail closed as ClassUncertain reads instead. Evaluated only
				// here, after classifyPiece has already resolved a file-opening
				// output redirect to ClassWrite, so `worktree list > <primary>/f`
				// is caught by the class tally and the named-path rule below
				// rather than read away into a primary checkout.
				pc = ClassRead
			}
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
// relative target with no static cwd is left to the caller's cwd leg. A
// target that resolves into a primary checkout but sits under its worktree
// home (FB7) is exempted as gate-managed scratch rather than denied -- see
// isWorktreeHomeScratch.
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
		kind, root, found, err := namedPathKind(lstat, readFile, abs)
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
			if isWorktreeHomeScratch(root, abs) {
				continue // FB7: gate-managed scratch under the worktree home, not repository content
			}
			return &Decision{Deny: true, Reason: fmt.Sprintf(
				"worktree-gate: this command writes into the primary checkout via %q, not a worktree; create one and retry", abs)}
		default:
			return &Decision{Deny: true, Reason: fmt.Sprintf(
				"worktree-gate: cannot determine worktree membership of the write target %q; denying rather than risk an unisolated write", abs)}
		}
	}
	return nil
}

// worktreeHomeName is the directory, relative to a primary checkout's root,
// where this project's tooling stages linked worktrees. A named target under
// it is the gate's own scratch state, not tracked repository content.
const worktreeHomeName = ".claude/worktrees"

// isWorktreeHomeScratch reports whether absPath is a descendant of root's
// worktree home. It is called only once namedPathDenial has already
// classified absPath KindPrimary, which means the walk-up from absPath never
// crossed a live worktree's own .git redirect first -- so a target under the
// home reaching here is leftover scratch (a dangling symlink, an orphaned
// checkout directory), never a live worktree itself; that case already
// returned via KindWorktree. FB7's reproduction was exactly this: a dangling
// symlink at <primary>/.claude/worktrees/<name>, denied identically from
// every cwd because the rule runs ahead of the cwd short-circuits.
func isWorktreeHomeScratch(root, absPath string) bool {
	home := filepath.Join(root, filepath.FromSlash(worktreeHomeName))
	rel, err := filepath.Rel(home, absPath)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// namedPaths returns the paths a write-class piece actually writes to: its
// file-writing redirect targets, plus the destination operands of the command
// itself. Only destinations are returned. A command's read sources, its verb
// and subcommand words, its option flags, and an option's non-path value (a
// commit message, a mode) name no file the command writes, so they are left
// unjudged -- otherwise a benign token that happens to be unexpandable
// (`git commit -m "$(date)"`, `cp "$SRC" dst`) would be mistaken for a write
// into a primary checkout. A command whose writes land in its working directory
// rather than a named operand (git's commit/add/reset/…, a package or build
// tool) names no operand here at all; that locus is the cwd leg's to judge,
// including a `git -C` retarget the resolver already composes. Redirect targets
// are included whatever the command, since the shell -- not the command --
// opens them; they are returned first, ahead of the command's own destination
// operands, so a denial on a piece carrying both names the redirect's real
// target rather than a same-verdict operand that merely happens to resolve
// into the same checkout first (`echo x >> primary/f` denies on `primary/f`,
// not on `x`).
func namedPaths(p piece) []string {
	targets := outputRedirectTargets(p.raw)
	toks := skipAssignments(shellTokens(p.argv))
	if len(toks) == 0 {
		return targets
	}
	switch cmd := commandWord(toks[0]); {
	case cmd == "git":
		return append(targets, gitDestinations(toks[1:])...)
	case isCopyLikeWriter(cmd):
		return append(targets, copyDestinations(toks[1:])...)
	default:
		// An unmodeled write command (rm, tee, sed -i, an editor, find
		// -delete, …) writes the operands it names, so every non-flag operand
		// is a candidate destination -- the conservative default that keeps a
		// future write verb judged rather than silently exempt.
		return append(targets, operands(toks[1:])...)
	}
}

// commandWord reduces a leading argv token to a bare command name: quotes
// stripped, directory prefix removed, lower-cased, so "/usr/bin/cp", "'cp'",
// and "cp" all read as "cp".
func commandWord(tok string) string {
	return strings.ToLower(filepath.Base(stripQuotes(tok)))
}

func isCopyLikeWriter(cmd string) bool {
	switch cmd {
	case "cp", "mv", "ln", "install":
		return true
	}
	return false
}

// operands returns the non-flag tokens in args, quote-stripped, treating every
// token after a bare "--" as an operand even if it begins with "-".
func operands(args []string) []string {
	var out []string
	rest := false
	for _, a := range args {
		if !rest && a == "--" {
			rest = true
			continue
		}
		if !rest && strings.HasPrefix(a, "-") {
			continue
		}
		out = append(out, stripQuotes(a))
	}
	return out
}

// copyDestinations returns the write destination of a cp/mv/ln/install
// invocation: a -t/--target-directory value when present, otherwise the last
// positional operand. The leading operands are read sources, so a source that
// is a variable or a substitution does not read as a write into a primary
// checkout.
func copyDestinations(args []string) []string {
	if dir, ok := targetDirectory(args); ok {
		return []string{stripQuotes(dir)}
	}
	ops := operands(args)
	if len(ops) == 0 {
		return nil
	}
	return ops[len(ops)-1:]
}

// targetDirectory returns the value of a -t / --target-directory option, which
// names the destination regardless of operand order (its split, glued, and
// =-joined spellings).
func targetDirectory(args []string) (string, bool) {
	for i, a := range args {
		switch {
		case a == "-t" || a == "--target-directory":
			if i+1 < len(args) {
				return args[i+1], true
			}
		case strings.HasPrefix(a, "--target-directory="):
			return a[len("--target-directory="):], true
		case strings.HasPrefix(a, "-t") && len(a) > 2:
			return a[2:], true
		}
	}
	return "", false
}

// gitDestinations returns the filesystem path a git subcommand writes as a
// named operand, or nil when git writes its own working tree instead -- the
// common case, governed by the cwd leg (including a composed `git -C`). Only
// clone, init, and worktree add take an explicit destination path.
func gitDestinations(args []string) []string {
	sub, rest := gitSubcommand(args)
	switch sub {
	case "clone":
		// git clone [opts] <url> [<dir>]: an explicit target dir is the last
		// positional; a lone url clones into the cwd, which the cwd leg judges.
		if ops := operands(rest); len(ops) >= 2 {
			return ops[len(ops)-1:]
		}
	case "init":
		if ops := operands(rest); len(ops) > 0 {
			return ops[:1]
		}
	case "worktree":
		if len(rest) > 0 && rest[0] == "add" {
			if ops := operands(rest[1:]); len(ops) > 0 {
				return ops[:1]
			}
		}
	}
	return nil
}

// gitSubcommand skips git's global options -- their split, glued, and =-joined
// value forms -- and returns the subcommand token with the arguments after it.
func gitSubcommand(args []string) (string, []string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			if i+1 < len(args) {
				return args[i+1], args[i+2:]
			}
			return "", nil
		case a == "-C" || a == "-c" || a == "--git-dir" || a == "--work-tree" || a == "--namespace":
			i++ // this global option consumes the next token as its value
		case strings.HasPrefix(a, "-"):
			// a valueless or =-joined/glued global option: one token
		default:
			return a, args[i+1:]
		}
	}
	return "", nil
}

// gitReadSubcommands and gitWriteSubcommands are the plain git verbs whose
// class is fixed by the subcommand alone. Matching is on the exact subcommand
// token gitSubcommand isolates -- not a loose prefix -- so merge-base is a read
// even though merge is a write, and a leading global option never changes the
// verdict. remote, branch, tag, worktree, config, reflog, and stash are not
// here: each mixes read and write forms and is split on its own operands below.
var gitReadSubcommands = map[string]bool{
	"status": true, "diff": true, "log": true, "show": true, "fetch": true,
	"blame": true, "describe": true, "rev-parse": true, "ls-files": true,
	"ls-remote": true, "ls-tree": true, "cat-file": true, "grep": true,
	"merge-base": true,
}

var gitWriteSubcommands = map[string]bool{
	"commit": true, "add": true, "rm": true, "mv": true, "merge": true,
	"rebase": true, "checkout": true, "switch": true, "reset": true,
	"apply": true, "am": true, "cherry-pick": true, "revert": true,
	"init": true, "clone": true, "push": true, "pull": true,
	"submodule": true, "lfs": true,
}

// classifyGit classifies a `git …` invocation from the tokens after the `git`
// word. It skips git's leading global options via gitSubcommand so a verb
// classifies identically with or without one, matches the plain read/write
// sets on the exact subcommand, and routes a verb that mixes read and write
// forms to its own splitter. An unrecognized subcommand -- or an unrecognized
// form of a split verb -- classifies write, the conservative default.
func classifyGit(args []string) BashClass {
	sub, rest := gitSubcommand(args)
	sub = strings.ToLower(sub)
	switch {
	case gitReadSubcommands[sub]:
		return ClassRead
	case gitWriteSubcommands[sub]:
		return ClassWrite
	}
	switch sub {
	case "remote":
		return classifyGitRemote(rest)
	case "branch":
		return classifyGitBranch(rest)
	case "tag":
		return classifyGitTag(rest)
	case "worktree":
		return classifyGitSubSelect(rest, gitWorktreeReadSubcommands)
	case "reflog":
		return classifyGitSubSelect(rest, gitReflogReadSubcommands)
	case "config":
		return classifyGitConfig(rest)
	case "stash":
		// Every stash form -- including bare `git stash`, `list`, and `show`
		// -- is treated as a write and denied outside a worktree.
		return ClassWrite
	}
	return ClassWrite
}

// classifyGitRemote reads bare `git remote`, its verbose listing, and the show
// and get-url subcommands; every mutating subcommand (add, remove, rename,
// set-*, prune, update) and any unrecognized form is a write.
func classifyGitRemote(rest []string) BashClass {
	if len(rest) == 0 {
		return ClassRead
	}
	switch strings.ToLower(rest[0]) {
	case "-v", "--verbose", "show", "get-url":
		return ClassRead
	}
	return ClassWrite
}

// gitBranchListFlags and gitTagListFlags force git into list mode: with one
// present a positional operand is a pattern or value, not a new ref, so the
// verb reads even alongside an operand (`git branch --contains <commit>`,
// `git tag -l '<pattern>'`, `git tag -v <tag>`). git's -a/-r reject an operand
// outright, so they read too -- git refuses the create, nothing is written.
var gitBranchListFlags = map[string]bool{
	"-a": true, "--all": true, "-r": true, "--remotes": true,
	"-l": true, "--list": true, "--show-current": true,
	"--contains": true, "--no-contains": true,
	"--merged": true, "--no-merged": true, "--points-at": true,
}

var gitTagListFlags = map[string]bool{
	"-l": true, "--list": true, "--contains": true, "--no-contains": true,
	"--points-at": true, "--merged": true, "--no-merged": true,
	"-v": true, "--verify": true,
}

// gitBranchModifierFlags and gitTagModifierFlags tune a listing's output but do
// NOT force list mode: `git branch -v` lists, yet `git branch -v <name>` still
// CREATES <name> (verified against git; likewise --sort/--format/--column). So
// a modifier flag reads on its own but never neutralizes an operand -- an
// operand alongside only modifier flags is a create (write).
var gitBranchModifierFlags = map[string]bool{
	"-v": true, "-vv": true, "--verbose": true, "--format": true, "--sort": true,
}

var gitTagModifierFlags = map[string]bool{
	"--sort": true, "--format": true, "--column": true,
}

// classifyGitBranch splits git branch, whose listing and mutating forms share a
// verb; see classifyGitRefEditFn for the operand rule.
func classifyGitBranch(rest []string) BashClass {
	return classifyGitRefEditFn(rest,
		func(tok string) bool { return gitBranchListFlags[gitFlagKey(tok)] },
		func(tok string) bool { return gitBranchModifierFlags[gitFlagKey(tok)] },
	)
}

// classifyGitTag splits git tag. It adds -n/-n<num> (show annotation lines,
// which force list mode) to tag's list-forcing flags; -v verifies a signature
// (a read that names an existing tag), never "verbose".
func classifyGitTag(rest []string) BashClass {
	isListForcing := func(tok string) bool {
		if gitTagListFlags[gitFlagKey(tok)] {
			return true
		}
		if strings.HasPrefix(tok, "-n") {
			for _, c := range tok[2:] {
				if c < '0' || c > '9' {
					return false
				}
			}
			return true
		}
		return false
	}
	return classifyGitRefEditFn(rest, isListForcing,
		func(tok string) bool { return gitTagModifierFlags[gitFlagKey(tok)] })
}

// classifyGitRefEditFn classifies git branch / git tag from their operands. A
// mutating or unrecognized flag (delete/move/copy/annotate/sign/config or an
// unknown one) is a write and dominates. A positional operand creates a ref --
// a write -- UNLESS a list-forcing flag is present, which turns the operand
// into a pattern or value (a read). A modifier flag (-v/--sort/--format) reads
// on its own but does not neutralize an operand. A bare invocation lists.
func classifyGitRefEditFn(rest []string, isListForcing, isModifier func(string) bool) BashClass {
	if len(rest) == 0 {
		return ClassRead
	}
	sawListForcing, sawWriteFlag, sawOperand := false, false, false
	for _, tok := range rest {
		switch {
		case tok == "--":
			continue
		case isFlagToken(tok):
			switch {
			case isListForcing(tok):
				sawListForcing = true
			case isModifier(tok):
				// reads alone, but does not make an operand a listing
			default:
				sawWriteFlag = true // delete/move/copy/annotate/sign/unknown
			}
		default:
			sawOperand = true // a positional operand
		}
	}
	switch {
	case sawWriteFlag:
		// A mutating or unrecognized flag dominates any listing flag present.
		return ClassWrite
	case sawOperand && !sawListForcing:
		// A create -- `git branch <name>`, `git branch -v <name>`, `git tag <t>`
		// -- since no list-forcing flag turned the operand into a pattern/value.
		return ClassWrite
	default:
		return ClassRead
	}
}

var gitWorktreeReadSubcommands = map[string]bool{"list": true}
var gitReflogReadSubcommands = map[string]bool{"show": true}

// classifyGitSubSelect reads only the listed subcommands of a verb whose other
// forms mutate (git worktree, git reflog); every other form, including bare,
// is a write.
func classifyGitSubSelect(rest []string, read map[string]bool) BashClass {
	if read[strings.ToLower(firstOperand(rest))] {
		return ClassRead
	}
	return ClassWrite
}

// classifyGitConfig reads only --get, --list, and -l (exact matches, so
// --get-regexp and --get-all fall through to write); every other form --
// setting, unsetting, adding, or a positional set -- is a write.
func classifyGitConfig(rest []string) BashClass {
	for _, tok := range rest {
		switch gitFlagKey(tok) {
		case "--get", "--list", "-l":
			return ClassRead
		}
	}
	return ClassWrite
}

// firstOperand returns the first non-flag token in args (skipping a bare "--"
// separator), or "" when there is none.
func firstOperand(args []string) string {
	for _, a := range args {
		if a == "--" || isFlagToken(a) {
			continue
		}
		return a
	}
	return ""
}

// isFlagToken reports whether tok is an option flag (a leading "-", but not a
// bare "-" stdin operand).
func isFlagToken(tok string) bool {
	return strings.HasPrefix(tok, "-") && tok != "-"
}

// gitFlagKey reduces a long option to its name by dropping a "=value" suffix
// (`--sort=-committerdate` -> `--sort`); a short flag is returned unchanged.
func gitFlagKey(tok string) string {
	if strings.HasPrefix(tok, "--") {
		if i := strings.IndexByte(tok, '='); i >= 0 {
			return tok[:i]
		}
	}
	return tok
}

// outputRedirectTargets recovers the file-writing redirect targets from a raw
// segment, using the same longest-first operator predicate and the same
// discard/dup exclusion (isFdDupOrDiscardTarget) the decomposition applies:
// `echo x>&2` yields no target (a genuine duplication under the dup-capable
// `>&` operator), `echo x>/dev/null` yields none either (the discard
// device), while `echo x>&/p/f` and `echo x>1` (a plain, non-dup-capable
// operator) both yield their real path. Quotes and backslashes are honored
// so a `>` inside "a > b" never reads as an operator.
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
			dupCapable := strings.HasSuffix(raw[i:i+length], "&")
			i += length
			for i < n && raw[i] == ' ' {
				i++
			}
			target, next := readTarget(raw, i)
			i = next
			if output && target != "" && !isFdDupOrDiscardTarget(target, dupCapable) {
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

// sc15Identity reports whether a top-level piece is an invocation of SC15's
// digest-verified provisioned CLI over a channel the shell cannot use to
// smuggle a write past it: its leading token is the argv-supplied provisioned
// binary path, the binary at that path re-hashes to the argv-supplied expected
// digest, and it opens no file -- no redirect that opens a path in either
// direction, no here-document. A bare file-descriptor duplication (2>&1) opens
// nothing and so does not void identity, the common spelling of a landing call.
// Identity is keyed on BINARY IDENTITY, never a command word or basename, so a
// bare/relative/PATH-resolved name, a wrong digest, an unreadable binary, or a
// missing argv parameter all fall through to the fail-closed verdict. On a
// match it returns the tokens after the path so a caller can apply its own verb
// policy; ok is false when identity does not hold. This is the shared half of
// both SC15 allowances -- the write exemption (sc15Exempt) and the read
// reclassification (sc15ReadAllowed) layer their own verb policy behind it.
func sc15Identity(readFile ReadFileFunc, verifiedPath, expectedDigest string, p piece) (args []string, ok bool) {
	expected := strings.ToLower(strings.TrimSpace(expectedDigest))
	if verifiedPath == "" || expected == "" {
		return nil, false // both parameters arrive as argv; absence of either denies
	}
	if p.openingRedirect || len(p.heredocs) > 0 {
		return nil, false // a file-opening redirect is the shell's write, not the CLI's
	}
	toks := shellTokens(p.argv)
	for i, tok := range toks {
		toks[i] = stripQuotes(tok)
	}
	if len(toks) < 2 || toks[0] != verifiedPath {
		return nil, false
	}
	b, err := readFile(verifiedPath)
	if err != nil {
		return nil, false // cannot re-verify the binary's identity: not the CLI
	}
	sum := sha256.Sum256(b)
	if hex.EncodeToString(sum[:]) != expected {
		return nil, false
	}
	return toks[1:], true
}

// sc15Exempt reports whether a top-level piece is SC15's sanctioned landing
// WRITE invocation: it clears the shared identity check and its verb is one of
// the four landing verbs (merge, push, worktree add, worktree remove) carrying
// neither a repo-retargeting flag nor a cleanup-forcing --force -- the
// sanctioned channel acts on the repo it is invoked in, never one it is pointed
// at, and never destroys unseen state on a forced cleanup it was not asked to
// prove safe. An exempt piece is waived from both the class tally and the
// named-path rule, so `worktree add <primary>/…` is not denied by the very rule
// shipped alongside it.
func sc15Exempt(readFile ReadFileFunc, verifiedPath, expectedDigest string, p piece) bool {
	args, ok := sc15Identity(readFile, verifiedPath, expectedDigest, p)
	if !ok {
		return false
	}
	return sc15VerbAllowed(args) && !sc15Retargets(args) && !sc15ForcesCleanup(args)
}

// sc15ReadAllowed reports whether a top-level piece is the digest-verified CLI's
// one READ verb, worktree list. It shares sc15Identity with the write allowance
// but not its verb policy: a read verb writes nothing and names no repo it could
// be retargeted onto, so a --repo/--config flag does not void it (retargeting is
// only the write channel's concern). Unlike the write allowance it waives no
// rule -- the caller applies it only where a piece would otherwise be
// ClassUncertain, reclassifying it read.
func sc15ReadAllowed(readFile ReadFileFunc, verifiedPath, expectedDigest string, p piece) bool {
	args, ok := sc15Identity(readFile, verifiedPath, expectedDigest, p)
	if !ok {
		return false
	}
	return sc15ReadVerb(args)
}

// sc15ReadVerb reports whether the tokens after the binary name the CLI's one
// read verb, `worktree list`. A bare `worktree`, any other worktree subcommand,
// and every other verb are not reads. Trailing flags and operands do not
// matter: the verb writes nothing however it is spelled.
func sc15ReadVerb(args []string) bool {
	return len(args) >= 2 && args[0] == "worktree" && args[1] == "list"
}

// sc15VerbAllowed reports whether the tokens after the binary name one of the
// four landing verbs -- merge, push, worktree add, or worktree remove. worktree
// remove is the sanctioned standalone worktree cleanup from a primary checkout;
// it runs its own no-work-loss guard inside the CLI. resign is deliberately
// excluded.
func sc15VerbAllowed(args []string) bool {
	if len(args) == 0 {
		return false
	}
	switch args[0] {
	case "merge", "push":
		return true
	case "worktree":
		return len(args) >= 2 && (args[1] == "add" || args[1] == "remove")
	default:
		return false
	}
}

// sc15ForcesCleanup reports whether args are a cleanup-capable landing call --
// merge, or worktree remove -- carrying a --force flag. Such a call would drive
// the CLI's worktree cleanup past its own no-work-loss guard, destroying state
// on a tree the gate cannot see, so the gate declines to SANCTION it from a
// primary checkout the same way it declines a repo-retargeting flag: a forced
// cleanup must be run deliberately, not auto-sanctioned. This is the gate's
// refusal meaning of --force, kept distinct from the CLI's own --force, which
// only OVERRIDES a cleanup refusal where the gate already permits the call.
// worktree add's --force (reuse a branch, overwrite a path) is a different
// concern and stays sanctioned.
func sc15ForcesCleanup(args []string) bool {
	cleanup := false
	switch {
	case len(args) >= 1 && args[0] == "merge":
		cleanup = true
	case len(args) >= 2 && args[0] == "worktree" && args[1] == "remove":
		cleanup = true
	}
	if !cleanup {
		return false
	}
	for _, a := range args {
		if a == "--force" || strings.HasPrefix(a, "--force=") {
			return true
		}
	}
	return false
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
