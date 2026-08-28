package detect

import (
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func primaryFS() *fakeFS { return newFakeFS().dir("/repo/.git") }
func worktreeFS() *fakeFS {
	return newFakeFS().file("/repo/wt/.git", "gitdir: /repo/.git/worktrees/wt\n")
}

func TestDecide_WriteOutsideWorktree_Denied(t *testing.T) {
	fs := primaryFS()
	d := Decide(fs.lstat, fs.readFile, nil, Verbs{}, nil, Input{ToolName: "Write", FilePath: "/repo/a.go"})
	if !d.Deny {
		t.Fatal("expected deny for a Write into the primary checkout")
	}
}

func TestDecide_WriteInsideWorktree_Allowed(t *testing.T) {
	fs := worktreeFS()
	d := Decide(fs.lstat, fs.readFile, nil, Verbs{}, nil, Input{ToolName: "Write", FilePath: "/repo/wt/a.go"})
	if d.Deny {
		t.Fatalf("expected allow for a Write inside a worktree, got deny: %s", d.Reason)
	}
}

func TestDecide_WriteOutsideAnyRepo_Allowed(t *testing.T) {
	fs := newFakeFS()
	d := Decide(fs.lstat, fs.readFile, nil, Verbs{}, nil, Input{ToolName: "Write", FilePath: "/tmp/scratch.txt"})
	if d.Deny {
		t.Fatalf("expected allow for a write confidently outside any repo, got deny: %s", d.Reason)
	}
}

func TestDecide_Bash_ReadInPrimaryCheckout_Allowed(t *testing.T) {
	fs := primaryFS()
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{ToolName: "Bash", CWD: "/repo", Command: "git status"})
	if d.Deny {
		t.Fatalf("expected allow for a read in the primary checkout, got deny: %s", d.Reason)
	}
}

func TestDecide_Bash_WriteInPrimaryCheckout_Denied(t *testing.T) {
	fs := primaryFS()
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{ToolName: "Bash", CWD: "/repo", Command: "git commit -m x"})
	if !d.Deny {
		t.Fatal("expected deny for a write-classified Bash command in the primary checkout")
	}
}

func TestDecide_Bash_WriteInWorktree_Allowed(t *testing.T) {
	fs := worktreeFS()
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{ToolName: "Bash", CWD: "/repo/wt", Command: "git commit -m x"})
	if d.Deny {
		t.Fatalf("expected allow for a write in an already-isolated worktree, got deny: %s", d.Reason)
	}
}

func TestDecide_Bash_UncertainInPrimaryCheckout_Denied(t *testing.T) {
	fs := primaryFS()
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{ToolName: "Bash", CWD: "/repo", Command: "some-unlisted-tool"})
	if !d.Deny {
		t.Fatal("expected deny for an unclassifiable command in the primary checkout (fail closed on uncertainty)")
	}
}

func TestDecide_Bash_OutsideAnyRepo_Allowed(t *testing.T) {
	fs := newFakeFS()
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{ToolName: "Bash", CWD: "/tmp", Command: "git commit -m x"})
	if d.Deny {
		t.Fatalf("expected allow for a Bash call confidently outside any repo, got deny: %s", d.Reason)
	}
}

func TestDecide_Bash_NoCWD_DeniedFailClosed(t *testing.T) {
	fs := primaryFS()
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{ToolName: "Bash", Command: "git status"})
	if !d.Deny {
		t.Fatal("expected deny when no working directory is reported (fail closed on uncertainty)")
	}
}

func TestDecide_Bash_IndeterminateRepoMembership_DeniedFailClosed(t *testing.T) {
	fs := newFakeFS().errAt("/repo/.git", errors.New("permission denied"))
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{ToolName: "Bash", CWD: "/repo", Command: "git status"})
	if !d.Deny {
		t.Fatal("expected deny when repo membership can't be resolved (fail closed on uncertainty)")
	}
}

func TestDecide_Bash_DegradedClassifierInPrimaryCheckout_DeniedFailClosed(t *testing.T) {
	fs := primaryFS()
	verbsErr := errors.New("worktree-gate: embedded verbs.json is corrupt")
	d := Decide(fs.lstat, fs.readFile, nil, Verbs{}, verbsErr, Input{ToolName: "Bash", CWD: "/repo", Command: "git commit -m x"})
	if !d.Deny {
		t.Fatal("expected deny: a degraded classifier artifact in a primary checkout could be masking a real write (fail closed, not fail open)")
	}
}

// -- denial remedies (L1.22): every denial names a remedy this gate itself
// permits from where the caller stands. The two literal pins below break
// together in one edit, so they are spelled out rather than derived: the
// working-directory leg keeps the worktree advice, while the named-target leg
// must NOT repeat it -- creating a worktree of the caller's own repository does
// nothing for a write aimed into a different one.

const worktreeAdvice = "create one and retry"

func TestDecide_Bash_WorkingDirectoryDenial_KeepsWorktreeAdvice(t *testing.T) {
	fs := primaryFS()
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{ToolName: "Bash", CWD: "/repo", Command: "git commit -m x"})
	if !d.Deny {
		t.Fatal("expected deny for a write-classified Bash command in the primary checkout")
	}
	if !strings.Contains(d.Reason, worktreeAdvice) {
		t.Errorf("working-directory denial %q must still name the worktree remedy %q", d.Reason, worktreeAdvice)
	}
}

func TestDecide_Bash_NamedTargetDenial_DropsWorktreeAdvice(t *testing.T) {
	fs := newFakeFS().dir("/repo/.git").dir("/other/.git")
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{ToolName: "Bash", CWD: "/repo", Command: "cp x /other/f"})
	if !d.Deny {
		t.Fatal("expected deny for a write naming a path inside another repository's primary checkout")
	}
	if strings.Contains(d.Reason, worktreeAdvice) {
		t.Errorf("named-target denial %q must not name a remedy about the caller's own repository (%q)", d.Reason, worktreeAdvice)
	}
	if !strings.Contains(d.Reason, "worktree list") {
		t.Errorf("named-target denial %q names no way to find a worktree of the containing repository", d.Reason)
	}
}

// TestDecide_Bash_EveryCorpusDenial_NamesARemedy asserts what the corpus format
// itself cannot: each denying case carries a non-empty remedy, that remedy also
// closes the human-facing Reason, and no case offers the generic worktree advice
// as its whole remedy -- it must come with a spelling the caller can run to find
// a worktree. The remedy is read from Decision.Remedy rather than split back out
// of Reason: Reason's situation half echoes the caller's command, which can
// itself contain the " -- " separator, so any split of Reason is unreliable.
func TestDecide_Bash_EveryCorpusDenial_NamesARemedy(t *testing.T) {
	v := testVerbs(t)
	correctDigest := hex.EncodeToString(sha256Sum(corpusBinContent))

	for _, c := range loadDecideBashCorpus(t) {
		if !c.WantDeny {
			continue
		}
		t.Run(c.Name, func(t *testing.T) {
			d := runDecideBashCase(t, v, correctDigest, c)
			if strings.TrimSpace(d.Remedy) == "" {
				t.Fatalf("Decide(cwd=%q, cmd=%q) denial %q carries no remedy", c.CWD, c.Command, d.Reason)
			}
			if !strings.HasSuffix(d.Reason, " -- "+d.Remedy) {
				t.Errorf("Decide(cwd=%q, cmd=%q) Reason %q does not close with its remedy %q", c.CWD, c.Command, d.Reason, d.Remedy)
			}
			if strings.Contains(d.Remedy, worktreeAdvice) && !strings.Contains(d.Remedy, "worktree list") {
				t.Errorf("Decide(cwd=%q, cmd=%q) remedy %q offers the generic worktree advice alone", c.CWD, c.Command, d.Remedy)
			}
		})
	}
}

// TestDecide_Bash_WriteInPrimaryCheckout_RemedyNamesBranchDeleteConstraint pins
// that remedyThisRepoWorktree no longer offers "create a worktree and retry" as
// the whole answer for a branch delete: git refuses to delete a branch checked
// out in the worktree the delete runs from, so that route cannot work for this
// verb. The remedy must instead name the working route (the provisioned CLI's
// `branch delete`, run from the primary checkout itself) and state why.
func TestDecide_Bash_WriteInPrimaryCheckout_RemedyNamesBranchDeleteConstraint(t *testing.T) {
	fs := primaryFS()
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{ToolName: "Bash", CWD: "/repo", Command: "git commit -m x"})
	if !d.Deny {
		t.Fatal("expected deny for a write-classified Bash command in the primary checkout")
	}
	if !strings.Contains(d.Remedy, "checked-out branch") {
		t.Errorf("remedy %q does not state the constraint that a worktree cannot delete its own checked-out branch", d.Remedy)
	}
	if !strings.Contains(d.Remedy, "`branch delete <branch>`") {
		t.Errorf("remedy %q does not name the sanctioned direct route for a branch delete", d.Remedy)
	}
}

// TestRemedyConstants_AreRunnableAsSpelled pins the two properties a remedy's
// TEXT must hold, which no per-case assertion covers because they are about how
// the advice is spelled rather than which advice a leg picks:
//
//   - no remedy contains deny()'s " -- " join token, so Reason always closes
//     with exactly the remedy;
//   - no remedy offers a bare `git-tools <verb>` invocation. Both SC15
//     allowances key on binary identity, so a bare command word fails
//     sc15Identity and is denied from a primary checkout (corpus case
//     sc15-worktree-list-bare-word-denies) -- advice this gate then denies is
//     worse than no advice. A remedy naming the CLI must say to run it by its
//     absolute provisioned path.
//
// The list is spelled out rather than derived: a new remedy constant must be
// added here deliberately, which is the point.
func TestRemedyConstants_AreRunnableAsSpelled(t *testing.T) {
	remedies := map[string]string{
		"remedyTargetRepoWorktree": remedyTargetRepoWorktree,
		"remedyThisRepoWorktree":   remedyThisRepoWorktree,
		"remedyRewordAsRead":       remedyRewordAsRead,
		"remedyLiteralTarget":      remedyLiteralTarget,
		"remedyStaticCWD":          remedyStaticCWD,
		"remedyReportCWD":          remedyReportCWD,
		"remedyReadablePath":       remedyReadablePath,
		"remedyProveMembership":    remedyProveMembership,
		"remedyRestoreVerbData":    remedyRestoreVerbData,
	}
	for name, r := range remedies {
		t.Run(name, func(t *testing.T) {
			if strings.Contains(r, " -- ") {
				t.Errorf("remedy %q contains deny()'s join token, so Reason no longer closes with exactly the remedy", r)
			}
			if strings.Contains(r, "`git-tools ") {
				t.Errorf("remedy %q offers a bare `git-tools <verb>` spelling, which fails SC15's identity check and is denied from a primary checkout; name the absolute provisioned path instead", r)
			}
		})
	}
}

func TestDecide_UnknownTool_NoOp(t *testing.T) {
	fs := primaryFS()
	d := Decide(fs.lstat, fs.readFile, nil, Verbs{}, nil, Input{ToolName: "Read", FilePath: "/repo/a.go"})
	if d.Deny || d.Degraded != "" {
		t.Errorf("expected a no-op Decision for a tool this gate doesn't govern, got %+v", d)
	}
}

// -- a Write/Edit under a .dat tree denies from a primary checkout like any
// other target, whatever its basename.

func TestDecide_Write_DatTreePlanJSON_InPrimaryCheckout_Denied(t *testing.T) {
	fs := newFakeFS().dir("/proj/.dat/some-effort/.git")
	d := Decide(fs.lstat, fs.readFile, nil, Verbs{}, nil, Input{
		ToolName: "Write", FilePath: "/proj/.dat/some-effort/plan.json",
	})
	if !d.Deny {
		t.Fatal("expected deny for a Write into the primary checkout")
	}
}

func TestDecide_Edit_DatTreePlanJSON_InPrimaryCheckout_Denied(t *testing.T) {
	fs := newFakeFS().dir("/proj/.dat/some-effort/.git")
	d := Decide(fs.lstat, fs.readFile, nil, Verbs{}, nil, Input{
		ToolName: "Edit", FilePath: "/proj/.dat/some-effort/plan.json",
	})
	if !d.Deny {
		t.Fatal("expected deny for an Edit into the primary checkout")
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
			d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{
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
			d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{
				ToolName: "Bash", CWD: "/repo", Command: c.command,
				ProvisionedBinPath: sc15Bin, ProvisionedBinDigest: correctDigest,
			})
			if d.Deny != c.wantDeny {
				t.Errorf("Decide(cmd=%q) deny=%v, want %v (reason=%q)", c.command, d.Deny, c.wantDeny, d.Reason)
			}
		})
	}
}
