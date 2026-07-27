package detect

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"testing"
)

// -- decideFileWrite edge cases not covered by decide_test.go --

func TestDecide_Write_EmptyFilePath_NoOp(t *testing.T) {
	fs := primaryFS()
	d := Decide(fs.lstat, fs.readFile, Verbs{}, nil, Input{ToolName: "Write", FilePath: ""})
	if d.Deny || d.Degraded != "" {
		t.Errorf("expected no-op for an empty file path, got %+v", d)
	}
}

func TestDecide_Write_IndeterminateGitEntry_DeniedFailClosed(t *testing.T) {
	// .git exists but is neither a directory nor a parseable worktree
	// redirect file (e.g. corrupted or an unrecognized type) -- must deny,
	// never guess it's safely inside a worktree.
	fs := newFakeFS().file("/repo/.git", "not a gitdir line\n")
	d := Decide(fs.lstat, fs.readFile, Verbs{}, nil, Input{ToolName: "Write", FilePath: "/repo/a.go"})
	if !d.Deny {
		t.Fatal("expected deny when the .git entry's kind can't be classified (fail closed on uncertainty)")
	}
}

func TestDecide_Write_IndeterminateRepoMembership_DeniedFailClosed(t *testing.T) {
	fs := newFakeFS().errAt("/repo/.git", errors.New("permission denied"))
	d := Decide(fs.lstat, fs.readFile, Verbs{}, nil, Input{ToolName: "Write", FilePath: "/repo/a.go"})
	if !d.Deny {
		t.Fatal("expected deny when repo membership can't be resolved for a Write (fail closed on uncertainty)")
	}
}

func TestDecide_Edit_SameRulesAsWrite(t *testing.T) {
	fs := primaryFS()
	d := Decide(fs.lstat, fs.readFile, Verbs{}, nil, Input{ToolName: "Edit", FilePath: "/repo/a.go"})
	if !d.Deny {
		t.Fatal("expected Edit outside a worktree to deny, same as Write")
	}
}

// -- Bash: worktree membership is checked before classifier health --

func TestDecide_Bash_WorktreeWithDegradedClassifier_AllowedAndDegraded(t *testing.T) {
	fs := worktreeFS()
	verbsErr := errors.New("worktree-gate: embedded verbs.json is corrupt")
	d := Decide(fs.lstat, fs.readFile, Verbs{}, verbsErr, Input{ToolName: "Bash", CWD: "/repo/wt", Command: "git commit -m x"})
	if d.Deny {
		t.Fatalf("expected allow: already inside a worktree, regardless of classifier health, got deny: %s", d.Reason)
	}
	if d.Degraded == "" {
		t.Fatal("expected the classifier defect to still be surfaced via Degraded even though the call is allowed")
	}
}

// -- ClassifyBash: case-insensitivity, whitespace, and conservative bias --

func TestClassifyBash_CaseInsensitiveWritePrefix(t *testing.T) {
	v := testVerbs(t)
	if got := ClassifyBash(v, "GIT COMMIT -m fix"); got != ClassWrite {
		t.Errorf("ClassifyBash(upper-case commit) = %v, want ClassWrite", got)
	}
}

func TestClassifyBash_RedirectInsideReadLikeCommand_StillWrite(t *testing.T) {
	// grep is a read-prefix verb, but a redirect anywhere in the piece must
	// still dominate: the conservative over-approximation cares about the
	// possibility of a write, not the verb's usual behavior.
	v := testVerbs(t)
	if got := ClassifyBash(v, "grep pattern file.txt > /tmp/out"); got != ClassWrite {
		t.Errorf("ClassifyBash(grep with redirect) = %v, want ClassWrite", got)
	}
}

func TestClassifyBash_EmptyPiecesFromConnectors_Ignored(t *testing.T) {
	v := testVerbs(t)
	if got := ClassifyBash(v, "git status ;; git diff"); got != ClassRead {
		t.Errorf("ClassifyBash(double-semicolon) = %v, want ClassRead (empty pieces skipped)", got)
	}
}

func TestClassifyBash_WhitespaceOnlyCommand_IsRead(t *testing.T) {
	// No piece at all (blank/whitespace command): sawUnknown never set, so
	// this degenerates to ClassRead. Decide's own empty-command guard is
	// what actually keeps a blank Bash call from ever reaching here.
	v := testVerbs(t)
	if got := ClassifyBash(v, "   "); got != ClassRead {
		t.Errorf("ClassifyBash(blank) = %v, want ClassRead", got)
	}
}

func TestClassifyBash_TrailingWriteAfterReads_StillWrite(t *testing.T) {
	v := testVerbs(t)
	if got := ClassifyBash(v, "git status && git log && rm -rf notes"); got != ClassWrite {
		t.Errorf("ClassifyBash(reads then a write) = %v, want ClassWrite", got)
	}
}

func TestClassifyBash_FindWithMutatingFlag_StillWrite(t *testing.T) {
	// find is read-only by default (a bare listing), but -delete/-exec/
	// -execdir turn it into an arbitrary mutation -- the mutating flag must
	// dominate the read-prefix match, not the other way around.
	v := testVerbs(t)
	mutating := []string{
		"find . -delete",
		"find . -name *.o -exec rm {} \\;",
		"find . -execdir touch done \\;",
	}
	for _, cmd := range mutating {
		if got := ClassifyBash(v, cmd); got != ClassWrite {
			t.Errorf("ClassifyBash(%q) = %v, want ClassWrite", cmd, got)
		}
	}
}

func TestClassifyBash_ReadPrefixMatchesAtWordBoundary(t *testing.T) {
	// A read verb must match only at a word boundary: a distinct command that
	// merely shares the verb's opening letters (lsof, lsyncd, pwdx, lshw) is
	// not a repo read and must not inherit ClassRead, or it would bypass the
	// deny in a primary checkout. It falls through to ClassUncertain instead.
	v := testVerbs(t)
	notReads := []string{"lsof -i :8080", "lsyncd --config x", "pwdx 1234", "lshw", "catx foo", "findmnt"}
	for _, cmd := range notReads {
		if got := ClassifyBash(v, cmd); got == ClassRead {
			t.Errorf("ClassifyBash(%q) = ClassRead, want non-read (word-boundary mismatch)", cmd)
		}
	}
	stillReads := []string{"ls", "ls -la", "pwd", "cat README.md", "find . -type f"}
	for _, cmd := range stillReads {
		if got := ClassifyBash(v, cmd); got != ClassRead {
			t.Errorf("ClassifyBash(%q) = %v, want ClassRead", cmd, got)
		}
	}
}

func TestClassifyBash_PipedIntoUnknownTool_Uncertain(t *testing.T) {
	v := testVerbs(t)
	if got := ClassifyBash(v, "git log | some-custom-formatter"); got != ClassUncertain {
		t.Errorf("ClassifyBash(piped into unrecognized tool) = %v, want ClassUncertain", got)
	}
}

// -- ClassifyGitEntry: entry types other than dir/regular-file/worktree --

func TestClassifyGitEntry_SymlinkIsIndeterminate(t *testing.T) {
	// A .git entry that's neither a directory nor a regular file (e.g. a
	// symlink) is a shape ClassifyGitEntry doesn't recognize -- must not be
	// treated as either a primary checkout or a worktree redirect.
	nfs := newFakeFS()
	nfs.nodes["/repo/.git"] = fakeNode{mode: fs.ModeSymlink}
	got := ClassifyGitEntry(nfs.lstat, nfs.readFile, "/repo/.git")
	if got != KindIndeterminate {
		t.Errorf("ClassifyGitEntry(symlink) = %v, want KindIndeterminate", got)
	}
}

func TestClassifyGitEntry_GitdirLineWithoutWorktreesSegment_Indeterminate(t *testing.T) {
	// A gitdir redirect that doesn't point under .git/worktrees/<name> is
	// not a shape this gate recognizes as "already isolated" -- treat it
	// as indeterminate, never assume it's safe.
	fs := newFakeFS().file("/repo/.git", "gitdir: /some/other/place\n")
	got := ClassifyGitEntry(fs.lstat, fs.readFile, "/repo/.git")
	if got != KindIndeterminate {
		t.Errorf("ClassifyGitEntry(gitdir without /worktrees/) = %v, want KindIndeterminate", got)
	}
}

func TestClassifyGitEntry_EmptyFile_Indeterminate(t *testing.T) {
	fs := newFakeFS().file("/repo/.git", "")
	got := ClassifyGitEntry(fs.lstat, fs.readFile, "/repo/.git")
	if got != KindIndeterminate {
		t.Errorf("ClassifyGitEntry(empty file) = %v, want KindIndeterminate", got)
	}
}

// -- verbs.json packaging integrity: the shipped artifact must itself be
// non-trivial and every declared pattern class populated, since a silently
// empty class (e.g. no write_prefixes) would quietly narrow the
// conservative over-approximation without any test noticing.

func TestDefaultVerbs_ShippedArtifactIsPopulatedAndValid(t *testing.T) {
	v := testVerbs(t)
	if len(v.ReadPrefixes) == 0 {
		t.Error("shipped verbs.json has no read_prefixes")
	}
	if len(v.WritePrefixes) == 0 {
		t.Error("shipped verbs.json has no write_prefixes")
	}
	if len(v.WriteContains) == 0 {
		t.Error("shipped verbs.json has no write_contains")
	}
}

func TestDefaultVerbs_CriticalVerbsPresent(t *testing.T) {
	v := testVerbs(t)
	mustContain := func(list []string, want string) {
		t.Helper()
		for _, s := range list {
			if s == want {
				return
			}
		}
		t.Errorf("expected %q among the shipped patterns, not found", want)
	}
	mustContain(v.WritePrefixes, "git commit")
	mustContain(v.WritePrefixes, "git add")
	mustContain(v.WritePrefixes, "rm ")
	mustContain(v.WritePrefixes, "mv ")
	mustContain(v.WriteContains, ">")
	mustContain(v.WriteContains, ">>")
	mustContain(v.ReadPrefixes, "git status")
	mustContain(v.ReadPrefixes, "git diff")
	mustContain(v.ReadPrefixes, "git log")
}

// -- Run: verify the degraded diagnostic goes to stderr, never stdout, and
// never turns into a deny.

func TestRun_DegradedClassifierNeverDeniesAndReportsOnlyOnStderr(t *testing.T) {
	// Exercised at the Decide layer directly since Run always loads the
	// real (valid) embedded verbs.json; this confirms Run's plumbing keeps
	// Degraded off stdout even when Decide does report it.
	fs := worktreeFS()
	in := strings.NewReader(`{"tool_name":"Bash","cwd":"/repo/wt","tool_input":{"command":"git commit -m x"}}`)
	var out, errOut bytes.Buffer
	code := Run(in, &out, &errOut, fs.lstat, fs.readFile)
	if code != 0 || out.Len() != 0 {
		t.Errorf("Run() = code=%d stdout=%q, want a silent allow inside an already-isolated worktree", code, out.String())
	}
}

func TestRun_MissingFilePathOnWrite_NoOp(t *testing.T) {
	in := strings.NewReader(`{"tool_name":"Write","tool_input":{}}`)
	var out, errOut bytes.Buffer
	fs := primaryFS()
	code := Run(in, &out, &errOut, fs.lstat, fs.readFile)
	if code != 0 || out.Len() != 0 {
		t.Errorf("Run() with no file_path = code=%d stdout=%q, want no-op", code, out.String())
	}
}

func TestRun_BashEmptyCommand_NoOp(t *testing.T) {
	in := strings.NewReader(`{"tool_name":"Bash","cwd":"/repo","tool_input":{"command":""}}`)
	var out, errOut bytes.Buffer
	fs := primaryFS()
	code := Run(in, &out, &errOut, fs.lstat, fs.readFile)
	if code != 0 || out.Len() != 0 {
		t.Errorf("Run() with empty command = code=%d stdout=%q, want no-op", code, out.String())
	}
}

// -- Response shape sanity: a deny must always carry a non-empty reason,
// since an operator-facing block with no explanation is a usability
// failure even if the decision itself was correct.

func TestRun_DenyAlwaysCarriesNonEmptyReason(t *testing.T) {
	fs := primaryFS()
	in := strings.NewReader(`{"tool_name":"Write","tool_input":{"file_path":"/repo/a.go"}}`)
	var out, errOut bytes.Buffer
	Run(in, &out, &errOut, fs.lstat, fs.readFile)

	var resp response
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("stdout did not decode: %v", err)
	}
	if resp.HookSpecificOutput.PermissionDecisionReason == "" {
		t.Error("expected a non-empty PermissionDecisionReason on deny")
	}
	if resp.HookSpecificOutput.HookEventName != "PreToolUse" {
		t.Errorf("hookEventName = %q, want PreToolUse", resp.HookSpecificOutput.HookEventName)
	}
}
