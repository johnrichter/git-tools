package detect

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// stubGitIgnored returns a GitIgnoredFunc that answers ok, err for exactly
// (root, absPath) and false, nil for every other pair -- so a test states
// only the one path it cares about, and every other call site's own fake
// filesystem stays the only thing driving the rest of the decision.
func stubGitIgnored(root, absPath string, ok bool, err error) GitIgnoredFunc {
	return func(gotRoot, gotAbsPath string) (bool, error) {
		if gotRoot == root && gotAbsPath == absPath {
			return ok, err
		}
		return false, nil
	}
}

func TestDecide_Write_GitignoreExempt_Allowed(t *testing.T) {
	fs := newFakeFS().dir("/repo/.git")
	gitIgnored := stubGitIgnored("/repo", "/repo/prw/index.md", true, nil)
	d := Decide(fs.lstat, fs.readFile, gitIgnored, Verbs{}, nil, Input{
		ToolName: "Write",
		FilePath: "/repo/prw/index.md",
	})
	if d.Deny {
		t.Fatalf("expected allow for a Write matching a committed .gitignore rule, got deny: %s", d.Reason)
	}
}

func TestDecide_Write_GitignoreStubNotIgnored_StillDenied(t *testing.T) {
	fs := newFakeFS().dir("/repo/.git")
	gitIgnored := stubGitIgnored("/repo", "/repo/prw/index.md", false, nil)
	d := Decide(fs.lstat, fs.readFile, gitIgnored, Verbs{}, nil, Input{
		ToolName: "Write",
		FilePath: "/repo/prw/index.md",
	})
	if !d.Deny {
		t.Fatal("expected deny for a Write the stub reports as not ignored, unchanged from today")
	}
}

func TestDecide_Write_GitignoreStubErrors_StillDenied(t *testing.T) {
	fs := newFakeFS().dir("/repo/.git")
	gitIgnored := stubGitIgnored("/repo", "/repo/prw/index.md", true, errors.New("git unavailable"))
	d := Decide(fs.lstat, fs.readFile, gitIgnored, Verbs{}, nil, Input{
		ToolName: "Write",
		FilePath: "/repo/prw/index.md",
	})
	if !d.Deny {
		t.Fatal("expected deny when the gitignore check itself errors: an error must never turn into an allow")
	}
}

func TestDecide_Write_NilGitIgnoredFunc_StillDenied(t *testing.T) {
	fs := newFakeFS().dir("/repo/.git")
	d := Decide(fs.lstat, fs.readFile, nil, Verbs{}, nil, Input{
		ToolName: "Write",
		FilePath: "/repo/prw/index.md",
	})
	if !d.Deny {
		t.Fatal("expected deny with a nil GitIgnoredFunc, matching every pre-existing call site")
	}
}

// The Bash cases below run cwd inside a worktree of /repo, so the cwd leg's
// own, target-agnostic check (decideBash's "ClassWrite in a KindPrimary cwd"
// default case) is already clear on its own merits before SC20's named-path
// rule ever runs. That isolates the one thing under test: namedPathDenial's
// gitignore exemption for a write-class piece naming a target in a
// DIFFERENT repository, /other. A write-class Bash command run with cwd
// directly in an un-worktreed primary checkout is unaffected by this
// exception: it still denies exactly as it does today, since the cwd leg's
// blanket rule does not consult any one piece's named target. The two
// SameRepoPrimaryCheckoutCWD tests below pin that boundary.
func TestDecide_Bash_NamedTargetGitignoreExempt_Allowed(t *testing.T) {
	fs := newFakeFS().dir("/repo/.git").dir("/other/.git").
		file("/repo/.claude/worktrees/wt/.git", "gitdir: /repo/.git/worktrees/wt\n")
	v := testVerbs(t)
	gitIgnored := stubGitIgnored("/other", "/other/prw/index.md", true, nil)
	d := Decide(fs.lstat, fs.readFile, gitIgnored, v, nil, Input{
		ToolName: "Bash",
		CWD:      "/repo/.claude/worktrees/wt",
		Command:  "cp x /other/prw/index.md",
	})
	if d.Deny {
		t.Fatalf("expected allow for a named Bash write target matching a committed .gitignore rule, got deny: %s", d.Reason)
	}
}

func TestDecide_Bash_NamedTargetGitignoreStubNotIgnored_StillDenied(t *testing.T) {
	fs := newFakeFS().dir("/repo/.git").dir("/other/.git").
		file("/repo/.claude/worktrees/wt/.git", "gitdir: /repo/.git/worktrees/wt\n")
	v := testVerbs(t)
	gitIgnored := stubGitIgnored("/other", "/other/prw/index.md", false, nil)
	d := Decide(fs.lstat, fs.readFile, gitIgnored, v, nil, Input{
		ToolName: "Bash",
		CWD:      "/repo/.claude/worktrees/wt",
		Command:  "cp x /other/prw/index.md",
	})
	if !d.Deny {
		t.Fatal("expected deny for a named Bash write target the stub reports as not ignored, unchanged from today")
	}
}

// TestDecide_Bash_SameRepoPrimaryCheckoutCWD_GitignoreDoesNotExempt pins the
// scope boundary above directly: even when the one named write target is
// gitignore-exempt, a write-class command run with cwd itself directly in
// that same, un-worktreed primary checkout still denies, via the cwd leg,
// unchanged from today. Exempting this case too would require the cwd leg
// itself to trust a Bash parse's named-target list as exhaustive, which
// namedPaths's own doc comment does not claim for every command shape.
func TestDecide_Bash_SameRepoPrimaryCheckoutCWD_GitignoreDoesNotExempt(t *testing.T) {
	fs := newFakeFS().dir("/repo/.git")
	v := testVerbs(t)
	gitIgnored := stubGitIgnored("/repo", "/repo/prw/index.md", true, nil)
	d := Decide(fs.lstat, fs.readFile, gitIgnored, v, nil, Input{
		ToolName: "Bash",
		CWD:      "/repo",
		Command:  "echo x > prw/index.md",
	})
	if !d.Deny {
		t.Fatal("expected the cwd leg to still deny a same-repo write-class command run outside a worktree, gitignore exemption notwithstanding")
	}
	// echo is not a path-operand command (LED-023): its own argument "x" is
	// text it prints, never a destination, so namedPaths names only the
	// redirect target here -- which the stub above does exempt -- leaving
	// this command's eventual denial to the cwd leg, the same mechanism
	// TestDecide_Bash_SameRepoPrimaryCheckoutCWD_GitignoreDoesNotExempt_ViaCWDLegOnly
	// below pins for "date > ...". Before that fix, "x" was itself a
	// candidate destination and denied via namedPathDenial instead; this
	// assertion now checks for the cwd leg's own message to catch a
	// regression back to that mistake.
	if !strings.Contains(d.Reason, "may modify") {
		t.Fatalf("expected this command to deny via the cwd leg, got reason: %s", d.Reason)
	}
}

// TestDecide_Bash_SameRepoPrimaryCheckoutCWD_GitignoreDoesNotExempt_ViaCWDLegOnly
// is the corrected pin for the scope boundary described above
// TestDecide_Bash_NamedTargetGitignoreExempt_Allowed: a write-class Bash
// command run with its own cwd directly in
// the same, un-worktreed primary checkout still denies via decideBash's
// target-agnostic cwd leg, even when its ONLY named write target is
// gitignore-exempt. Unlike the test above, this command ("date > ...") names
// no operand besides the redirect target itself, so namedPathDenial has
// nothing left to deny on and the gitignore exemption there succeeds
// (scanBash reports ClassWrite with no denial) -- forcing the eventual
// denial down to decideBash's own cwd-leg default case. The Reason text is
// asserted specifically, because that is the only way to tell this apart
// from a namedPathDenial denial with the same Deny=true outcome: the cwd
// leg's message is "<command> may modify <root> outside a worktree" while
// namedPathDenial's is "...writes into the primary checkout of ... via ...".
func TestDecide_Bash_SameRepoPrimaryCheckoutCWD_GitignoreDoesNotExempt_ViaCWDLegOnly(t *testing.T) {
	fs := newFakeFS().dir("/repo/.git")
	v := testVerbs(t)
	gitIgnored := stubGitIgnored("/repo", "/repo/prw/index.md", true, nil)
	d := Decide(fs.lstat, fs.readFile, gitIgnored, v, nil, Input{
		ToolName: "Bash",
		CWD:      "/repo",
		Command:  "date > prw/index.md",
	})
	if !d.Deny {
		t.Fatal("expected the cwd leg to still deny a same-repo write-class command run outside a worktree, gitignore exemption notwithstanding")
	}
	if strings.Contains(d.Reason, "writes into the primary checkout") {
		t.Fatalf("denied via namedPathDenial instead of the cwd leg -- the gitignore exemption on the sole named target did not take effect as expected: %s", d.Reason)
	}
	if !strings.Contains(d.Reason, "may modify") {
		t.Fatalf("expected the cwd leg's own denial message, got: %s", d.Reason)
	}
}

// TestDecide_Bash_NamedTargetGitignoreExempt_WrongPathStillDenied is a
// precision check on gitignoreExempt's call sites: a stub that answers true
// only for a specific (root, absPath) pair must not accidentally exempt a
// different target that merely lands in the same primary checkout. This
// guards against a call-site bug that passes the wrong path (e.g. cwd
// instead of the resolved target) into gitignoreExempt.
func TestDecide_Bash_NamedTargetGitignoreExempt_WrongPathStillDenied(t *testing.T) {
	fs := newFakeFS().dir("/repo/.git").dir("/other/.git").
		file("/repo/.claude/worktrees/wt/.git", "gitdir: /repo/.git/worktrees/wt\n")
	v := testVerbs(t)
	// The stub only exempts /other/prw/index.md -- this command targets a
	// different file, /other/prw/other.md, in the same repository.
	gitIgnored := stubGitIgnored("/other", "/other/prw/index.md", true, nil)
	d := Decide(fs.lstat, fs.readFile, gitIgnored, v, nil, Input{
		ToolName: "Bash",
		CWD:      "/repo/.claude/worktrees/wt",
		Command:  "date > /other/prw/other.md",
	})
	if !d.Deny {
		t.Fatal("expected deny: the gitignore stub exempts a different path in the same repository, not this command's actual target")
	}
}

// TestDecideSource_ShellsOutToNothing is the compile-out companion to
// GitIgnoredFunc's injection, in the same shape as
// TestDecideSource_ReadsNoEnvironment: SC23 is the first rule whose real answer
// needs git, and the whole point of injecting it as a function is that this
// decision layer still spawns no process and imports no git of its own. A
// future rule that reaches for os/exec here would make the gate untestable
// hermetically and put subprocess behavior in the deny path itself, so the
// invariant is pinned rather than left to convention.
func TestDecideSource_ShellsOutToNothing(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		for _, forbidden := range []string{`"os/exec"`, "exec.Command", "internal/gitexec"} {
			if strings.Contains(string(b), forbidden) {
				t.Errorf("%s references %q; the decision layer must stay hermetic and take git answers through GitIgnoredFunc instead", name, forbidden)
			}
		}
	}
}
