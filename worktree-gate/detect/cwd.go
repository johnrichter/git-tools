package detect

import (
	"path/filepath"
	"regexp"
	"strings"
)

// cwdConnectors splits a Bash command into the segments a `cd` or a `git`
// invocation can appear in: &&, ||, ;, |, a lone & (backgrounding), and a
// literal newline. `&&` and `||` are listed ahead of their single-character
// forms so a compound operator is never split as two single ones.
var cwdConnectors = regexp.MustCompile(`&&|\|\||;|\n|\||&`)

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
// directory in force at the first git invocation in command, composed from
// every `cd` and every git `-C` preceding it at any position, with chained
// `cd`s and a quoted-literal target resolved by stripping quotes. A
// variable or otherwise unexpandable target makes the result unresolvable,
// never a silent fallback to the caller's own cwd. "" means no `cd`/`-C`
// preceded the first git invocation, so the caller's cwd governs unchanged.
//
// A command with no git invocation at all resolves the same way against
// whatever `cd`s it does contain, so a non-git write (rm, mv, an installer)
// is anchored at the directory the command actually reaches, not wherever
// the tool call happened to start.
func resolveEffectiveCWD(command string) (dir string, unresolvable bool) {
	accum := ""
	for _, seg := range cwdConnectors.Split(command, -1) {
		tokens := skipAssignments(strings.Fields(seg))
		if len(tokens) == 0 {
			continue
		}
		word := strings.TrimPrefix(tokens[0], "(")
		switch {
		case word == "cd":
			accum = applyCd(accum, tokens[1:])
		case word == "git" || strings.HasSuffix(word, "/git"):
			return asResult(applyGitOptions(accum, tokens[1:]))
		}
		// Anything else -- an unrelated command word -- carries no cwd
		// signal and is skipped; the scan keeps looking for the next `cd`
		// or `git` in a later segment.
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

// effectiveBashCWD resolves the real, absolute working directory decideBash
// should evaluate a Bash call against: resolveEffectiveCWD's result composed
// onto the session's own cwd. "" from the resolver (no cd/-C found) defers
// to sessionCWD unchanged; an absolute result overrides it outright; a
// relative result (a leading cd with no absolute anchor before it) is
// joined onto sessionCWD, the one real anchor decideBash has that the
// command-text-only resolver does not.
func effectiveBashCWD(sessionCWD, command string) (dir string, unresolvable bool) {
	resolved, denied := resolveEffectiveCWD(command)
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
