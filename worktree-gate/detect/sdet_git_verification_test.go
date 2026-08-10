package detect

import "testing"

// Independent adversarial verification of the git classification migration
// out of verbs.json into in-code subcommand sets. These cases probe forms
// the implementer's own suite does not already cover, plus a direct
// re-check of the read/write splits' literal examples through the full
// Decide path (not just classifyGit in isolation).

// `git -C <dir> status` must be allowed via the full Decide path from a
// primary checkout -- not just at the classifyGit unit level.
func TestSDET_Decide_GitDashCStatus_AllowedAtPrimary(t *testing.T) {
	fs := primaryFS()
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, v, nil, TrackingDocs{}, nil, Input{
		ToolName: "Bash", CWD: "/repo", Command: "git -C /repo status",
	})
	if d.Deny {
		t.Fatalf("git -C <dir> status must be allowed at a primary checkout, got deny: %s", d.Reason)
	}
}

// `git merge-base <a> <b>` must be allowed via the full Decide path.
func TestSDET_Decide_GitMergeBase_AllowedAtPrimary(t *testing.T) {
	fs := primaryFS()
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, v, nil, TrackingDocs{}, nil, Input{
		ToolName: "Bash", CWD: "/repo", Command: "git merge-base main feature",
	})
	if d.Deny {
		t.Fatalf("git merge-base <a> <b> must be allowed at a primary checkout, got deny: %s", d.Reason)
	}
}

// git remote add / git branch -D / git tag -d must be denied outside a
// worktree via the full Decide path (not just classifyGit).
func TestSDET_Decide_GitWriteForms_DeniedAtPrimary(t *testing.T) {
	v := testVerbs(t)
	cases := []string{
		"git remote add origin https://example.com/r.git",
		"git branch -D stale",
		"git tag -d v1.0",
		"git branch -d stale",
	}
	for _, cmd := range cases {
		fs := primaryFS()
		d := Decide(fs.lstat, fs.readFile, v, nil, TrackingDocs{}, nil, Input{
			ToolName: "Bash", CWD: "/repo", Command: cmd,
		})
		if !d.Deny {
			t.Errorf("%q must be denied at a primary checkout, got allow", cmd)
		}
	}
}

// Read forms of remote/branch/tag must stay allowed via the full Decide
// path, including the subtlety that `git tag -v` is a signature-verify
// read, not a verbose flag.
func TestSDET_Decide_GitReadForms_AllowedAtPrimary(t *testing.T) {
	v := testVerbs(t)
	cases := []string{
		"git remote",
		"git remote -v",
		"git remote show origin",
		"git branch",
		"git branch -a",
		"git branch -r",
		"git branch -v",
		"git branch --list",
		"git branch --show-current",
		"git branch --contains abc123",
		"git tag",
		"git tag -l 'v1.*'",
		"git tag -n",
		"git tag -v v1.0",
	}
	for _, cmd := range cases {
		fs := primaryFS()
		d := Decide(fs.lstat, fs.readFile, v, nil, TrackingDocs{}, nil, Input{
			ToolName: "Bash", CWD: "/repo", Command: cmd,
		})
		if d.Deny {
			t.Errorf("%q must stay allowed (read) at a primary checkout, got deny: %s", cmd, d.Reason)
		}
	}
}

// A leading global option must never change classification, exercised
// through glued and stacked forms.
func TestSDET_ClassifyGit_GluedAndStackedGlobalOptions(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want BashClass
	}{
		{"glued -c value", []string{"-cuser.name=x", "status"}, ClassRead},
		{"glued -c value on a write", []string{"-cuser.name=x", "commit", "-m", "x"}, ClassWrite},
		{"stacked -C and -c", []string{"-C", "dir", "-c", "user.name=x", "status"}, ClassRead},
		{"stacked globals on merge-base", []string{"-C", "dir", "-c", "user.name=x", "merge-base", "a", "b"}, ClassRead},
		{"stacked globals on a write split verb", []string{"-C", "dir", "branch", "-D", "x"}, ClassWrite},
	}
	for _, c := range cases {
		if got := classifyGit(c.args); got != c.want {
			t.Errorf("%s: classifyGit(%v) = %v, want %v", c.name, c.args, got, c.want)
		}
	}
}

// Migrated verbs (worktree, config, reflog, stash) must preserve their
// documented read/write split, and an unrecognized subcommand anywhere in
// git must default to write, never uncertain.
func TestSDET_ClassifyGit_MigratedVerbsAndUnknownSubcommand(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want BashClass
	}{
		{"worktree list stays read", []string{"worktree", "list"}, ClassRead},
		{"worktree add is write", []string{"worktree", "add", "p"}, ClassWrite},
		{"worktree remove is write", []string{"worktree", "remove", "p"}, ClassWrite},
		{"worktree prune is write", []string{"worktree", "prune"}, ClassWrite},
		{"worktree unrecognized subcommand is write", []string{"worktree", "lock"}, ClassWrite},
		{"config --get is read", []string{"config", "--get", "k"}, ClassRead},
		{"config --list is read", []string{"config", "--list"}, ClassRead},
		{"config -l is read", []string{"config", "-l"}, ClassRead},
		{"config --get-regexp stays write", []string{"config", "--get-regexp", "k.*"}, ClassWrite},
		{"config --get-all stays write", []string{"config", "--get-all", "k"}, ClassWrite},
		{"config --unset is write", []string{"config", "--unset", "k"}, ClassWrite},
		{"reflog show is read", []string{"reflog", "show"}, ClassRead},
		{"reflog bare is write", []string{"reflog"}, ClassWrite},
		{"reflog expire is write", []string{"reflog", "expire"}, ClassWrite},
		{"reflog delete is write", []string{"reflog", "delete", "HEAD@{0}"}, ClassWrite},
		{"stash bare is write", []string{"stash"}, ClassWrite},
		{"stash list stays write, not a read exemption", []string{"stash", "list"}, ClassWrite},
		{"stash show stays write, not a read exemption", []string{"stash", "show"}, ClassWrite},
		{"stash pop is write", []string{"stash", "pop"}, ClassWrite},
		{"unknown git subcommand defaults to write, not uncertain", []string{"bisect", "start"}, ClassWrite},
		{"another unknown subcommand defaults to write", []string{"gc"}, ClassWrite},
	}
	for _, c := range cases {
		if got := classifyGit(c.args); got != c.want {
			t.Errorf("%s: classifyGit(%v) = %v, want %v", c.name, c.args, got, c.want)
		}
	}
}

// The unrecognized-subcommand default must also respect a leading global
// option, not just the explicitly mapped verbs.
func TestSDET_ClassifyGit_UnknownSubcommandWriteDefault_GlobalOptionParity(t *testing.T) {
	base := classifyGit([]string{"bisect", "start"})
	withOpt := classifyGit([]string{"-C", "dir", "bisect", "start"})
	if base != ClassWrite || withOpt != ClassWrite {
		t.Errorf("unknown subcommand must default to write with and without a leading global option: bare=%v, with -C=%v", base, withOpt)
	}
}

// A positional operand on branch/tag triggers write only when no listing
// flag is present; combined with a listing flag it stays read; an
// unrecognized flag also classifies as write, never uncertain.
func TestSDET_ClassifyGit_RefEditPositionalAndUnrecognizedFlag(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want BashClass
	}{
		{"branch positional alone creates a ref: write", []string{"branch", "newbranch"}, ClassWrite},
		{"branch positional with listing flag stays read", []string{"branch", "--contains", "abc123"}, ClassRead},
		{"branch unrecognized flag is write, not uncertain", []string{"branch", "--unknown-flag"}, ClassWrite},
		{"branch move flag is write", []string{"branch", "-m", "old", "new"}, ClassWrite},
		// A modifier flag (-v/-vv/--verbose/--sort/--format) lists on its own
		// but does NOT force list mode, so with an operand git still CREATES the
		// ref -- these must classify write, not be neutralized by the flag.
		{"branch -v operand still creates a ref: write", []string{"branch", "-v", "newbranch"}, ClassWrite},
		{"branch -vv operand still creates a ref: write", []string{"branch", "-vv", "newbranch"}, ClassWrite},
		{"branch --sort operand still creates a ref: write", []string{"branch", "--sort=-committerdate", "newbranch"}, ClassWrite},
		{"branch --format operand still creates a ref: write", []string{"branch", "--format=%(refname)", "newbranch"}, ClassWrite},
		{"branch -v alone is a verbose listing: read", []string{"branch", "-v"}, ClassRead},
		{"tag positional alone creates a tag: write", []string{"tag", "v2.0"}, ClassWrite},
		{"tag with annotate flag is write", []string{"tag", "-a", "v2.0", "-m", "msg"}, ClassWrite},
		{"tag unrecognized flag is write, not uncertain", []string{"tag", "--unknown-flag"}, ClassWrite},
		{"tag --sort operand still creates a tag: write", []string{"tag", "--sort=-creatordate", "v2.0"}, ClassWrite},
		{"tag --format operand still creates a tag: write", []string{"tag", "--format=%(refname)", "v2.0"}, ClassWrite},
		{"tag --column operand still creates a tag: write", []string{"tag", "--column", "v2.0"}, ClassWrite},
		{"tag --sort alone is a sorted listing: read", []string{"tag", "--sort=-creatordate"}, ClassRead},
	}
	for _, c := range cases {
		if got := classifyGit(c.args); got != c.want {
			t.Errorf("%s: classifyGit(%v) = %v, want %v", c.name, c.args, got, c.want)
		}
	}
}

// verbs.json must carry zero git prefixes post-migration, re-checked against
// the parsed struct returned by the real embedded artifact loader rather
// than a test-only fixture.
func TestSDET_VerbsJSON_NoGitPrefixesInShippedArtifact(t *testing.T) {
	v, err := DefaultVerbs()
	if err != nil {
		t.Fatalf("DefaultVerbs() error: %v", err)
	}
	for _, p := range append(append([]string{}, v.ReadPrefixes...), v.WritePrefixes...) {
		if len(p) >= 3 && p[:3] == "git" {
			t.Errorf("verbs.json still carries a git-prefixed pattern %q", p)
		}
	}
	if len(v.ReadPrefixes) != 15 {
		t.Errorf("expected 15 non-git read_prefixes, got %d: %v", len(v.ReadPrefixes), v.ReadPrefixes)
	}
}
