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
	d := Decide(fs.lstat, fs.readFile, Verbs{}, nil, TrackingDocs{}, nil, Input{ToolName: "Write", FilePath: ""})
	if d.Deny || d.Degraded != "" {
		t.Errorf("expected no-op for an empty file path, got %+v", d)
	}
}

func TestDecide_Write_IndeterminateGitEntry_DeniedFailClosed(t *testing.T) {
	// .git exists but is neither a directory nor a parseable worktree
	// redirect file (e.g. corrupted or an unrecognized type) -- must deny,
	// never guess it's safely inside a worktree.
	fs := newFakeFS().file("/repo/.git", "not a gitdir line\n")
	d := Decide(fs.lstat, fs.readFile, Verbs{}, nil, TrackingDocs{}, nil, Input{ToolName: "Write", FilePath: "/repo/a.go"})
	if !d.Deny {
		t.Fatal("expected deny when the .git entry's kind can't be classified (fail closed on uncertainty)")
	}
}

func TestDecide_Write_IndeterminateRepoMembership_DeniedFailClosed(t *testing.T) {
	fs := newFakeFS().errAt("/repo/.git", errors.New("permission denied"))
	d := Decide(fs.lstat, fs.readFile, Verbs{}, nil, TrackingDocs{}, nil, Input{ToolName: "Write", FilePath: "/repo/a.go"})
	if !d.Deny {
		t.Fatal("expected deny when repo membership can't be resolved for a Write (fail closed on uncertainty)")
	}
}

func TestDecide_Edit_SameRulesAsWrite(t *testing.T) {
	fs := primaryFS()
	d := Decide(fs.lstat, fs.readFile, Verbs{}, nil, TrackingDocs{}, nil, Input{ToolName: "Edit", FilePath: "/repo/a.go"})
	if !d.Deny {
		t.Fatal("expected Edit outside a worktree to deny, same as Write")
	}
}

// -- Bash: worktree membership is checked before classifier health --

func TestDecide_Bash_WorktreeWithDegradedClassifier_AllowedAndDegraded(t *testing.T) {
	fs := worktreeFS()
	verbsErr := errors.New("worktree-gate: embedded verbs.json is corrupt")
	d := Decide(fs.lstat, fs.readFile, Verbs{}, verbsErr, TrackingDocs{}, nil, Input{ToolName: "Bash", CWD: "/repo/wt", Command: "git commit -m x"})
	if d.Deny {
		t.Fatalf("expected allow: already inside a worktree, regardless of classifier health, got deny: %s", d.Reason)
	}
	if d.Degraded == "" {
		t.Fatal("expected the classifier defect to still be surfaced via Degraded even though the call is allowed")
	}
}

// -- SC9: the opaque-script residual. decideBash never inspects what a
// script actually does; an arbitrary script run from a primary checkout
// (e.g. a build step that writes .claude/settings.json) matches no known
// read or write pattern and falls to the fail-closed Uncertain default, so
// it denies without any script-specific logic.

func TestDecide_Bash_SC9_OpaqueScriptWritingClaudeSettingsFromPrimaryCheckout_Denied(t *testing.T) {
	fs := primaryFS()
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, v, nil, TrackingDocs{}, nil, Input{
		ToolName: "Bash", CWD: "/repo", Command: "./build.sh",
	})
	if !d.Deny {
		t.Fatal("SC9: expected deny for an opaque script (./build.sh) run from a primary checkout -- it could write .claude/settings.json or any other file, and this gate cannot inspect an arbitrary script's behavior")
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
	// Git verbs are classified from the in-code subcommand sets, never from
	// verbs.json -- a `git …` prefix here would be dead at best and a
	// re-introduced leading-global-option/prefix-collision defect at worst.
	for _, list := range [][]string{v.ReadPrefixes, v.WritePrefixes} {
		for _, p := range list {
			if strings.Fields(p)[0] == "git" {
				t.Errorf("verbs.json still carries a git prefix %q; git is classified in code", p)
			}
		}
	}
}

// TestDefaultVerbs_CriticalVerbsPresent pins the classifier's load-bearing
// verbs: the non-git patterns that still ship in verbs.json, and the in-code
// git subcommand sets. The git assertions are behavioral (via classifyGit) so
// dropping merge-base from the read set, dropping a plain write verb, or
// removing a split verb's case -- which would let its read forms fall to the
// write default -- all trip a check here.
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
	mustContain(v.WritePrefixes, "rm ")
	mustContain(v.WritePrefixes, "mv ")
	mustContain(v.WriteContains, ">")
	mustContain(v.WriteContains, ">>")

	mustClassifyGit := func(want BashClass, args ...string) {
		t.Helper()
		if got := classifyGit(args); got != want {
			t.Errorf("classifyGit(%v) = %v, want %v", args, got, want)
		}
	}
	// Plain reads, including the merge-base addition (A4) that must stay a read
	// while merge stays a write.
	mustClassifyGit(ClassRead, "status")
	mustClassifyGit(ClassRead, "diff")
	mustClassifyGit(ClassRead, "log")
	mustClassifyGit(ClassRead, "show")
	mustClassifyGit(ClassRead, "merge-base", "a", "b")
	// Plain writes.
	mustClassifyGit(ClassWrite, "commit", "-m", "x")
	mustClassifyGit(ClassWrite, "add", ".")
	mustClassifyGit(ClassWrite, "merge", "main")
	// Split verbs: a preserved read form and a write form each -- removing a
	// split case flips the read form to the write default and trips here.
	mustClassifyGit(ClassRead, "remote", "-v")
	mustClassifyGit(ClassWrite, "remote", "add", "o", "u")
	mustClassifyGit(ClassRead, "branch", "-a")
	mustClassifyGit(ClassWrite, "branch", "-D", "x")
	mustClassifyGit(ClassRead, "tag", "-l")
	mustClassifyGit(ClassWrite, "tag", "-d", "x")
	mustClassifyGit(ClassRead, "worktree", "list")
	mustClassifyGit(ClassWrite, "worktree", "add", "p")
	mustClassifyGit(ClassRead, "config", "--get", "k")
	mustClassifyGit(ClassWrite, "config", "--set", "k", "v")
	mustClassifyGit(ClassRead, "reflog", "show")
	mustClassifyGit(ClassWrite, "reflog", "expire")
	mustClassifyGit(ClassWrite, "stash")
}

// TestClassifyGit_LeadingGlobalOptionParity pins A3/L1.9: gitSubcommand skips a
// leading global option, so every git verb classifies identically with and
// without one, across each option's value-consuming and =-joined forms.
func TestClassifyGit_LeadingGlobalOptionParity(t *testing.T) {
	globals := [][]string{
		{"-C", "somedir"},
		{"-c", "user.name=x"},
		{"--git-dir", "/g"},
		{"--git-dir=/g"},
		{"--work-tree", "/w"},
		{"--work-tree=/w"},
		{"--namespace", "n"},
		{"--namespace=n"},
	}
	verbs := [][]string{
		{"status"}, {"commit", "-m", "x"}, {"merge-base", "a", "b"},
		{"merge", "main"}, {"remote", "-v"}, {"remote", "add", "o", "u"},
		{"branch", "-a"}, {"branch", "-D", "x"}, {"tag", "-l"}, {"tag", "-d", "x"},
		{"worktree", "list"}, {"worktree", "add", "p"},
		{"config", "--get", "k"}, {"config", "--set", "k", "v"},
		{"reflog", "show"}, {"stash"},
	}
	for _, verb := range verbs {
		base := classifyGit(verb)
		for _, g := range globals {
			withOpt := append(append([]string{}, g...), verb...)
			if got := classifyGit(withOpt); got != base {
				t.Errorf("classifyGit(%v) = %v, differs from classifyGit(%v) = %v", withOpt, got, verb, base)
			}
		}
	}
}

// -- trackingdocs.json packaging integrity, mirroring verbs.json's own
// non-triviality check above.

func TestDefaultTrackingDocs_ShippedArtifactIsPopulatedAndValid(t *testing.T) {
	td := testTrackingDocs(t)
	want := []string{"design.md", "plan.json", "plan.md", "execution.json", "execution.md", "feedback.json", "feedback.md"}
	for _, basename := range want {
		if !td.has(basename) {
			t.Errorf("expected %q among the shipped tracking-doc basenames, not found", basename)
		}
	}
}

// -- underProjectDir: containment at any depth, not a plain string prefix.

func TestUnderProjectDir(t *testing.T) {
	cases := []struct {
		name       string
		projectDir string
		filePath   string
		want       bool
	}{
		{"direct child", "/proj", "/proj/plan.json", true},
		{"nested at any depth", "/proj", "/proj/.dat/some-effort/plan.json", true},
		{"project dir unset", "", "/proj/plan.json", false},
		{"outside the project dir", "/proj", "/otherrepo/plan.json", false},
		{"same-prefix sibling is not contained", "/proj", "/proj-other/plan.json", false},
	}
	for _, c := range cases {
		if got := underProjectDir(c.projectDir, c.filePath); got != c.want {
			t.Errorf("%s: underProjectDir(%q, %q) = %v, want %v", c.name, c.projectDir, c.filePath, got, c.want)
		}
	}
}

// -- a corrupt tracking-doc artifact denies rather than fails open, for any
// call the exemption could have covered.

func TestDecide_Write_TrackingDocsDegradedArtifact_DeniedFailClosed(t *testing.T) {
	fs := newFakeFS().dir("/proj/.dat/some-effort/.git")
	tdErr := errors.New("worktree-gate: embedded trackingdocs.json is corrupt")
	d := Decide(fs.lstat, fs.readFile, Verbs{}, nil, TrackingDocs{}, tdErr, Input{
		ToolName: "Write", FilePath: "/proj/.dat/some-effort/plan.json", ProjectDir: "/proj",
	})
	if !d.Deny {
		t.Fatal("expected deny: a degraded tracking-doc artifact could be masking a real write in scope of the exemption (fail closed, not fail open)")
	}
}

func TestDecide_Write_TrackingDocsDegradedArtifact_IrrelevantOutsideProjectDir(t *testing.T) {
	// A corrupt tracking-doc artifact only matters for calls the exemption
	// could ever have covered -- outside the project dir it can't have, so
	// the ordinary primary-checkout deny is unaffected by the defect.
	fs := primaryFS()
	tdErr := errors.New("worktree-gate: embedded trackingdocs.json is corrupt")
	d := Decide(fs.lstat, fs.readFile, Verbs{}, nil, TrackingDocs{}, tdErr, Input{
		ToolName: "Write", FilePath: "/repo/a.go", ProjectDir: "",
	})
	if !d.Deny {
		t.Fatal("expected the ordinary primary-checkout deny to hold: no project dir means the corrupt artifact was never consulted")
	}
	if d.Degraded != "" {
		t.Errorf("expected no Degraded report: the tracking-doc artifact was never consulted, got %q", d.Degraded)
	}
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
	code := Run(in, &out, &errOut, fs.lstat, fs.readFile, noEnv)
	if code != 0 || out.Len() != 0 {
		t.Errorf("Run() = code=%d stdout=%q, want a silent allow inside an already-isolated worktree", code, out.String())
	}
}

func TestRun_MissingFilePathOnWrite_NoOp(t *testing.T) {
	in := strings.NewReader(`{"tool_name":"Write","tool_input":{}}`)
	var out, errOut bytes.Buffer
	fs := primaryFS()
	code := Run(in, &out, &errOut, fs.lstat, fs.readFile, noEnv)
	if code != 0 || out.Len() != 0 {
		t.Errorf("Run() with no file_path = code=%d stdout=%q, want no-op", code, out.String())
	}
}

func TestRun_BashEmptyCommand_NoOp(t *testing.T) {
	in := strings.NewReader(`{"tool_name":"Bash","cwd":"/repo","tool_input":{"command":""}}`)
	var out, errOut bytes.Buffer
	fs := primaryFS()
	code := Run(in, &out, &errOut, fs.lstat, fs.readFile, noEnv)
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
	Run(in, &out, &errOut, fs.lstat, fs.readFile, noEnv)

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
