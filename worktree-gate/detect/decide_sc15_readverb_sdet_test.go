package detect

import (
	"encoding/hex"
	"errors"
	"testing"
)

// Independent adversarial probe for the read-verb allowance
// (sc15ReadAllowed / sc15ReadVerb) reclassifying `worktree list` and
// `branch list` from ClassUncertain to ClassRead: an exact --repo-sibling
// case, both identity argv parameters empty, verb case sensitivity,
// piped/sequenced placement, and a mid-command retarget flag that must NOT
// void the read (unlike the write allowance). Every probe below runs
// against both two-token pins sc15ReadVerb admits, so neither verb drifts
// from the other's behavior under the shared identity check.

// readVerbPins are the exact two-token spellings sc15ReadVerb admits, paired
// with the same tokens wrong-cased -- a spelling the CLI itself would reject,
// so it must never read-allow either.
var readVerbPins = []struct {
	verb      string
	wrongCase string
}{
	{"worktree list", "Worktree List"},
	{"branch list", "Branch List"},
}

func TestSDET_SC15ReadVerb_RepoFlagAgainstSiblingPath_Allowed(t *testing.T) {
	// A --repo naming a real sibling repo must not void the read allowance:
	// it is not gated by the retarget rule the write allowance uses.
	for _, p := range readVerbPins {
		t.Run(p.verb, func(t *testing.T) {
			fs := newFakeFS().dir("/repo/.git").dir("/sibling/.git").file(sc15Bin, sc15BinContent)
			digest := hex.EncodeToString(sha256Sum(sc15BinContent))
			v := testVerbs(t)
			d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{
				ToolName: "Bash", CWD: "/repo", Command: sc15Bin + " " + p.verb + " --repo /sibling",
				ProvisionedBinPath: sc15Bin, ProvisionedBinDigest: digest,
			})
			if d.Deny {
				t.Fatalf("%s --repo <sibling> from a primary checkout must be ALLOWED, got deny: %s", p.verb, d.Reason)
			}
		})
	}
}

func TestSDET_SC15ReadVerb_BothArgvParamsEmpty_Denied(t *testing.T) {
	for _, p := range readVerbPins {
		t.Run(p.verb, func(t *testing.T) {
			fs := newFakeFS().dir("/repo/.git").file(sc15Bin, sc15BinContent)
			v := testVerbs(t)
			d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{
				ToolName: "Bash", CWD: "/repo", Command: sc15Bin + " " + p.verb,
				ProvisionedBinPath: "", ProvisionedBinDigest: "",
			})
			if !d.Deny {
				t.Fatalf("%s with both argv identity parameters empty must DENY", p.verb)
			}
		})
	}
}

func TestSDET_SC15ReadVerb_UppercaseSubverb_NotRecognized_Denied(t *testing.T) {
	// The verb match is exact-token, not case-folded, so a spelling the CLI
	// itself would reject is not read-allowed.
	for _, p := range readVerbPins {
		t.Run(p.verb, func(t *testing.T) {
			fs := newFakeFS().dir("/repo/.git").file(sc15Bin, sc15BinContent)
			digest := hex.EncodeToString(sha256Sum(sc15BinContent))
			v := testVerbs(t)
			d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{
				ToolName: "Bash", CWD: "/repo", Command: sc15Bin + " " + p.wrongCase,
				ProvisionedBinPath: sc15Bin, ProvisionedBinDigest: digest,
			})
			if !d.Deny {
				t.Fatalf("%q (wrong case) must not be read-allowed; expected deny", p.wrongCase)
			}
		})
	}
}

func TestSDET_SC15ReadVerb_SequencedWithWriteAfterDenies(t *testing.T) {
	// The read allowance is per-piece: a later plain write piece in the same
	// compound command must still deny the whole command.
	for _, p := range readVerbPins {
		t.Run(p.verb, func(t *testing.T) {
			fs := newFakeFS().dir("/repo/.git").file(sc15Bin, sc15BinContent)
			digest := hex.EncodeToString(sha256Sum(sc15BinContent))
			v := testVerbs(t)
			d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{
				ToolName: "Bash", CWD: "/repo",
				Command:            sc15Bin + " " + p.verb + " && git commit -m x",
				ProvisionedBinPath: sc15Bin, ProvisionedBinDigest: digest,
			})
			if !d.Deny {
				t.Fatalf("%s && git commit from a primary checkout must still DENY on the second piece", p.verb)
			}
		})
	}
}

func TestSDET_SC15ReadVerb_PipedIntoGrep_Allowed(t *testing.T) {
	// A read verb piped into an ordinary read command stays allowed: the
	// allowance must compose with the rest of the class tally.
	for _, p := range readVerbPins {
		t.Run(p.verb, func(t *testing.T) {
			fs := newFakeFS().dir("/repo/.git").file(sc15Bin, sc15BinContent)
			digest := hex.EncodeToString(sha256Sum(sc15BinContent))
			v := testVerbs(t)
			d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{
				ToolName: "Bash", CWD: "/repo",
				Command:            sc15Bin + " " + p.verb + " | grep main",
				ProvisionedBinPath: sc15Bin, ProvisionedBinDigest: digest,
			})
			if d.Deny {
				t.Fatalf("%s | grep main from a primary checkout must be ALLOWED, got deny: %s", p.verb, d.Reason)
			}
		})
	}
}

func TestSDET_SC15ReadVerb_TruncatedDigestNeverMatches_Denied(t *testing.T) {
	// A truncated (but textually valid-looking) digest must never accidentally
	// prefix-match the real one.
	for _, p := range readVerbPins {
		t.Run(p.verb, func(t *testing.T) {
			fs := newFakeFS().dir("/repo/.git").file(sc15Bin, sc15BinContent)
			fullDigest := hex.EncodeToString(sha256Sum(sc15BinContent))
			truncated := fullDigest[:len(fullDigest)-4]
			v := testVerbs(t)
			d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{
				ToolName: "Bash", CWD: "/repo", Command: sc15Bin + " " + p.verb,
				ProvisionedBinPath: sc15Bin, ProvisionedBinDigest: truncated,
			})
			if !d.Deny {
				t.Fatalf("%s: a truncated expected digest must not match; expected deny", p.verb)
			}
		})
	}
}

func TestSDET_SC15ReadVerb_UnreadableBinary_FallsClosed(t *testing.T) {
	for _, p := range readVerbPins {
		t.Run(p.verb, func(t *testing.T) {
			fs := newFakeFS().dir("/repo/.git").errAt(sc15Bin, errors.New("EACCES"))
			v := testVerbs(t)
			d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{
				ToolName: "Bash", CWD: "/repo", Command: sc15Bin + " " + p.verb,
				ProvisionedBinPath: sc15Bin, ProvisionedBinDigest: "irrelevant",
			})
			if !d.Deny {
				t.Fatalf("%s: an unreadable binary at the verified path must deny (cannot re-verify identity)", p.verb)
			}
		})
	}
}

func TestSDET_SC15ReadVerb_DoesNotWidenWriteAllowanceRetargetVoid(t *testing.T) {
	// The retarget void stays a write-channel-only rule: a write verb with a
	// --repo flag stays denied even though --repo no longer voids the read
	// allowance. The two policies must stay independent.
	fs := newFakeFS().dir("/repo/.git").dir("/sibling/.git").file(sc15Bin, sc15BinContent)
	digest := hex.EncodeToString(sha256Sum(sc15BinContent))
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{
		ToolName: "Bash", CWD: "/repo", Command: sc15Bin + " merge --repo /sibling main",
		ProvisionedBinPath: sc15Bin, ProvisionedBinDigest: digest,
	})
	if !d.Deny {
		t.Fatal("'merge --repo <sibling>' from a primary checkout must stay DENIED")
	}
}

// TestSDET_SC15ReadVerb_LeadingTokenSharedWithWriteVerbNotConfused pins that
// each read verb's leading token also names a sanctioned WRITE verb
// (worktree add/remove share `worktree`, branch delete shares `branch`), and
// that sharing a leading token never blurs the two: the write subcommand
// stays write-only and the read subcommand stays read-only regardless of
// which one a piece names.
func TestSDET_SC15ReadVerb_LeadingTokenSharedWithWriteVerbNotConfused(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"worktree-list-reads", []string{"worktree", "list"}, true},
		{"worktree-add-not-a-read", []string{"worktree", "add", "/p", "main"}, false},
		{"worktree-remove-not-a-read", []string{"worktree", "remove", "/p"}, false},
		{"branch-list-reads", []string{"branch", "list"}, true},
		{"branch-delete-not-a-read", []string{"branch", "delete", "feature"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sc15ReadVerb(c.args); got != c.want {
				t.Errorf("sc15ReadVerb(%q) = %v, want %v", c.args, got, c.want)
			}
		})
	}
}
