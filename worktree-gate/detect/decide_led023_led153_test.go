package detect

import (
	"strings"
	"testing"
)

// led023FS builds the primary-plus-worktree topology these tests need: a
// real primary checkout at /repo (so a target that resolves there names an
// actual KindPrimary path, not an absent one) with a linked worktree at
// /repo/wt.
func led023FS() *fakeFS {
	return newFakeFS().dir("/repo/.git").
		file("/repo/wt/.git", "gitdir: /repo/.git/worktrees/wt\n").
		file("/repo/tracked.md", "tracked\n").
		file("/repo/wt/tracked.md", "tracked\n")
}

// LED-023: the unmodeled-command default in namedPaths used to treat every
// non-flag operand of ANY unmodeled write-class command as a candidate write
// destination -- including an inline interpreter program string that merely
// happens to contain glob-like bracket syntax. All three cases below run from
// a WORKTREE cwd, the cwd where SC20's named-path rule (not the cwd leg) is
// the only thing that can deny or allow them, per this branch's own prior
// review lesson: a primary-checkout cwd denies everything regardless of this
// bug and would hide a coverage loss completely.
func TestLED023_InlineProgramOperandNoLongerMistakenForPath(t *testing.T) {
	fs := led023FS()
	v := testVerbs(t)

	allowed := []string{
		`jq '.a["b"]' /repo/wt/tracked.md > /repo/wt/out.json`,
		`python3 -c "d['x']=1" > /repo/wt/out.txt`,
		`awk '{print $1"-"$2}' /repo/wt/tracked.md > /repo/wt/out.txt`,
	}
	for _, cmd := range allowed {
		t.Run(cmd, func(t *testing.T) {
			d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{
				ToolName: "Bash", CWD: "/repo/wt", Command: cmd,
			})
			if d.Deny {
				t.Fatalf("expected allow (real redirect target lands in the worktree), got deny: %s", d.Reason)
			}
		})
	}
}

// Negative/regression: the fix must not blind the rule to a real write
// smuggled the same way -- a redirect that genuinely lands in the primary
// checkout still denies, and the destination-bearing unmodeled commands
// (rm, sed -i, an editor, tee, mkdir, …) still judge their own operand
// exactly as before, from the same worktree cwd.
func TestLED023_RealWritesStillDenied_FromWorktreeCWD(t *testing.T) {
	fs := led023FS()
	v := testVerbs(t)

	denied := []string{
		`jq '.a["b"]' /repo/wt/tracked.md > /repo/tracked.md`,
		`rm /repo/tracked.md`,
		`sed -i s/a/b/ /repo/tracked.md`,
		`tee /repo/tracked.md < /repo/wt/tracked.md`,
		`mkdir /repo/newdir`,
		`vim /repo/tracked.md`,
	}
	for _, cmd := range denied {
		t.Run(cmd, func(t *testing.T) {
			d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{
				ToolName: "Bash", CWD: "/repo/wt", Command: cmd,
			})
			if !d.Deny {
				t.Fatalf("expected deny (operand names a primary-checkout path), got allow")
			}
		})
	}
}

// sed is split on its own -i flag: only that form writes to a named operand
// at all, so a plain sed's positional file argument is a read source, not a
// destination, and must not be denied merely for being unexpandable-shaped
// or for resolving into the primary checkout as an operand.
func TestLED023_SedWithoutInPlace_OperandIsNotADestination(t *testing.T) {
	fs := led023FS()
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{
		ToolName: "Bash", CWD: "/repo/wt",
		Command: `sed s/a/b/ /repo/tracked.md > /repo/wt/out.md`,
	})
	if d.Deny {
		t.Fatalf("expected allow: sed's own positional file is a read source without -i, got deny: %s", d.Reason)
	}
}

// LED-153: a documented helper form invokes the target tool through a shell
// variable ("$DAT_TOOLS"), so the command word itself is unrecognized by
// namedPaths. Its own non-destination argument (a read-source path) must not
// be treated as a candidate write target and denied for being unexpandable,
// independent of whatever the real redirect target is. A literal, absolute
// redirect target is allowed from a worktree cwd; a redirect target that is
// itself a shell variable stays denied (SC5's precedent: the gate cannot rule
// out where an unresolved variable points), which is the half of LED-153
// that belongs to the documented call form, not to this classifier.
func TestLED153_VariableNamedToolArgumentNoLongerMistakenForPath(t *testing.T) {
	fs := led023FS()
	v := testVerbs(t)

	d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{
		ToolName: "Bash", CWD: "/repo/wt",
		Command: `"$DAT_TOOLS" render "$PROJ/plan.json" > /repo/wt/plan.md`,
	})
	if d.Deny {
		t.Fatalf("expected allow: the render call's own read argument is not a destination, got deny: %s", d.Reason)
	}

	d = Decide(fs.lstat, fs.readFile, nil, v, nil, Input{
		ToolName: "Bash", CWD: "/repo/wt",
		Command: `"$DAT_TOOLS" render "$PROJ/plan.json" > "$PROJ/plan.md"`,
	})
	if !d.Deny {
		t.Fatal("expected deny: the redirect target itself is still an unresolved shell variable")
	}
	if !strings.Contains(d.Reason, "cannot resolve statically") {
		t.Fatalf("expected the unresolvable-target reason, got: %s", d.Reason)
	}
}
