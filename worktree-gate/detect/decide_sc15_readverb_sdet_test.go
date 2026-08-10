package detect

import (
	"encoding/hex"
	"errors"
	"testing"
)

// Independent adversarial probe for the read-verb allowance
// (sc15ReadAllowed / sc15ReadVerb) reclassifying `worktree list` from
// ClassUncertain to ClassRead: an exact --repo-sibling case, both identity
// argv parameters empty, verb case sensitivity, piped/sequenced placement,
// and a mid-command retarget flag that must NOT void the read (unlike the
// write allowance).

func TestSDET_SC15ReadVerb_RepoFlagAgainstSiblingPath_Allowed(t *testing.T) {
	// A --repo naming a real sibling repo must not void the read allowance:
	// it is not gated by the retarget rule the write allowance uses.
	fs := newFakeFS().dir("/repo/.git").dir("/sibling/.git").file(sc15Bin, sc15BinContent)
	digest := hex.EncodeToString(sha256Sum(sc15BinContent))
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, v, nil, TrackingDocs{}, nil, Input{
		ToolName: "Bash", CWD: "/repo", Command: sc15Bin + " worktree list --repo /sibling",
		ProvisionedBinPath: sc15Bin, ProvisionedBinDigest: digest,
	})
	if d.Deny {
		t.Fatalf("worktree list --repo <sibling> from a primary checkout must be ALLOWED, got deny: %s", d.Reason)
	}
}

func TestSDET_SC15ReadVerb_BothArgvParamsEmpty_Denied(t *testing.T) {
	fs := newFakeFS().dir("/repo/.git").file(sc15Bin, sc15BinContent)
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, v, nil, TrackingDocs{}, nil, Input{
		ToolName: "Bash", CWD: "/repo", Command: sc15Bin + " worktree list",
		ProvisionedBinPath: "", ProvisionedBinDigest: "",
	})
	if !d.Deny {
		t.Fatal("worktree list with both argv identity parameters empty must DENY")
	}
}

func TestSDET_SC15ReadVerb_UppercaseSubverb_NotRecognized_Denied(t *testing.T) {
	// The verb match is exact-token, not case-folded, so a spelling the CLI
	// itself would reject is not read-allowed.
	fs := newFakeFS().dir("/repo/.git").file(sc15Bin, sc15BinContent)
	digest := hex.EncodeToString(sha256Sum(sc15BinContent))
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, v, nil, TrackingDocs{}, nil, Input{
		ToolName: "Bash", CWD: "/repo", Command: sc15Bin + " Worktree List",
		ProvisionedBinPath: sc15Bin, ProvisionedBinDigest: digest,
	})
	if !d.Deny {
		t.Fatal("'Worktree List' (wrong case) must not be read-allowed; expected deny")
	}
}

func TestSDET_SC15ReadVerb_SequencedWithWriteAfterDenies(t *testing.T) {
	// The read allowance is per-piece: a later plain write piece in the same
	// compound command must still deny the whole command.
	fs := newFakeFS().dir("/repo/.git").file(sc15Bin, sc15BinContent)
	digest := hex.EncodeToString(sha256Sum(sc15BinContent))
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, v, nil, TrackingDocs{}, nil, Input{
		ToolName: "Bash", CWD: "/repo",
		Command:            sc15Bin + " worktree list && git commit -m x",
		ProvisionedBinPath: sc15Bin, ProvisionedBinDigest: digest,
	})
	if !d.Deny {
		t.Fatal("worktree list && git commit from a primary checkout must still DENY on the second piece")
	}
}

func TestSDET_SC15ReadVerb_PipedIntoGrep_Allowed(t *testing.T) {
	// A read verb piped into an ordinary read command stays allowed: the
	// allowance must compose with the rest of the class tally.
	fs := newFakeFS().dir("/repo/.git").file(sc15Bin, sc15BinContent)
	digest := hex.EncodeToString(sha256Sum(sc15BinContent))
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, v, nil, TrackingDocs{}, nil, Input{
		ToolName: "Bash", CWD: "/repo",
		Command:            sc15Bin + " worktree list | grep main",
		ProvisionedBinPath: sc15Bin, ProvisionedBinDigest: digest,
	})
	if d.Deny {
		t.Fatalf("worktree list | grep main from a primary checkout must be ALLOWED, got deny: %s", d.Reason)
	}
}

func TestSDET_SC15ReadVerb_TruncatedDigestNeverMatches_Denied(t *testing.T) {
	// A truncated (but textually valid-looking) digest must never accidentally
	// prefix-match the real one.
	fs := newFakeFS().dir("/repo/.git").file(sc15Bin, sc15BinContent)
	fullDigest := hex.EncodeToString(sha256Sum(sc15BinContent))
	truncated := fullDigest[:len(fullDigest)-4]
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, v, nil, TrackingDocs{}, nil, Input{
		ToolName: "Bash", CWD: "/repo", Command: sc15Bin + " worktree list",
		ProvisionedBinPath: sc15Bin, ProvisionedBinDigest: truncated,
	})
	if !d.Deny {
		t.Fatal("a truncated expected digest must not match; expected deny")
	}
}

func TestSDET_SC15ReadVerb_UnreadableBinary_FallsClosed(t *testing.T) {
	fs := newFakeFS().dir("/repo/.git").errAt(sc15Bin, errors.New("EACCES"))
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, v, nil, TrackingDocs{}, nil, Input{
		ToolName: "Bash", CWD: "/repo", Command: sc15Bin + " worktree list",
		ProvisionedBinPath: sc15Bin, ProvisionedBinDigest: "irrelevant",
	})
	if !d.Deny {
		t.Fatal("an unreadable binary at the verified path must deny (cannot re-verify identity)")
	}
}

func TestSDET_SC15ReadVerb_DoesNotWidenWriteAllowanceRetargetVoid(t *testing.T) {
	// The retarget void stays a write-channel-only rule: a write verb with a
	// --repo flag stays denied even though --repo no longer voids the read
	// allowance. The two policies must stay independent.
	fs := newFakeFS().dir("/repo/.git").dir("/sibling/.git").file(sc15Bin, sc15BinContent)
	digest := hex.EncodeToString(sha256Sum(sc15BinContent))
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, v, nil, TrackingDocs{}, nil, Input{
		ToolName: "Bash", CWD: "/repo", Command: sc15Bin + " merge --repo /sibling main",
		ProvisionedBinPath: sc15Bin, ProvisionedBinDigest: digest,
	})
	if !d.Deny {
		t.Fatal("'merge --repo <sibling>' from a primary checkout must stay DENIED")
	}
}
