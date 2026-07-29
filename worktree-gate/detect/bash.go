package detect

import (
	"regexp"
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
	// for signals that aren't anchored to the start (shell redirects).
	WriteContains []string `json:"write_contains"`
}

var shellConnectors = regexp.MustCompile(`&&|\|\||;|\n|\|`)

// shellWriteMetachars are shell constructs that can carry a side-effecting
// write or a second command inside a single connector-split piece:
// redirects (`>`, `<` and their process-substitution forms), command and
// variable substitution (`$(...)`, `${...}`, backticks), and backgrounding
// (a lone `&`, which shellConnectors deliberately does not split on). The
// sanctioned-landing override rejects any command containing one, so no
// write can ride along inside an otherwise-covered git merge/commit -- the
// base classifier catches these via WriteContains, but the override
// bypasses that classification entirely.
var shellWriteMetachars = regexp.MustCompile("[<>&$`]")

// ClassifyBash splits a compound command on its shell connectors (&&, ||,
// ;, |, newlines) and classifies each piece independently: one write-like
// piece makes the whole command a write, the dominant and conservative
// outcome. Absent any write-like piece, the command is a Read only if
// every piece matches a known read pattern; a piece matching neither set
// makes the whole command Uncertain.
func ClassifyBash(v Verbs, command string) BashClass {
	sawUnknown := false
	for _, piece := range shellConnectors.Split(command, -1) {
		piece = strings.TrimSpace(piece)
		if piece == "" {
			continue
		}
		switch classifyPiece(v, piece) {
		case ClassWrite:
			return ClassWrite
		case ClassRead:
			// known-safe; keep checking the remaining pieces
		default:
			sawUnknown = true
		}
	}
	if sawUnknown {
		return ClassUncertain
	}
	return ClassRead
}

func classifyPiece(v Verbs, piece string) BashClass {
	lower := strings.ToLower(piece)
	for _, w := range v.WritePrefixes {
		if strings.HasPrefix(lower, w) {
			return ClassWrite
		}
	}
	for _, w := range v.WriteContains {
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

// hasCommandPrefix reports whether piece begins with read verb r at a word
// boundary -- piece is exactly r, or r is immediately followed by a space.
func hasCommandPrefix(piece, r string) bool {
	if !strings.HasPrefix(piece, r) {
		return false
	}
	rest := piece[len(r):]
	return rest == "" || rest[0] == ' '
}

// mergeGateVerbs are the only two commands the DAT_MERGE_GATE override may
// allow: build-with-team's documented landing-merge flow, run directly from
// the primary checkout.
var mergeGateVerbs = []string{"git merge", "git commit"}

// isSanctionedLandingCommand reports whether command is a single,
// unconnected invocation of one of mergeGateVerbs. Splitting on the same
// shell connectors ClassifyBash uses means any chained, piped, or
// newline-joined command is disqualified by carrying more than one piece;
// a subshell or an env-var-prefixed form is disqualified too, since neither
// starts with the exact verb text. Any redirect, command/variable
// substitution, or backgrounding metacharacter disqualifies it as well, so a
// write cannot ride along inside the single covered piece. A non-covered
// write verb can never ride along with a covered one.
func isSanctionedLandingCommand(command string) bool {
	if shellWriteMetachars.MatchString(command) {
		return false
	}
	var only string
	pieces := 0
	for _, piece := range shellConnectors.Split(command, -1) {
		piece = strings.TrimSpace(piece)
		if piece == "" {
			continue
		}
		pieces++
		only = piece
	}
	if pieces != 1 {
		return false
	}
	lower := strings.ToLower(only)
	for _, v := range mergeGateVerbs {
		if hasCommandPrefix(lower, v) {
			return true
		}
	}
	return false
}
