package detect

import (
	"strings"
	"testing"
)

// SC24: an interior `cd` (inside a command substitution, a backtick span, or
// an eval/-c payload) can retarget where a path-blind write (git commit,
// reset, checkout -- any verb whose destination is implicit in the process's
// own cwd, not a named operand) actually lands, defeating both SC20's
// named-path rule (nothing to resolve) and decideBash's own cwd leg (which
// only ever sees the OUTER command's own effective cwd). Every case here runs
// from a WORKTREE cwd: that is the only cwd where this bypass class is live,
// since a primary-checkout cwd already denies almost any write-class command
// on its own merits regardless of this fix.
//
// Topology: /repo is a primary checkout, /repo/wt its worktree (the caller's
// own sanctioned location), /other is a SECOND repo's primary checkout, and
// /other/wt is that second repo's own worktree.
func sc24FS() *fakeFS {
	return newFakeFS().
		dir("/repo/.git").
		file("/repo/wt/.git", "gitdir: /repo/.git/worktrees/wt\n").
		dir("/other/.git").
		file("/other/wt/.git", "gitdir: /other/.git/worktrees/wt\n")
}

func TestDecide_Bash_InteriorCdThenPathBlindWrite_CommandSubstitution_Denied(t *testing.T) {
	fs := sc24FS()
	v := testVerbs(t)
	verbs := []string{
		`git commit -am "message"`,
		`git reset --hard HEAD~1`,
		`git checkout main`,
	}
	for _, verb := range verbs {
		cmd := `$(cd /other && ` + verb + `)`
		d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{ToolName: "Bash", CWD: "/repo/wt", Command: cmd})
		if !d.Deny {
			t.Errorf("Decide(cmd=%q) from a worktree cwd = allow, want deny (interior cd retargets into another repo's primary checkout)", cmd)
			continue
		}
		if !containsAll(d.Reason, "/other") {
			t.Errorf("Decide(cmd=%q) reason %q does not name the resolved interior target /other, not the caller's own outer cwd", cmd, d.Reason)
		}
	}
}

func TestDecide_Bash_InteriorCdThenPathBlindWrite_Backtick_Denied(t *testing.T) {
	fs := sc24FS()
	v := testVerbs(t)
	verbs := []string{
		`git commit -am "message"`,
		`git reset --hard HEAD~1`,
		`git checkout main`,
	}
	for _, verb := range verbs {
		cmd := "`cd /other && " + verb + "`"
		d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{ToolName: "Bash", CWD: "/repo/wt", Command: cmd})
		if !d.Deny {
			t.Errorf("Decide(cmd=%q) from a worktree cwd = allow, want deny (interior cd retargets into another repo's primary checkout)", cmd)
		}
	}
}

func TestDecide_Bash_InteriorCdThenPathBlindWrite_EvalPayload_Denied(t *testing.T) {
	fs := sc24FS()
	v := testVerbs(t)
	cmd := `eval "cd /other && git commit -am fix"`
	d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{ToolName: "Bash", CWD: "/repo/wt", Command: cmd})
	if !d.Deny {
		t.Fatalf("Decide(cmd=%q) from a worktree cwd = allow, want deny (eval payload's own cd retargets into another repo's primary checkout)", cmd)
	}
}

func TestDecide_Bash_InteriorCdThenPathBlindWrite_NestedSubshellInSubstitution_Denied(t *testing.T) {
	fs := sc24FS()
	v := testVerbs(t)
	cmd := `$( (cd /other && git commit -am fix) )`
	d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{ToolName: "Bash", CWD: "/repo/wt", Command: cmd})
	if !d.Deny {
		t.Fatalf("Decide(cmd=%q) from a worktree cwd = allow, want deny (subshell nested inside a command substitution)", cmd)
	}
}

// TestDecide_Bash_InteriorCdIntoAnotherWorktree_Allowed is the companion
// negative case for a genuine positive: an interior `cd` that lands in
// ANOTHER repository's own worktree (already isolated) is not denied, so this
// fix does not turn every interior `cd` into a blanket deny -- only one that
// resolves into a primary checkout.
func TestDecide_Bash_InteriorCdIntoAnotherWorktree_Allowed(t *testing.T) {
	fs := sc24FS()
	v := testVerbs(t)
	cmd := `$(cd /other/wt && git commit -am fix)`
	d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{ToolName: "Bash", CWD: "/repo/wt", Command: cmd})
	if d.Deny {
		t.Fatalf("Decide(cmd=%q) from a worktree cwd = deny, want allow (interior cd lands in an already-isolated worktree): %s", cmd, d.Reason)
	}
}

// TestDecide_Bash_PathBlindWrite_NoInteriorCd_Regression pins that the common
// case -- a path-blind write with no interior cd at all -- still resolves
// against the caller's own real cwd exactly as before this fix, run from a
// worktree cwd in both directions: allowed inside the caller's own worktree,
// denied when that same worktree's own cwd is swapped for a primary checkout.
func TestDecide_Bash_PathBlindWrite_NoInteriorCd_Regression(t *testing.T) {
	fs := sc24FS()
	v := testVerbs(t)
	verbs := []string{
		`git commit -am "message"`,
		`git reset --hard HEAD~1`,
		`git checkout main`,
	}
	for _, verb := range verbs {
		if d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{ToolName: "Bash", CWD: "/repo/wt", Command: verb}); d.Deny {
			t.Errorf("Decide(cmd=%q) from the caller's own worktree cwd = deny, want allow (no regression): %s", verb, d.Reason)
		}
		if d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{ToolName: "Bash", CWD: "/repo", Command: verb}); !d.Deny {
			t.Errorf("Decide(cmd=%q) from the primary checkout cwd = allow, want deny (no regression)", verb)
		}
	}
}

// TestDecide_Bash_InteriorCdUnresolvable_DeniesFailClosed pins SC24's own
// fail-closed leg: an interior `cd` whose target the gate cannot read
// statically (here, a command substitution) still denies a path-blind write
// behind it, rather than silently falling back to the caller's own outer cwd.
func TestDecide_Bash_InteriorCdUnresolvable_DeniesFailClosed(t *testing.T) {
	fs := sc24FS()
	v := testVerbs(t)
	cmd := `$(cd $(pwd)/elsewhere && git commit -am fix)`
	d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{ToolName: "Bash", CWD: "/repo/wt", Command: cmd})
	if !d.Deny {
		t.Fatalf("Decide(cmd=%q) from a worktree cwd = allow, want deny (interior cd target is not statically resolvable)", cmd)
	}
}

// TestDecide_Bash_InteriorCdRelativeTarget_ComposedOntoOuterCWD covers
// composeInteriorCWD's relative-join branch, which every case above misses by
// using only absolute interior targets. Both directions, so the assertion
// pins real path composition rather than a target that happens to match: the
// same `..`-style spelling denies or allows purely on where the join lands.
func TestDecide_Bash_InteriorCdRelativeTarget_ComposedOntoOuterCWD(t *testing.T) {
	v := testVerbs(t)

	fs := sc24FS()
	cmd := `$(cd ../../other && git commit -am fix)`
	d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{ToolName: "Bash", CWD: "/repo/wt", Command: cmd})
	if !d.Deny {
		t.Fatalf("Decide(cmd=%q) from /repo/wt = allow, want deny (relative interior cd joins onto the outer cwd and lands in /other, a primary checkout)", cmd)
	}
	if !containsAll(d.Reason, "/other") {
		t.Errorf("Decide(cmd=%q) reason %q does not name the joined target /other", cmd, d.Reason)
	}

	fs = sc24FS().dir("/repo/wt/sub")
	cmd = `$(cd .. && git commit -am fix)`
	d = Decide(fs.lstat, fs.readFile, nil, v, nil, Input{ToolName: "Bash", CWD: "/repo/wt/sub", Command: cmd})
	if d.Deny {
		t.Fatalf("Decide(cmd=%q) from /repo/wt/sub = deny, want allow (the join lands back in the caller's own worktree /repo/wt): %s", cmd, d.Reason)
	}
}

// TestDecide_Bash_InteriorCd_RelativeNamedPath_ResolvesAgainstInteriorCWD
// pins the OTHER half of threading the interior cwd through the recursion:
// SC20's named-path rule resolves a relative destination against that same
// composed cwd, so a write that DOES name a path is caught in the interior
// too. Without the threading this resolved against the caller's own worktree
// and allowed, and nothing else in this file would catch that regression --
// every other case here reaches the path-blind leg instead.
func TestDecide_Bash_InteriorCd_RelativeNamedPath_ResolvesAgainstInteriorCWD(t *testing.T) {
	fs := sc24FS()
	v := testVerbs(t)
	cmd := `$(cd /other && rm -rf build)`
	d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{ToolName: "Bash", CWD: "/repo/wt", Command: cmd})
	if !d.Deny {
		t.Fatalf("Decide(cmd=%q) from /repo/wt = allow, want deny (relative named path resolves against the interior cwd, into /other)", cmd)
	}
	if !containsAll(d.Reason, "/other/build") {
		t.Errorf("Decide(cmd=%q) reason %q does not name /other/build, so the named path was not resolved against the interior cwd", cmd, d.Reason)
	}
}

// TestDecide_Bash_InteriorCdAfterFirstGit_DisclosedResidual pins the residual
// documented at pathBlindWriteDenial as ALLOWED, keeping it a visible
// boundary rather than a silent one: resolveEffectiveCWD returns at the first
// git word, so a `cd` behind one in the same interior never composes. The
// top-level spelling is asserted alongside it to hold the record that this is
// the shared cwd contract rather than an interior-only weakness -- both move
// together the day SC-CWD-RESOLVER-CONTRACT does.
func TestDecide_Bash_InteriorCdAfterFirstGit_DisclosedResidual(t *testing.T) {
	fs := sc24FS()
	v := testVerbs(t)
	for _, cmd := range []string{
		`$(git status && cd /other && git commit -am fix)`,
		`git status && cd /other && git commit -am fix`,
	} {
		d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{ToolName: "Bash", CWD: "/repo/wt", Command: cmd})
		if d.Deny {
			t.Fatalf("Decide(cmd=%q) = deny, want allow -- this is the disclosed first-git-stop residual. A deny here means the cwd contract changed: retire the residual note at pathBlindWriteDenial and re-point this test, rather than deleting it. Got: %s", cmd, d.Reason)
		}
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
