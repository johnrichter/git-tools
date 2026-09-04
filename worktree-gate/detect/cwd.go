package detect

import (
	"path/filepath"
	"regexp"
	"strings"
)

// envAssignmentToken matches a leading `VAR=value` command-segment prefix:
// a letter/underscore, then any run of characters, then `=`. Such a prefix
// is skipped without reading its name or value -- no environment or
// command-string token feeds a cwd decision.
var envAssignmentToken = regexp.MustCompile(`^[A-Za-z_].*=`)

// unresolvableCWD marks an effective directory whose value is not in the
// static command text (a variable or otherwise unexpandable `cd`/`-C`
// target). It never leaves this file: resolveEffectiveCWD and
// effectiveBashCWD both translate it to an explicit unresolvable result.
const unresolvableCWD = "\x00worktree-gate:cwd-unresolvable\x00"

// resolveEffectiveCWD implements SC-CWD-RESOLVER-CONTRACT: the working
// directory in force at the first git (or provisioned-CLI) invocation in
// command, composed from every `cd` and every git `-C` preceding it at any
// position, with chained `cd`s and a quoted-literal target resolved by
// stripping quotes. A variable or otherwise unexpandable target makes the
// result unresolvable, never a silent fallback to the caller's own cwd. ""
// means no `cd`/`-C` preceded the first such invocation, so the caller's cwd
// governs unchanged.
//
// A command with no git or provisioned-CLI invocation at all resolves the
// same way against whatever `cd`s it does contain, so a non-git write (rm,
// mv, an installer) is anchored at the directory the command actually
// reaches, not wherever the tool call happened to start.
//
// provisionedPath is the argv-supplied absolute path of the provisioned
// `git-tools` CLI (Input.ProvisionedBinPath, threaded through Decide). A
// piece's head word is recognized as that CLI only on an exact match against
// this value, the same identity test sc15Identity applies -- a bare name, a
// relative path, or an arbitrary absolute path composes nothing, same as
// today. An empty provisionedPath disables the recognition outright, so a
// call that would otherwise qualify never composes on that basis alone.
//
// It walks the same pieces the classifier does (decompose): one connector
// set and one redirect-operator predicate, so a `cd` target and a git or
// git-tools invocation are read from a piece's argv -- the command words
// with redirect operators and their targets already stripped, so `cd /a>/b`
// composes `/a`. The resolver's own leading-token tolerance stays wider than
// the classifier's on purpose (it skips a `VAR=value` prefix and a bare
// `(`), so a non-leading `cd` still composes.
func resolveEffectiveCWD(command, provisionedPath string) (dir string, unresolvable bool) {
	accum := ""
	for _, p := range decompose(command) {
		tokens := skipAssignments(strings.Fields(p.argv))
		if len(tokens) == 0 {
			continue
		}
		word := strings.TrimPrefix(tokens[0], "(")
		switch {
		case word == "cd":
			accum = applyCd(accum, tokens[1:])
		case word == "git" || strings.HasSuffix(word, "/git"):
			return asResult(applyGitOptions(accum, tokens[1:]))
		case provisionedPath != "" && stripQuotes(word) == provisionedPath:
			return asResult(applyGitToolsOptions(accum, tokens[1:]))
		}
		// Anything else -- an unrelated command word -- carries no cwd
		// signal and is skipped; the scan keeps looking for the next `cd`,
		// `git`, or provisioned-CLI invocation in a later piece.
	}
	return asResult(accum)
}

func asResult(dir string) (string, bool) {
	if dir == unresolvableCWD {
		return "", true
	}
	return dir, false
}

// skipAssignments drops leading `VAR=value` prefixes and a bare subshell
// `(` token, the same tolerance the shell resolver applies before reading a
// segment's command word.
func skipAssignments(tokens []string) []string {
	i := 0
	for i < len(tokens) {
		if tokens[i] == "(" || envAssignmentToken.MatchString(tokens[i]) {
			i++
			continue
		}
		break
	}
	return tokens[i:]
}

// applyCd composes a `cd`'s target onto accum. A missing, empty, or
// unexpandable target makes the result unresolvable.
func applyCd(accum string, args []string) string {
	if len(args) == 0 {
		return unresolvableCWD
	}
	target := stripQuotes(args[0])
	if target == "" || isUnexpandable(target) {
		return unresolvableCWD
	}
	return composeDir(accum, target)
}

// applyGitOptions walks a git invocation's global options, composing every
// `-C` onto accum, until it reaches the subcommand (or `--`, or runs out of
// options). Other global options that take a value (--git-dir, --work-tree,
// --namespace, -c) are skipped without inspection, split or glued.
//
// governance-git.md mandates `-C` as the spelling for a directory-changing
// write under gate governance -- `git -C <worktree>` for a plain git call.
// applyGitToolsOptions is this function's counterpart for the provisioned
// CLI's own invocation, which the same document mandates as
// `<git-tools-path> <verb> -C <worktree>`; the two stay in step so a call
// spelled either mandated way composes correctly.
func applyGitOptions(accum string, args []string) string {
	this := accum
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "-C":
			if i+1 >= len(args) {
				return this
			}
			this = composeC(this, args[i+1])
			i += 2
		case strings.HasPrefix(a, "-C"):
			this = composeC(this, a[len("-C"):])
			i++
		case a == "--git-dir" || a == "--work-tree" || a == "--namespace" || a == "-c":
			i++
			if i < len(args) {
				i++
			}
		case strings.HasPrefix(a, "--git-dir=") || strings.HasPrefix(a, "--work-tree=") || strings.HasPrefix(a, "--namespace=") || strings.HasPrefix(a, "-c"):
			i++
		case a == "--":
			return this
		case strings.HasPrefix(a, "-"):
			i++
		default:
			return this // reached the subcommand
		}
	}
	return this
}

// applyGitToolsOptions composes the provisioned `git-tools` CLI's own `-C`
// (split or glued) onto accum -- the spelling resolveEffectiveCWD's caller
// has already confirmed this invocation carries once its head word matched
// the exact provisioned path. Unlike applyGitOptions it does not stop at the
// first non-flag token: git-tools' persistent flags may sit on either side
// of its verb word (cobra accepts both `-C <dir> worktree add ...` and
// `worktree add -C <dir> ...`), so every token is checked in turn, and a
// later occurrence composes over an earlier one -- the same rule
// applyGitOptions applies to a repeated `-C`. Every other token, including
// the verb itself, carries no directory signal here and is skipped without
// inspection.
//
// `--repo` and `--repo=` are git-tools' other two spellings of this same
// flag (cobra registers `-C` as `--repo`'s shorthand), and D-4/R7 only
// requires `-C` to compose: `sc15Retargets` denies a non-exempt write whose
// invocation carries any of the three, on the CALLING location, precisely
// because that location cannot be trusted once retargeted. Composing
// `--repo`/`--repo=` here as well would let a retarget whose destination
// resolves to no recognized repository escape that denial by relocating the
// whole command's effective cwd there instead of leaving it at the caller's
// own primary checkout -- a second decision change D-4 does not name and no
// other requirement compensates for. `-C` alone is exercised by every
// pinned corpus control this fix adds; extending composition to the other
// two spellings needs its own accompanying fix to the named-path leg and its
// own control, not a silent side effect of this one.
func applyGitToolsOptions(accum string, args []string) string {
	this := accum
	for i := 0; i < len(args); i++ {
		switch a := args[i]; {
		case a == "-C":
			if i+1 < len(args) {
				this = composeC(this, args[i+1])
				i++
			}
		case strings.HasPrefix(a, "-C"):
			this = composeC(this, a[len("-C"):])
		}
	}
	return this
}

// composeC resolves a `-C` target (quote-stripped) onto accum; unresolvable
// on an empty or unexpandable value.
func composeC(accum, raw string) string {
	t := stripQuotes(raw)
	if t == "" || isUnexpandable(t) {
		return unresolvableCWD
	}
	return composeDir(accum, t)
}

// composeDir composes a relative target onto accum, or replaces it outright
// for an absolute target. An unresolvable accum stays unresolvable under a
// relative target -- only an absolute target can cure it.
func composeDir(accum, target string) string {
	if strings.HasPrefix(target, "/") {
		return target
	}
	switch accum {
	case unresolvableCWD:
		return unresolvableCWD
	case "":
		return target
	default:
		return accum + "/" + target
	}
}

// stripQuotes strips a single matching pair of double or single quotes from
// s -- both the first and the last character, same kind. An unbalanced
// leading quote (no matching trailing quote) is left untouched.
func stripQuotes(s string) string {
	if len(s) < 2 {
		return s
	}
	first, last := s[0], s[len(s)-1]
	if (first == '"' || first == '\'') && first == last {
		return s[1 : len(s)-1]
	}
	return s
}

// isUnexpandable reports whether s carries a variable, command
// substitution, home-dir, or glob construct whose value lives in the shell
// environment rather than in the static command text.
func isUnexpandable(s string) bool {
	if strings.HasPrefix(s, "~") {
		return true
	}
	return strings.ContainsAny(s, "$`*?[]")
}

// composeInteriorCWD resolves the working directory in force at the start of
// an interior command string (a command substitution, backtick span, or
// eval/-c payload), given the cwd already in force where that interior is
// reached: outerCWD/outerUnresolvable, exactly what scanBash was carrying at
// the piece it recursed the interior out of. It is effectiveBashCWD's own
// composition rule, reused here instead of duplicated, but with one
// difference effectiveBashCWD cannot have: an already-unresolvable outer cwd
// must stay unresolvable under a relative interior target, never silently
// default to "" as effectiveBashCWD does for a bare sessionCWD of "". This is
// SC24's own resolver: a path-blind write recursed out of an interior (see
// pathBlindWriteDenial) is judged against ITS OWN effective location, not the
// caller's outer one, so `cd <path> && git commit` inside a `$(...)` lands the
// commit's cwd check on <path> even though the outer command's own cwd never
// changed. The composed cwd also reaches SC20: a RELATIVE path named by a
// write inside the same interior (`$(cd <primary> && rm -rf build)`) now
// resolves against <primary> rather than the outer cwd.
//
// It inherits resolveEffectiveCWD's stopping rule with the rest of that
// resolver: only the `cd`s preceding the interior's FIRST git or
// provisioned-CLI invocation compose, so a `cd` after one does not move the
// result. That residual, and why it is the cwd contract's to close rather
// than this function's, is documented at pathBlindWriteDenial.
//
// provisionedPath is passed straight through to resolveEffectiveCWD, so the
// interior's own head word is recognized against the same identity as the
// outer command's.
func composeInteriorCWD(outerCWD string, outerUnresolvable bool, interior, provisionedPath string) (dir string, unresolvable bool) {
	resolved, denied := resolveEffectiveCWD(interior, provisionedPath)
	switch {
	case denied:
		return "", true
	case resolved == "":
		return outerCWD, outerUnresolvable
	case filepath.IsAbs(resolved):
		return resolved, false
	case outerUnresolvable || outerCWD == "":
		return "", true
	default:
		return filepath.Join(outerCWD, resolved), false
	}
}

// effectiveBashCWD resolves the real, absolute working directory decideBash
// should evaluate a Bash call against: resolveEffectiveCWD's result composed
// onto the session's own cwd. "" from the resolver (no cd/-C found) defers
// to sessionCWD unchanged; an absolute result overrides it outright; a
// relative result (a leading cd with no absolute anchor before it) is
// joined onto sessionCWD, the one real anchor decideBash has that the
// command-text-only resolver does not. provisionedPath is Input's own
// argv-supplied ProvisionedBinPath, threaded straight through to the
// resolver.
func effectiveBashCWD(sessionCWD, command, provisionedPath string) (dir string, unresolvable bool) {
	resolved, denied := resolveEffectiveCWD(command, provisionedPath)
	switch {
	case denied:
		return "", true
	case resolved == "":
		return sessionCWD, false
	case filepath.IsAbs(resolved):
		return resolved, false
	case sessionCWD == "":
		return resolved, false
	default:
		return filepath.Join(sessionCWD, resolved), false
	}
}
