package detect

import (
	"strings"
)

// BashClass is the conservative classification of a Bash command against
// "does this modify a repo-tracked file" -- undecidable in general, so the
// classifier is biased toward the safe answer rather than a precise one.
type BashClass int

const (
	// ClassUncertain is the fail-closed default for a command matching
	// neither a known read nor a known write pattern.
	ClassUncertain BashClass = iota
	// ClassRead is a command confirmed to only inspect repo state.
	ClassRead
	// ClassWrite is a command that may modify tracked files.
	ClassWrite
)

// Verbs is the data-driven pattern set ClassifyBash matches against.
type Verbs struct {
	// ReadPrefixes match a trimmed, lower-cased command piece at a word
	// boundary: the piece must equal the verb or continue with a space, so a
	// write-capable command sharing an opening substring (lsof, lsyncd) is not
	// misread as a read and let through the fail-closed default.
	ReadPrefixes []string `json:"read_prefixes"`
	// WritePrefixes match a trimmed, lower-cased command piece by loose prefix
	// -- the safe direction to over-approximate, so anchoring is unnecessary.
	WritePrefixes []string `json:"write_prefixes"`
	// WriteContains match anywhere in a trimmed, lower-cased command piece,
	// for signals that aren't anchored to the start (a find mutation flag).
	// Redirect-operator entries (>, >>) are delimited by the shared redirect
	// predicate during decomposition, not matched here as substrings, so a
	// bare fd-dup (git status 2>&1) is not misread as a file write.
	WriteContains []string `json:"write_contains"`
}

// bashConnectors is the single canonical set of shell control operators that
// separate one command from the next. Both consumers of the decomposition --
// the classifier and the cwd resolver -- split on exactly this set, so a
// command breaks into the same pieces for classification and for cwd
// resolution. The set is pinned to contracts/connectors.json by test, the
// same canonical artifact the shell gate's own splitter is pinned to, so no
// copy can drift alone. Order is longest-first so a two-character operator
// (&&, ||) is never taken as two single ones (&, |).
var bashConnectors = []string{"&&", "||", "&", "|", ";", "\n"}

// maxInteriorDepth bounds SC16's recursion into nested wrappers so a
// pathologically deep or self-referential command string can never spin the
// classifier; eight levels is far beyond any real invocation.
const maxInteriorDepth = 8

// piece is one connector-delimited segment of a Bash command with its
// redirect operators already delimited. argv is the segment's command words
// with every redirect operator and its target removed -- what the classifier
// keys its verb match on and what the cwd resolver reads a cd/-C target from.
// raw is the segment verbatim, redirects and all, which SC16's interior
// extraction reads so nothing is lost to redirect stripping. writesFile is
// set when the segment opens a real (non-fd-dup) path for writing. hasRedirect
// records that any redirect operator was present, which disqualifies the
// segment from SC22's cd skip. openingRedirect narrows that to the redirects
// that open something -- a path in either direction, or a here-document --
// leaving out a bare file-descriptor duplication (2>&1, >&-) or a /dev/null
// discard, neither of which opens a repo file; SC15's landing allowance keys
// on it rather than on writesFile so the allowance never rides on
// classification semantics. heredocs holds the
// bodies of any here-documents the segment opened -- undecomposable regions
// bounded by governed-word tests.
type piece struct {
	argv            string
	raw             string
	writesFile      bool
	hasRedirect     bool
	openingRedirect bool
	heredocs        []string
}

// decompose splits a Bash command into the pieces both the classifier and the
// cwd resolver consume, using the one connector set (bashConnectors) and the
// one redirect-operator predicate (redirectOperatorAt). Connectors and
// redirects inside single/double quotes, a command substitution, a backtick
// span, or a here-document body are literal, so a quoted `;`, a `>` inside
// "a > b", or a newline inside a heredoc never splits or delimits. Empty
// segments (e.g. from `;;`) are dropped.
func decompose(command string) []piece {
	var pieces []piece
	var argv strings.Builder
	cur := piece{}
	segStart := 0

	flush := func(end int) {
		text := strings.TrimSpace(argv.String())
		if text != "" || cur.hasRedirect || len(cur.heredocs) > 0 {
			cur.argv = text
			cur.raw = strings.TrimSpace(command[segStart:end])
			pieces = append(pieces, cur)
		}
		argv.Reset()
		cur = piece{}
	}

	i, n := 0, len(command)
	atWordStart := true
	depth := 0 // command-substitution / nested-paren depth
	inBacktick, inSingle, inDouble := false, false, false
	var pendingDelims []string // heredoc delimiters whose bodies await the operator-line newline

	for i < n {
		c := command[i]

		switch {
		case inSingle:
			argv.WriteByte(c)
			if c == '\'' {
				inSingle = false
			}
			i++
			atWordStart = false
			continue
		case c == '\\':
			// Backslash escapes the next byte: both are literal.
			argv.WriteByte(c)
			i++
			if i < n {
				argv.WriteByte(command[i])
				i++
			}
			atWordStart = false
			continue
		case inDouble:
			argv.WriteByte(c)
			if c == '"' {
				inDouble = false
			}
			i++
			atWordStart = false
			continue
		case inBacktick:
			argv.WriteByte(c)
			if c == '`' {
				inBacktick = false
			}
			i++
			atWordStart = false
			continue
		case depth > 0:
			// Inside $(...): copy verbatim, tracking nested parens and quotes
			// so an inner connector never splits the outer command.
			argv.WriteByte(c)
			switch c {
			case '(':
				depth++
			case ')':
				depth--
			case '\'':
				inSingle = true
			case '"':
				inDouble = true
			case '`':
				inBacktick = true
			}
			i++
			atWordStart = false
			continue
		}

		switch c {
		case '\'':
			inSingle = true
			argv.WriteByte(c)
			i++
			atWordStart = false
			continue
		case '"':
			inDouble = true
			argv.WriteByte(c)
			i++
			atWordStart = false
			continue
		case '`':
			inBacktick = true
			argv.WriteByte(c)
			i++
			atWordStart = false
			continue
		}

		if strings.HasPrefix(command[i:], "$(") {
			depth++
			argv.WriteString("$(")
			i += 2
			atWordStart = false
			continue
		}

		// A here-document operator (<<DELIM) is a redirect: its DELIM token is
		// delimited out of argv here, and its body -- carried on the lines after
		// the operator line's newline -- is deferred and pulled at that newline
		// (below), so a command trailing the operator on the same line
		// (cat <<EOF && git commit) still decomposes and classifies.
		if delim, next, ok := heredocOperatorAt(command, i); ok {
			pendingDelims = append(pendingDelims, delim)
			cur.hasRedirect = true
			cur.openingRedirect = true
			i = next
			atWordStart = true
			continue
		}

		// The operator line's terminating newline is where the deferred heredoc
		// bodies begin. Pull each (in source order) as an undecomposable region,
		// end the line, and resume past the closing delimiter lines.
		if c == '\n' && len(pendingDelims) > 0 {
			bodies, resume := readHeredocBodies(command, i+1, pendingDelims)
			cur.heredocs = append(cur.heredocs, bodies...)
			pendingDelims = nil
			flush(i)
			i = resume
			segStart = i
			atWordStart = true
			continue
		}

		// Redirect operators bind before connectors so the `&` in `&>`/`&>>`
		// and the `|` in `>|` are never taken as a lone connector.
		if length, output, ok := redirectOperatorAt(command, i, atWordStart); ok {
			cur.hasRedirect = true
			dupCapable := strings.HasSuffix(command[i:i+length], "&")
			i += length
			for i < n && command[i] == ' ' {
				i++
			}
			target, next := readTarget(command, i)
			i = next
			if target != "" && !isFdDupOrDiscardTarget(target, dupCapable) {
				cur.openingRedirect = true
				if output {
					cur.writesFile = true
				}
			}
			argv.WriteByte(' ')
			atWordStart = true
			continue
		}

		if length, ok := connectorAt(command[i:]); ok {
			flush(i)
			i += length
			segStart = i
			atWordStart = true
			continue
		}

		argv.WriteByte(c)
		atWordStart = c == ' '
		i++
	}
	flush(n)
	return pieces
}

// connectorAt reports the length of the connector beginning at s, matching
// bashConnectors longest-first.
func connectorAt(s string) (int, bool) {
	for _, c := range bashConnectors {
		if strings.HasPrefix(s, c) {
			return len(c), true
		}
	}
	return 0, false
}

// redirectOperatorAt reports whether a shell redirect operator begins at s[i],
// matching the operator forms longest-first. output distinguishes a
// file-writing redirect (>, >>, >|, >&, &>, &>>, and their N-prefixed forms)
// from an input redirect (<, <&). A leading fd number (2>, 10>>) is only part
// of the operator at a word boundary, so `echo x2>f` still redirects on the
// bare `>`.
func redirectOperatorAt(s string, i int, atWordStart bool) (length int, output, ok bool) {
	j := i
	if atWordStart {
		for j < len(s) && s[j] >= '0' && s[j] <= '9' {
			j++
		}
	}
	numeric := j > i
	rest := s[j:]
	if !numeric {
		switch {
		case strings.HasPrefix(rest, "&>>"):
			return (j - i) + 3, true, true
		case strings.HasPrefix(rest, "&>"):
			return (j - i) + 2, true, true
		}
	}
	switch {
	case strings.HasPrefix(rest, ">>"):
		return (j - i) + 2, true, true
	case strings.HasPrefix(rest, ">|"):
		return (j - i) + 2, true, true
	case strings.HasPrefix(rest, ">&"):
		return (j - i) + 2, true, true
	case strings.HasPrefix(rest, "<&"):
		return (j - i) + 2, false, true
	case strings.HasPrefix(rest, ">"):
		return (j - i) + 1, true, true
	case strings.HasPrefix(rest, "<"):
		return (j - i) + 1, false, true
	}
	return 0, false, false
}

// readTarget reads the redirect target word beginning at s[i], honoring a
// leading quote so a spaced quoted path is not truncated, and otherwise
// stopping at whitespace, a connector, or the next redirect operator.
func readTarget(s string, i int) (target string, next int) {
	n := len(s)
	if i < n && (s[i] == '"' || s[i] == '\'') {
		q := s[i]
		j := i + 1
		for j < n && s[j] != q {
			j++
		}
		if j < n {
			j++ // include the closing quote
		}
		return s[i:j], j
	}
	start := i
	for i < n && !targetEnds(s, i) {
		i++
	}
	return s[start:i], i
}

// targetEnds reports whether the redirect target word ends at s[i].
func targetEnds(s string, i int) bool {
	if s[i] == ' ' {
		return true
	}
	if _, ok := connectorAt(s[i:]); ok {
		return true
	}
	_, _, isOp := redirectOperatorAt(s, i, false)
	return isOp
}

// isFdDupTarget reports whether word is a file-descriptor duplication target
// (a bare number, &N, or -) rather than a path -- such a redirect writes no
// file and so is not a write signal.
func isFdDupTarget(word string) bool {
	if word == "-" {
		return true
	}
	s := strings.TrimPrefix(word, "&")
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// isFdDupOrDiscardTarget reports whether a redirect naming target opens no
// repo file, so it carries no write signal regardless of direction: either
// /dev/null, the canonical discard device, exempt under any operator (A1 --
// a discard is never a repo write), or a genuine file-descriptor duplication
// (isFdDupTarget), admitted only when the matched operator itself is
// dup-capable -- ends in "&" (>&, <&, and their numeric-fd-prefixed forms
// like 2>&1) -- the one shell form where a bare digit or "-" dups a
// descriptor rather than naming a file. Under a plain operator (>, >>, <,
// &>, &>>) the same bare-digit or "-" spelling is the literal file bash
// opens, so `tool run >1` writes a file called "1"; it does not duplicate a
// descriptor (A2, reversing the prior deferral).
func isFdDupOrDiscardTarget(target string, dupCapable bool) bool {
	if target == "/dev/null" {
		return true
	}
	return dupCapable && isFdDupTarget(target)
}

// heredocOperatorAt reports whether a here-document operator (<<DELIM or
// <<-DELIM, but not the <<< here-string) begins at s[i], returning the
// quote-stripped delimiter word and the index past it. The body is not read
// here -- it is pulled later, at the operator line's newline -- so any command
// trailing the operator on the same line stays in the decomposition stream.
func heredocOperatorAt(s string, i int) (delim string, next int, ok bool) {
	if !strings.HasPrefix(s[i:], "<<") || strings.HasPrefix(s[i:], "<<<") {
		return "", 0, false
	}
	n := len(s)
	j := i + 2
	if j < n && s[j] == '-' {
		j++
	}
	for j < n && s[j] == ' ' {
		j++
	}
	start := j
	for j < n && s[j] != ' ' && s[j] != '\n' {
		if _, isConn := connectorAt(s[j:]); isConn {
			break
		}
		j++
	}
	delim = stripQuotes(s[start:j])
	if delim == "" {
		return "", 0, false
	}
	return delim, j, true
}

// readHeredocBodies pulls the bodies of the pending here-document delimiters,
// in source order, from the lines beginning at s[start]. Each body runs to its
// own closing delimiter line; an unterminated heredoc takes the rest of the
// string. resume is the index past the last delimiter line consumed.
func readHeredocBodies(s string, start int, delims []string) (bodies []string, resume int) {
	pos, n := start, len(s)
	for _, delim := range delims {
		bodyStart := pos
		for {
			nl := strings.IndexByte(s[pos:], '\n')
			if nl < 0 {
				if strings.TrimSpace(s[pos:]) == delim {
					bodies = append(bodies, s[bodyStart:pos])
				} else {
					bodies = append(bodies, s[bodyStart:])
				}
				pos = n
				break
			}
			lineEnd := pos + nl
			if strings.TrimSpace(s[pos:lineEnd]) == delim {
				bodies = append(bodies, s[bodyStart:pos])
				pos = lineEnd + 1
				break
			}
			pos = lineEnd + 1
		}
	}
	return bodies, pos
}

// ClassifyBash splits a compound command into its pieces (shared decomposition)
// and classifies each independently: a write-like piece makes the whole
// command a write, the dominant and conservative outcome. Absent any write, the
// command is a Read only if every piece is a known read; a piece matching
// neither set makes the command Uncertain. Under SC16, the interior of a
// wrapper (command substitution, backtick, subshell, group, eval, bash -c/sh -c
// string, xargs argv, or here-document body) is classified as an additional
// piece and the strictest verdict wins, so a wrapper reaches the same verdict
// as the bare command it smuggles.
func ClassifyBash(v Verbs, command string) BashClass {
	return classifyBash(v, command, 0)
}

func classifyBash(v Verbs, command string, depth int) BashClass {
	worst := ClassRead
	for _, p := range decompose(command) {
		worst = stricter(worst, classifyPiece(v, p))
		if worst == ClassWrite {
			return ClassWrite
		}
		if depth >= maxInteriorDepth {
			continue
		}
		for _, interior := range decomposableInteriors(p.raw) {
			worst = stricter(worst, classifyBash(v, interior, depth+1))
			if worst == ClassWrite {
				return ClassWrite
			}
		}
		for _, body := range p.heredocs {
			worst = stricter(worst, boundedVerdict(v, body))
			if worst == ClassWrite {
				return ClassWrite
			}
		}
	}
	return worst
}

func classifyPiece(v Verbs, p piece) BashClass {
	if p.writesFile {
		return ClassWrite
	}
	if eligibleCdSkip(p) {
		// SC22: a piece whose whole argv is `cd <literal-target>` writes
		// nothing -- its target is consumed and range-checked by the cwd
		// resolver -- so it is skipped rather than left to fall to the
		// fail-closed Uncertain default that a bare `cd` would otherwise hit.
		return ClassRead
	}
	stripped := stripGroupOpeners(p.argv)
	if toks := shellTokens(stripped); len(toks) > 0 {
		switch commandWord(toks[0]) {
		case "git":
			// Every `git …` verb is classified from the in-code subcommand sets
			// (see classifyGit), not the verbs.json prefixes, so a leading global
			// option never defeats the match and merge-base stays a read while
			// merge is a write.
			return classifyGit(toks[1:])
		case "git-tools":
			// Every `git-tools …` invocation is a write by default, whatever its
			// verb: the provisioned CLI names no read class here, so a
			// differently planted binary of the same name can never earn one for
			// free. Its two real read verbs (worktree list, branch list) reach
			// ClassRead only through sc15ReadAllowed's independently
			// digest-verified check.
			return ClassWrite
		}
	}
	lower := strings.ToLower(stripped)
	for _, w := range v.WritePrefixes {
		if strings.HasPrefix(lower, w) {
			return ClassWrite
		}
	}
	for _, w := range v.WriteContains {
		if isRedirectOperatorString(w) {
			continue
		}
		if strings.Contains(lower, w) {
			return ClassWrite
		}
	}
	for _, r := range v.ReadPrefixes {
		if hasCommandPrefix(lower, r) {
			return ClassRead
		}
	}
	return ClassUncertain
}

// gitToolsVerbShapes names the provisioned CLI's verb shapes a denial can
// recognize by name -- plain data, consulted only to name a matching verb
// inside a more specific deny message (see gitToolsVerbShape in decide.go).
// It plays no part in classification: classifyPiece's git-tools default
// above is unconditional, so this list can never turn into a read set by
// accident.
var gitToolsVerbShapes = []string{
	"merge", "push", "rebase", "resign", "sign",
	"branch create", "branch delete",
	"worktree add", "worktree remove",
	"tag create", "hooks install",
}

// eligibleCdSkip reports whether p is exactly `cd <literal-target>` and nothing
// else -- SC22's skip fires only here. A redirect, a substitution or backtick,
// a glob, a variable, or any token beyond the single target disqualifies it, so
// no write signal a fuller piece would carry is dropped.
func eligibleCdSkip(p piece) bool {
	if p.hasRedirect || len(p.heredocs) > 0 {
		return false
	}
	toks := strings.Fields(p.argv)
	if len(toks) != 2 || toks[0] != "cd" {
		return false
	}
	return !strings.ContainsAny(toks[1], "*?[]$`")
}

// stripGroupOpeners drops a leading subshell `(` or group `{ ` token so the
// command word inside a `( … )` or `{ …; }` wrapper is matched directly.
func stripGroupOpeners(argv string) string {
	for {
		t := strings.TrimSpace(argv)
		switch {
		case strings.HasPrefix(t, "("):
			argv = t[1:]
		case t == "{" || strings.HasPrefix(t, "{ "):
			argv = strings.TrimPrefix(t, "{")
		default:
			return t
		}
	}
}

// isRedirectOperatorString reports whether w is wholly a redirect operator, so
// a WriteContains entry that is one (>, >>) is left to the shared redirect
// predicate instead of being matched as a bare substring.
func isRedirectOperatorString(w string) bool {
	length, _, ok := redirectOperatorAt(w, 0, true)
	return ok && length == len(w)
}

// stricter returns the stricter of two verdicts: a write dominates an
// uncertain, which dominates a read.
func stricter(a, b BashClass) BashClass {
	if rank(a) >= rank(b) {
		return a
	}
	return b
}

func rank(c BashClass) int {
	switch c {
	case ClassWrite:
		return 2
	case ClassUncertain:
		return 1
	default:
		return 0
	}
}

// boundedVerdict classifies an undecomposable region (a here-document body): a
// write iff its raw text names a governed command word, otherwise a read, so
// the region falls through to D6 leniency rather than a blanket deny.
func boundedVerdict(v Verbs, region string) BashClass {
	if containsGovernedWord(v, region) {
		return ClassWrite
	}
	return ClassRead
}

// containsGovernedWord reports whether region names git, the provisioned CLI,
// or any write-class verb at a word boundary.
func containsGovernedWord(v Verbs, region string) bool {
	lower := strings.ToLower(region)
	if hasWord(lower, "git") {
		return true
	}
	for _, w := range v.WritePrefixes {
		if verb := firstToken(w); verb != "" && hasWord(lower, verb) {
			return true
		}
	}
	return false
}

// hasCommandPrefix reports whether piece begins with read verb r at a word
// boundary -- piece is exactly r, or r is immediately followed by a space.
func hasCommandPrefix(piece, r string) bool {
	if !strings.HasPrefix(piece, r) {
		return false
	}
	rest := piece[len(r):]
	return rest == "" || rest[0] == ' '
}

// hasWord reports whether word appears in s bounded by non-word characters on
// both sides, so `git` matches in `git-tools` and `/usr/bin/git` but not in
// `legitimate`.
func hasWord(s, word string) bool {
	for from := 0; ; {
		idx := strings.Index(s[from:], word)
		if idx < 0 {
			return false
		}
		start := from + idx
		if wordBoundary(s, start-1) && wordBoundary(s, start+len(word)) {
			return true
		}
		from = start + 1
	}
}

func wordBoundary(s string, i int) bool {
	if i < 0 || i >= len(s) {
		return true
	}
	return !isWordByte(s[i])
}

func isWordByte(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_'
}

func firstToken(s string) string {
	f := strings.Fields(s)
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

// decomposableInteriors returns the nested command strings SC16 re-classifies
// as additional pieces: the interior of every command substitution and
// backtick span, the argument of an eval or a shell's -c, and the argv an
// xargs runs. Each is fed back through the same classifier.
func decomposableInteriors(raw string) []string {
	var out []string
	out = append(out, commandSubstitutions(raw)...)
	out = append(out, evalAndShellCArgs(raw)...)
	out = append(out, xargsArgv(raw)...)
	return out
}

// commandSubstitutions returns the interior of every top-level $(...) and
// backtick span in raw. A single-quoted span suppresses both; a double-quoted
// span suppresses neither, matching the shell. Nested $(...) is captured whole
// and re-split when its interior is recursed.
func commandSubstitutions(raw string) []string {
	var out []string
	inSingle, inDouble := false, false
	i, n := 0, len(raw)
	for i < n {
		c := raw[i]
		switch {
		case c == '\\' && !inSingle:
			i += 2
			continue
		case inSingle:
			if c == '\'' {
				inSingle = false
			}
		case c == '\'' && !inDouble:
			inSingle = true
		case c == '"':
			inDouble = !inDouble
		case c == '`':
			end := strings.IndexByte(raw[i+1:], '`')
			if end < 0 {
				return out
			}
			out = append(out, raw[i+1:i+1+end])
			i += end + 2
			continue
		case c == '$' && i+1 < n && raw[i+1] == '(':
			depth, j := 1, i+2
			for j < n && depth > 0 {
				switch raw[j] {
				case '(':
					depth++
				case ')':
					depth--
				}
				if depth == 0 {
					break
				}
				j++
			}
			if depth != 0 {
				return out
			}
			out = append(out, raw[i+2:j])
			i = j + 1
			continue
		}
		i++
	}
	return out
}

// evalAndShellCArgs returns the command string an eval or a shell's -c runs.
func evalAndShellCArgs(raw string) []string {
	toks := shellTokens(raw)
	var out []string
	for i := 0; i < len(toks); i++ {
		bare := stripQuotes(toks[i])
		switch {
		case bare == "eval":
			var parts []string
			for _, t := range toks[i+1:] {
				parts = append(parts, stripQuotes(t))
			}
			if len(parts) > 0 {
				out = append(out, strings.Join(parts, " "))
			}
		case isShellName(bare):
			if arg, ok := dashCArg(toks[i+1:]); ok {
				out = append(out, arg)
			}
		}
	}
	return out
}

// xargsArgv returns the command argv an xargs invocation runs, skipping xargs's
// own option flags.
func xargsArgv(raw string) []string {
	toks := shellTokens(raw)
	var out []string
	for i := 0; i < len(toks); i++ {
		if stripQuotes(toks[i]) != "xargs" {
			continue
		}
		j := i + 1
		for j < len(toks) && strings.HasPrefix(stripQuotes(toks[j]), "-") {
			j++
		}
		if j >= len(toks) {
			continue
		}
		var parts []string
		for _, t := range toks[j:] {
			parts = append(parts, stripQuotes(t))
		}
		out = append(out, strings.Join(parts, " "))
	}
	return out
}

// dashCArg returns the value of the first -c option in args (separated or
// glued), the command string a shell -c executes.
func dashCArg(args []string) (string, bool) {
	for i := 0; i < len(args); i++ {
		bare := stripQuotes(args[i])
		if bare == "-c" {
			if i+1 < len(args) {
				return stripQuotes(args[i+1]), true
			}
			return "", false
		}
		if strings.HasPrefix(bare, "-c") && len(bare) > 2 {
			return stripQuotes(bare[2:]), true
		}
	}
	return "", false
}

func isShellName(w string) bool {
	switch w {
	case "sh", "bash", "dash", "zsh", "ksh":
		return true
	}
	for _, s := range []string{"/sh", "/bash", "/dash", "/zsh", "/ksh"} {
		if strings.HasSuffix(w, s) {
			return true
		}
	}
	return false
}

// shellTokens splits s into whitespace-separated words, keeping a quoted span
// (and its quotes) as one word so an embedded space or operator does not break
// a token. It backs eval/-c/xargs argument extraction, not the connector
// decomposition.
func shellTokens(s string) []string {
	var toks []string
	var b strings.Builder
	inSingle, inDouble := false, false
	flush := func() {
		if b.Len() > 0 {
			toks = append(toks, b.String())
			b.Reset()
		}
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\\' && !inSingle:
			b.WriteByte(c)
			if i+1 < len(s) {
				i++
				b.WriteByte(s[i])
			}
		case inSingle:
			b.WriteByte(c)
			if c == '\'' {
				inSingle = false
			}
		case c == '\'' && !inDouble:
			inSingle = true
			b.WriteByte(c)
		case inDouble:
			b.WriteByte(c)
			if c == '"' {
				inDouble = false
			}
		case c == '"':
			inDouble = true
			b.WriteByte(c)
		case c == ' ' || c == '\t' || c == '\n':
			flush()
		default:
			b.WriteByte(c)
		}
	}
	flush()
	return toks
}
