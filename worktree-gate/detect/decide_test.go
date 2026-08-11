package detect

import (
	"encoding/hex"
	"errors"
	"testing"
)

func primaryFS() *fakeFS { return newFakeFS().dir("/repo/.git") }
func worktreeFS() *fakeFS {
	return newFakeFS().file("/repo/wt/.git", "gitdir: /repo/.git/worktrees/wt\n")
}

func TestDecide_WriteOutsideWorktree_Denied(t *testing.T) {
	fs := primaryFS()
	d := Decide(fs.lstat, fs.readFile, Verbs{}, nil, TrackingDocs{}, nil, Input{ToolName: "Write", FilePath: "/repo/a.go"})
	if !d.Deny {
		t.Fatal("expected deny for a Write into the primary checkout")
	}
}

func TestDecide_WriteInsideWorktree_Allowed(t *testing.T) {
	fs := worktreeFS()
	d := Decide(fs.lstat, fs.readFile, Verbs{}, nil, TrackingDocs{}, nil, Input{ToolName: "Write", FilePath: "/repo/wt/a.go"})
	if d.Deny {
		t.Fatalf("expected allow for a Write inside a worktree, got deny: %s", d.Reason)
	}
}

func TestDecide_WriteOutsideAnyRepo_Allowed(t *testing.T) {
	fs := newFakeFS()
	d := Decide(fs.lstat, fs.readFile, Verbs{}, nil, TrackingDocs{}, nil, Input{ToolName: "Write", FilePath: "/tmp/scratch.txt"})
	if d.Deny {
		t.Fatalf("expected allow for a write confidently outside any repo, got deny: %s", d.Reason)
	}
}

func TestDecide_Bash_ReadInPrimaryCheckout_Allowed(t *testing.T) {
	fs := primaryFS()
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, v, nil, TrackingDocs{}, nil, Input{ToolName: "Bash", CWD: "/repo", Command: "git status"})
	if d.Deny {
		t.Fatalf("expected allow for a read in the primary checkout, got deny: %s", d.Reason)
	}
}

func TestDecide_Bash_WriteInPrimaryCheckout_Denied(t *testing.T) {
	fs := primaryFS()
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, v, nil, TrackingDocs{}, nil, Input{ToolName: "Bash", CWD: "/repo", Command: "git commit -m x"})
	if !d.Deny {
		t.Fatal("expected deny for a write-classified Bash command in the primary checkout")
	}
}

func TestDecide_Bash_WriteInWorktree_Allowed(t *testing.T) {
	fs := worktreeFS()
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, v, nil, TrackingDocs{}, nil, Input{ToolName: "Bash", CWD: "/repo/wt", Command: "git commit -m x"})
	if d.Deny {
		t.Fatalf("expected allow for a write in an already-isolated worktree, got deny: %s", d.Reason)
	}
}

func TestDecide_Bash_UncertainInPrimaryCheckout_Denied(t *testing.T) {
	fs := primaryFS()
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, v, nil, TrackingDocs{}, nil, Input{ToolName: "Bash", CWD: "/repo", Command: "some-unlisted-tool"})
	if !d.Deny {
		t.Fatal("expected deny for an unclassifiable command in the primary checkout (fail closed on uncertainty)")
	}
}

func TestDecide_Bash_OutsideAnyRepo_Allowed(t *testing.T) {
	fs := newFakeFS()
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, v, nil, TrackingDocs{}, nil, Input{ToolName: "Bash", CWD: "/tmp", Command: "git commit -m x"})
	if d.Deny {
		t.Fatalf("expected allow for a Bash call confidently outside any repo, got deny: %s", d.Reason)
	}
}

func TestDecide_Bash_NoCWD_DeniedFailClosed(t *testing.T) {
	fs := primaryFS()
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, v, nil, TrackingDocs{}, nil, Input{ToolName: "Bash", Command: "git status"})
	if !d.Deny {
		t.Fatal("expected deny when no working directory is reported (fail closed on uncertainty)")
	}
}

func TestDecide_Bash_IndeterminateRepoMembership_DeniedFailClosed(t *testing.T) {
	fs := newFakeFS().errAt("/repo/.git", errors.New("permission denied"))
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, v, nil, TrackingDocs{}, nil, Input{ToolName: "Bash", CWD: "/repo", Command: "git status"})
	if !d.Deny {
		t.Fatal("expected deny when repo membership can't be resolved (fail closed on uncertainty)")
	}
}

func TestDecide_Bash_DegradedClassifierInPrimaryCheckout_DeniedFailClosed(t *testing.T) {
	fs := primaryFS()
	verbsErr := errors.New("worktree-gate: embedded verbs.json is corrupt")
	d := Decide(fs.lstat, fs.readFile, Verbs{}, verbsErr, TrackingDocs{}, nil, Input{ToolName: "Bash", CWD: "/repo", Command: "git commit -m x"})
	if !d.Deny {
		t.Fatal("expected deny: a degraded classifier artifact in a primary checkout could be masking a real write (fail closed, not fail open)")
	}
}

func TestDecide_UnknownTool_NoOp(t *testing.T) {
	fs := primaryFS()
	d := Decide(fs.lstat, fs.readFile, Verbs{}, nil, TrackingDocs{}, nil, Input{ToolName: "Read", FilePath: "/repo/a.go"})
	if d.Deny || d.Degraded != "" {
		t.Errorf("expected a no-op Decision for a tool this gate doesn't govern, got %+v", d)
	}
}

// -- tracking-doc exemption: a Write/Edit under the configured project dir
// whose basename is in the tracking-doc set is allowed even in a primary
// checkout.

func testTrackingDocs(t *testing.T) TrackingDocs {
	t.Helper()
	td, err := DefaultTrackingDocs()
	if err != nil {
		t.Fatalf("DefaultTrackingDocs() error: %v", err)
	}
	return td
}

func TestDecide_Write_TrackingDocExempt_AllowedInPrimaryCheckout(t *testing.T) {
	fs := newFakeFS().dir("/proj/.dat/some-effort/.git")
	td := testTrackingDocs(t)
	d := Decide(fs.lstat, fs.readFile, Verbs{}, nil, td, nil, Input{
		ToolName: "Write", FilePath: "/proj/.dat/some-effort/plan.json", ProjectDir: "/proj",
	})
	if d.Deny {
		t.Fatalf("expected allow for a tracking-doc write under the project dir, got deny: %s", d.Reason)
	}
}

func TestDecide_Edit_TrackingDocExempt_AllowedInPrimaryCheckout(t *testing.T) {
	fs := newFakeFS().dir("/proj/.dat/some-effort/.git")
	td := testTrackingDocs(t)
	d := Decide(fs.lstat, fs.readFile, Verbs{}, nil, td, nil, Input{
		ToolName: "Edit", FilePath: "/proj/.dat/some-effort/plan.json", ProjectDir: "/proj",
	})
	if d.Deny {
		t.Fatalf("expected allow for a tracking-doc edit under the project dir, got deny: %s", d.Reason)
	}
}

func TestDecide_Write_TrackingDocExempt_NoProjectDir_Denied(t *testing.T) {
	fs := newFakeFS().dir("/repo/.dat/some-effort/.git")
	td := testTrackingDocs(t)
	d := Decide(fs.lstat, fs.readFile, Verbs{}, nil, td, nil, Input{
		ToolName: "Write", FilePath: "/repo/.dat/some-effort/plan.json", ProjectDir: "",
	})
	if !d.Deny {
		t.Fatal("expected deny: no project dir configured, so no exemption applies")
	}
}

func TestDecide_Write_TrackingDocExempt_OutsideProjectDir_Denied(t *testing.T) {
	fs := newFakeFS().dir("/otherrepo/.dat/some-effort/.git")
	td := testTrackingDocs(t)
	d := Decide(fs.lstat, fs.readFile, Verbs{}, nil, td, nil, Input{
		ToolName: "Write", FilePath: "/otherrepo/.dat/some-effort/plan.json", ProjectDir: "/proj",
	})
	if !d.Deny {
		t.Fatal("expected deny: the target sits outside the configured project dir")
	}
}

func TestDecide_Write_TrackingDocExempt_BasenameNotInSet_Denied(t *testing.T) {
	fs := newFakeFS().dir("/proj/.dat/some-effort/.git")
	td := testTrackingDocs(t)
	d := Decide(fs.lstat, fs.readFile, Verbs{}, nil, td, nil, Input{
		ToolName: "Write", FilePath: "/proj/.dat/some-effort/notes.md", ProjectDir: "/proj",
	})
	if !d.Deny {
		t.Fatal("expected deny: notes.md is not in the tracking-doc set")
	}
}

// -- SC15 read allowance (A6): the digest-verified provisioned CLI's one read
// verb, `worktree list`, is reclassified read where it would otherwise fail
// closed as ClassUncertain, so it is allowed from a primary checkout. It shares
// the identity check with the write allowance but not its retarget policy, and
// voids on any file-opening redirect or here-document.

const (
	sc15Bin        = "/plugin-data/bin/git-tools"
	sc15BinContent = "PROVISIONED-CLI-BYTES"
)

// sc15Argv resolves the corpus-style argv sentinels ("" => correct value,
// "omit" => empty parameter, "wrong" => a valid-but-mismatched digest, else a
// literal) so a case can drive the identity inputs off argv alone.
func sc15Argv(pathSel, digestSel, correctDigest string) (path, digest string) {
	path = sc15Bin
	switch pathSel {
	case "omit":
		path = ""
	case "":
	default:
		path = pathSel
	}
	digest = correctDigest
	switch digestSel {
	case "omit":
		digest = ""
	case "wrong":
		digest = "0000000000000000000000000000000000000000000000000000000000000000"
	case "":
	default:
		digest = digestSel
	}
	return path, digest
}

func TestDecide_Bash_SC15ReadAllowance(t *testing.T) {
	v := testVerbs(t)
	correctDigest := hex.EncodeToString(sha256Sum(sc15BinContent))

	cases := []struct {
		name          string
		command       string
		pathSel       string
		digestSel     string
		unreadableBin bool
		wantDeny      bool
	}{
		{name: "bare-worktree-list-allowed", command: sc15Bin + " worktree list"},
		{name: "repo-flag-does-not-void-read-allowed", command: sc15Bin + " worktree list --repo /other"},
		{name: "glued-config-flag-does-not-void-read-allowed", command: sc15Bin + " worktree list --config=/other"},

		{name: "wrong-digest-denies", command: sc15Bin + " worktree list", digestSel: "wrong", wantDeny: true},
		{name: "empty-path-argv-denies", command: sc15Bin + " worktree list", pathSel: "omit", wantDeny: true},
		{name: "empty-digest-argv-denies", command: sc15Bin + " worktree list", digestSel: "omit", wantDeny: true},
		{name: "bare-name-denies", command: "git-tools worktree list", wantDeny: true},
		{name: "relative-name-denies", command: "./git-tools worktree list", wantDeny: true},
		{name: "unreadable-binary-denies", command: sc15Bin + " worktree list", unreadableBin: true, wantDeny: true},
		{name: "output-redirect-into-primary-denies", command: sc15Bin + " worktree list > /repo/tracked.md", wantDeny: true},
		{name: "input-redirect-voids-read-denies", command: sc15Bin + " worktree list < /repo/somefile", wantDeny: true},
		{name: "here-document-voids-read-denies", command: sc15Bin + " worktree list <<EOF\nx\nEOF", wantDeny: true},
		{name: "worktree-no-subverb-denies", command: sc15Bin + " worktree", wantDeny: true},
		{name: "worktree-prune-not-read-denies", command: sc15Bin + " worktree prune", wantDeny: true},
		{name: "scan-not-read-denies", command: sc15Bin + " scan", wantDeny: true},
		{name: "resign-not-read-but-write-allowed", command: sc15Bin + " resign --apply"},
		{name: "command-substituted-list-at-depth-not-zero-denies", command: sc15Bin + " worktree list $(" + sc15Bin + " worktree list)", wantDeny: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := newFakeFS().dir("/repo/.git").file("/repo/tracked.md", "x\n")
			if c.unreadableBin {
				fs.errAt(sc15Bin, errors.New("permission denied"))
			} else {
				fs.file(sc15Bin, sc15BinContent)
			}
			path, digest := sc15Argv(c.pathSel, c.digestSel, correctDigest)
			d := Decide(fs.lstat, fs.readFile, v, nil, TrackingDocs{}, nil, Input{
				ToolName: "Bash", CWD: "/repo", Command: c.command,
				ProvisionedBinPath: path, ProvisionedBinDigest: digest,
			})
			if d.Deny != c.wantDeny {
				t.Errorf("Decide(cmd=%q) deny=%v, want %v (reason=%q)", c.command, d.Deny, c.wantDeny, d.Reason)
			}
		})
	}
}

// TestDecide_Bash_SC15WriteAllowance_Unchanged pins that splitting the identity
// half out of sc15Exempt left the write allowance behaviorally identical: the
// landing verbs are still allowed from a primary checkout (worktree add still
// waived from the named-path rule even naming a primary path), resign joins
// them as a landing verb, and all four retarget spellings still void it.
func TestDecide_Bash_SC15WriteAllowance_Unchanged(t *testing.T) {
	v := testVerbs(t)
	correctDigest := hex.EncodeToString(sha256Sum(sc15BinContent))

	cases := []struct {
		name     string
		command  string
		wantDeny bool
	}{
		{name: "merge-allowed", command: sc15Bin + " merge main"},
		{name: "push-allowed", command: sc15Bin + " push"},
		{name: "worktree-add-names-primary-path-allowed", command: sc15Bin + " worktree add /repo/wt2 main"},
		{name: "resign-landing-allowed", command: sc15Bin + " resign --apply"},
		{name: "merge-repo-spaced-denies", command: sc15Bin + " merge --repo /other main", wantDeny: true},
		{name: "merge-config-spaced-denies", command: sc15Bin + " merge --config /other main", wantDeny: true},
		{name: "merge-repo-glued-denies", command: sc15Bin + " merge --repo=/other main", wantDeny: true},
		{name: "merge-config-glued-denies", command: sc15Bin + " merge --config=/other main", wantDeny: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := newFakeFS().dir("/repo/.git").file(sc15Bin, sc15BinContent)
			d := Decide(fs.lstat, fs.readFile, v, nil, TrackingDocs{}, nil, Input{
				ToolName: "Bash", CWD: "/repo", Command: c.command,
				ProvisionedBinPath: sc15Bin, ProvisionedBinDigest: correctDigest,
			})
			if d.Deny != c.wantDeny {
				t.Errorf("Decide(cmd=%q) deny=%v, want %v (reason=%q)", c.command, d.Deny, c.wantDeny, d.Reason)
			}
		})
	}
}
