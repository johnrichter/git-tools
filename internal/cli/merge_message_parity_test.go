package cli

import (
	"errors"
	"fmt"
	"testing"
)

// The signing gate moved to internal/signing, which now emits raw, unsanitized
// refusal messages; sanitizeMessage runs once here at the finish* boundary
// (finishDiagnostic). Before the move, the git-failure paths sanitized their
// message inside the gate too, so those were sanitized twice. This test proves
// the emitted message is byte-identical either way — i.e. dropping the inner
// sanitize changed no output — for every refusal path, including messages that
// carry the control characters an underlying git error can contain.
//
// Each case reproduces the exact fmt.Sprintf template the gate uses. innerSanitized
// records whether the pre-extraction gate wrapped that message in sanitizeMessage
// before returning it; the two argument-check paths never did.
func TestSigningRefusal_EmittedMessageMatchesPreExtraction(t *testing.T) {
	const (
		source = "feature"
		target = "main"
		base   = "abc1234"
		dir    = "/tmp/repo"
	)
	// A git error and a signing detail carrying newlines, tabs and other control
	// characters, so sanitizeMessage has real work to do on the sanitized paths.
	gitErr := errors.New("fatal: loose object\n\tcorrupt\x00 tail\x7f  trailing   ")
	detail := "gpg failed to sign the data\nno secret key available\x7f"

	cases := []struct {
		name           string
		raw            string
		innerSanitized bool
	}{
		{"refExists_failed", fmt.Sprintf("could not check whether %s is a local branch: %v", source, gitErr), true},
		{"source_not_branch", fmt.Sprintf("%q is not a local branch, so the signing gate cannot re-sign what the merge would land", source), false},
		{"merge_base_failed", fmt.Sprintf("could not compute the fork point of %s with %s: %v", source, target, gitErr), true},
		{"no_fork_point", fmt.Sprintf("%s and %s share no common ancestor, so there is no range for the signing gate to sign", source, target), false},
		{"range_states_failed", fmt.Sprintf("could not read signature status over %s..%s: %v", base, source, gitErr), true},
		{"signing_probe_failed", fmt.Sprintf("could not test whether git can sign in %s: %v", dir, gitErr), true},
		{"key_unresolved", fmt.Sprintf("no key resolved for commit signing, so merging %s would land unsigned commits: %s", source, detail), true},
		{"resign_dryrun_failed", fmt.Sprintf("re-signing %s..%s could not be computed: %v", base, source, gitErr), true},
		{"resign_apply_failed", fmt.Sprintf("re-signing %s..%s failed: %v", base, source, gitErr), true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Pre-extraction: the gate stored either the raw message or a
			// sanitized one, and finishDiagnostic sanitized whatever it stored.
			preField := tc.raw
			if tc.innerSanitized {
				preField = sanitizeMessage(tc.raw)
			}
			pre := sanitizeMessage(preField)
			// Post-extraction: the gate stores the raw message; finishDiagnostic
			// sanitizes it once.
			post := sanitizeMessage(tc.raw)
			if post != pre {
				t.Errorf("emitted message diverged from pre-extraction value\n pre: %q\npost: %q", pre, post)
			}
			// Guard against a vacuous test on the control-char paths: sanitizing
			// must actually change those raw messages.
			if tc.innerSanitized && post == tc.raw {
				t.Errorf("sanitizeMessage was a no-op on a control-char message: %q", tc.raw)
			}
		})
	}
}
