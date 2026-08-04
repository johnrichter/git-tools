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
