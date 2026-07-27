package detect

import (
	"errors"
	"testing"
)

func primaryFS() *fakeFS { return newFakeFS().dir("/repo/.git") }
func worktreeFS() *fakeFS {
	return newFakeFS().file("/repo/wt/.git", "gitdir: /repo/.git/worktrees/wt\n")
}

func TestDecide_WriteOutsideWorktree_Denied(t *testing.T) {
	fs := primaryFS()
	d := Decide(fs.lstat, fs.readFile, Verbs{}, nil, Input{ToolName: "Write", FilePath: "/repo/a.go"})
	if !d.Deny {
		t.Fatal("expected deny for a Write into the primary checkout")
	}
}

func TestDecide_WriteInsideWorktree_Allowed(t *testing.T) {
	fs := worktreeFS()
	d := Decide(fs.lstat, fs.readFile, Verbs{}, nil, Input{ToolName: "Write", FilePath: "/repo/wt/a.go"})
	if d.Deny {
		t.Fatalf("expected allow for a Write inside a worktree, got deny: %s", d.Reason)
	}
}

func TestDecide_WriteOutsideAnyRepo_Allowed(t *testing.T) {
	fs := newFakeFS()
	d := Decide(fs.lstat, fs.readFile, Verbs{}, nil, Input{ToolName: "Write", FilePath: "/tmp/scratch.txt"})
	if d.Deny {
		t.Fatalf("expected allow for a write confidently outside any repo, got deny: %s", d.Reason)
	}
}

func TestDecide_Bash_ReadInPrimaryCheckout_Allowed(t *testing.T) {
	fs := primaryFS()
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, v, nil, Input{ToolName: "Bash", CWD: "/repo", Command: "git status"})
	if d.Deny {
		t.Fatalf("expected allow for a read in the primary checkout, got deny: %s", d.Reason)
	}
}

func TestDecide_Bash_WriteInPrimaryCheckout_Denied(t *testing.T) {
	fs := primaryFS()
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, v, nil, Input{ToolName: "Bash", CWD: "/repo", Command: "git commit -m x"})
	if !d.Deny {
		t.Fatal("expected deny for a write-classified Bash command in the primary checkout")
	}
}

func TestDecide_Bash_WriteInWorktree_Allowed(t *testing.T) {
	fs := worktreeFS()
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, v, nil, Input{ToolName: "Bash", CWD: "/repo/wt", Command: "git commit -m x"})
	if d.Deny {
		t.Fatalf("expected allow for a write in an already-isolated worktree, got deny: %s", d.Reason)
	}
}

func TestDecide_Bash_UncertainInPrimaryCheckout_Denied(t *testing.T) {
	fs := primaryFS()
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, v, nil, Input{ToolName: "Bash", CWD: "/repo", Command: "some-unlisted-tool"})
	if !d.Deny {
		t.Fatal("expected deny for an unclassifiable command in the primary checkout (fail closed on uncertainty)")
	}
}

func TestDecide_Bash_OutsideAnyRepo_Allowed(t *testing.T) {
	fs := newFakeFS()
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, v, nil, Input{ToolName: "Bash", CWD: "/tmp", Command: "git commit -m x"})
	if d.Deny {
		t.Fatalf("expected allow for a Bash call confidently outside any repo, got deny: %s", d.Reason)
	}
}

func TestDecide_Bash_NoCWD_DeniedFailClosed(t *testing.T) {
	fs := primaryFS()
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, v, nil, Input{ToolName: "Bash", Command: "git status"})
	if !d.Deny {
		t.Fatal("expected deny when no working directory is reported (fail closed on uncertainty)")
	}
}

func TestDecide_Bash_IndeterminateRepoMembership_DeniedFailClosed(t *testing.T) {
	fs := newFakeFS().errAt("/repo/.git", errors.New("permission denied"))
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, v, nil, Input{ToolName: "Bash", CWD: "/repo", Command: "git status"})
	if !d.Deny {
		t.Fatal("expected deny when repo membership can't be resolved (fail closed on uncertainty)")
	}
}

func TestDecide_Bash_DegradedClassifier_FailsOpenAndReportsDegraded(t *testing.T) {
	fs := primaryFS()
	verbsErr := errors.New("worktree-gate: embedded verbs.json is corrupt")
	d := Decide(fs.lstat, fs.readFile, Verbs{}, verbsErr, Input{ToolName: "Bash", CWD: "/repo", Command: "git commit -m x"})
	if d.Deny {
		t.Fatalf("expected fail-open on a degraded classifier artifact, got deny: %s", d.Reason)
	}
	if d.Degraded == "" {
		t.Fatal("expected Degraded to be set so the defect is surfaced loudly")
	}
}

func TestDecide_UnknownTool_NoOp(t *testing.T) {
	fs := primaryFS()
	d := Decide(fs.lstat, fs.readFile, Verbs{}, nil, Input{ToolName: "Read", FilePath: "/repo/a.go"})
	if d.Deny || d.Degraded != "" {
		t.Errorf("expected a no-op Decision for a tool this gate doesn't govern, got %+v", d)
	}
}
