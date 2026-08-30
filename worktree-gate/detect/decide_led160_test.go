package detect

import (
	"path/filepath"
	"syscall"
	"testing"
)

// led160Note is the shape LED-160 was filed against twice: a long free-text
// `--note` value on a helper call this package cannot classify. Its length is
// load-bearing -- past NAME_MAX a real filesystem answers ENAMETOOLONG for
// this text taken as a path, which is what makes a wrongly-resolved operand
// deny outright instead of passing unnoticed.
// nameMax is Linux's single-component path limit, the threshold past which a
// path built out of free text stops resolving at all.
const nameMax = 255

const led160Note = "gate result: plan acceptance verdict for this task -- the reviewer confirmed every acceptance criterion, re-ran the full suite, and recorded no residual risk beyond the disclosed fail-closed denial on an unresolved redirect target; see the ledger entry for the full rationale and the follow-up owner"

// LED-160: a long free-text operand of an unmodeled command is not resolved as
// a filesystem path. namedPaths' unmodeled-command rule (LED-023) judges only
// the redirect targets of a command it cannot name, so a helper call made
// write-class by its own shell redirect never has its `--note` value joined
// onto the cwd and lstat'd. Judging that operand instead lands the join on a
// path past NAME_MAX, lstat answers ENAMETOOLONG, and namedPathDenial fails
// closed on an indeterminate target -- a length-dependent denial of an
// otherwise identical call, which is how the ticket was reported, twice.
// LED-023's own cases cannot pin this: their operands resolve inside the
// worktree and are allowed either way, so only a target long enough to raise
// the errno separates the two behaviors.
//
// Both lengths run from a WORKTREE cwd, the only cwd where SC20's named-path
// rule decides the verdict; a primary-checkout cwd denies either note on the
// cwd leg and would hide the difference entirely.
func TestLED160_LongFreeTextOperandIsNotResolvedAsAPath(t *testing.T) {
	for _, tc := range []struct {
		name string
		note string
	}{
		{"long note past NAME_MAX", led160Note},
		{"short note", "ok"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ffs := led023FS()
			// The fake filesystem models path contents, not length limits, so
			// the errno a real one raises is registered for exactly the target
			// whose final component exceeds NAME_MAX -- registering it for the
			// short note too would erase the length contrast this test rests on.
			if len(tc.note) > nameMax {
				ffs.errAt(filepath.Join("/repo/wt", tc.note, ".git"), syscall.ENAMETOOLONG)
			}

			d := Decide(ffs.lstat, ffs.readFile, nil, testVerbs(t), nil, Input{
				ToolName: "Bash", CWD: "/repo/wt",
				Command: `dat-tools log-note /repo/wt/state.json --note "` + tc.note + `" > /repo/wt/out.txt`,
			})
			if d.Deny {
				t.Fatalf("expected allow: the note text is a flag value, not a write target, got deny: %s", d.Reason)
			}
		})
	}
}
