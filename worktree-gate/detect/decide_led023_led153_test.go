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

// Review regression: narrowing operand judging must not narrow it past the
// commands the verbs model itself names as writers. A package manager's
// DEFAULT write locus is its cwd -- which the cwd leg judges -- but each of
// them takes a path-valued option that retargets that locus into a checkout
// the cwd leg never looks at, so its operands must still be judged. The first
// pass at the LED-023 fix replaced the catch-all with a hand-curated command
// list that omitted this whole class, silently reopening a write into the
// primary checkout from a legitimate worktree cwd.
func TestLED023_ModeledWriterOperandsStillJudged_PackageManagerRetarget(t *testing.T) {
	fs := led023FS()
	v := testVerbs(t)

	denied := []string{
		`npm install --prefix /repo`,
		`pip install -t /repo/site pkg`,
		`pip install --target /repo/site pkg`,
		`poetry install --directory /repo`,
		`gem install --install-dir /repo pkg`,
		`yarn add --cwd /repo pkg`,
		`go install /repo/tracked.md`,
		`find /repo -delete`,
	}
	for _, cmd := range denied {
		t.Run(cmd, func(t *testing.T) {
			d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{
				ToolName: "Bash", CWD: "/repo/wt", Command: cmd,
			})
			if !d.Deny {
				t.Fatalf("expected deny: an operand of a modeled write command resolves into the primary checkout")
			}
		})
	}

	// The same commands with no retargeting operand write in the cwd, which is
	// a worktree here -- they must stay allowed, or the rule above is just the
	// old over-broad default wearing a narrower name.
	allowed := []string{
		`npm install`,
		`npm install lodash`,
		`pip install requests`,
		`go mod tidy`,
	}
	for _, cmd := range allowed {
		t.Run(cmd, func(t *testing.T) {
			d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{
				ToolName: "Bash", CWD: "/repo/wt", Command: cmd,
			})
			if d.Deny {
				t.Fatalf("expected allow (the write lands in the worktree cwd), got deny: %s", d.Reason)
			}
		})
	}
}

// pathOperandCommand is derived from Verbs.WritePrefixes rather than restated
// as a second list, so classification and operand judging cannot drift apart.
// This pins that property directly: every write-prefix command word must be a
// path-operand command, whatever verbs.json later gains. It fails the moment
// anyone replaces the derivation with a hand-maintained set that omits an
// entry -- the exact regression TestLED023_ModeledWriterOperandsStillJudged_PackageManagerRetarget
// above caught behaviorally.
func TestPathOperandCommand_CoversEveryWritePrefixVerb(t *testing.T) {
	v := testVerbs(t)
	if len(v.WritePrefixes) == 0 {
		t.Fatal("write_prefixes is empty, so this test would pass vacuously")
	}
	for _, w := range v.WritePrefixes {
		verb := firstToken(w)
		if verb == "" {
			t.Errorf("write_prefixes entry %q has no command word", w)
			continue
		}
		if !pathOperandCommand(v, verb) {
			t.Errorf("write_prefixes names %q a writer, but pathOperandCommand does not judge its operands", verb)
		}
	}
	// find's writing forms are write_contains-anchored, not write_prefixes, so
	// the derivation alone would miss it.
	if !pathOperandCommand(v, "find") {
		t.Error("find's -delete/-exec forms write the path it names, so its operands must be judged")
	}
	// A command the model names nowhere must NOT have its operands judged --
	// that is LED-023 itself.
	for _, cmd := range []string{"jq", "python3", "awk", "$dat_tools"} {
		if pathOperandCommand(v, cmd) {
			t.Errorf("%q is not a modeled writer; judging its operands is LED-023", cmd)
		}
	}
}
