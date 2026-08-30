// Tests for IsIgnoredByCommittedGitignore, driven against scratch
// repositories built with scratchRepo (defined in gitexec_test.go). Each case
// exercises one of the narrow answers the function must give: ignored by a
// committed `.gitignore`, or not -- because the match traces elsewhere, the
// rule was never committed, the committed file since changed, the path is
// not ignored at all, or the target is not a repository.
package gitexec

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// runGitOrFatal runs git args in dir, failing the test on a non-zero exit --
// the setup steps below need every command to succeed, unlike the assertions
// under test.
func runGitOrFatal(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

func writeFileOrFatal(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestIsIgnoredByCommittedGitignore_RootMatch(t *testing.T) {
	dir := scratchRepo(t)
	writeFileOrFatal(t, filepath.Join(dir, ".gitignore"), "*.log\n")
	runGitOrFatal(t, dir, "add", ".gitignore")
	runGitOrFatal(t, dir, "commit", "-q", "-m", "ignore logs")

	ignored, err := IsIgnoredByCommittedGitignore(context.Background(), dir, "app.log")
	if err != nil {
		t.Fatal(err)
	}
	if !ignored {
		t.Fatalf("app.log against a committed root .gitignore = false, want true")
	}
}

func TestIsIgnoredByCommittedGitignore_NestedMatch(t *testing.T) {
	dir := scratchRepo(t)
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileOrFatal(t, filepath.Join(dir, "sub", ".gitignore"), "*.tmp\n")
	runGitOrFatal(t, dir, "add", "sub/.gitignore")
	runGitOrFatal(t, dir, "commit", "-q", "-m", "ignore sub tmp files")

	ignored, err := IsIgnoredByCommittedGitignore(context.Background(), dir, "sub/file.tmp")
	if err != nil {
		t.Fatal(err)
	}
	if !ignored {
		t.Fatalf("sub/file.tmp against a committed nested .gitignore = false, want true")
	}
}

func TestIsIgnoredByCommittedGitignore_InfoExcludeOnlyDoesNotCount(t *testing.T) {
	dir := scratchRepo(t)
	// info/exclude is local-only, never committed to the repository: even a
	// clean match there must not grant the exception.
	writeFileOrFatal(t, filepath.Join(dir, ".git", "info", "exclude"), "excluded.txt\n")
	writeFileOrFatal(t, filepath.Join(dir, "excluded.txt"), "content\n")

	ignored, err := IsIgnoredByCommittedGitignore(context.Background(), dir, "excluded.txt")
	if err != nil {
		t.Fatal(err)
	}
	if ignored {
		t.Fatalf("a match sourced only from .git/info/exclude = true, want false")
	}
}

func TestIsIgnoredByCommittedGitignore_UncommittedGitignoreDoesNotCount(t *testing.T) {
	dir := scratchRepo(t)
	// A real .gitignore pattern, matched correctly by git itself, but the
	// file was never added or committed: the rule is not yet repository
	// content, so the exception must not fire.
	writeFileOrFatal(t, filepath.Join(dir, ".gitignore"), "*.secret\n")
	writeFileOrFatal(t, filepath.Join(dir, "x.secret"), "content\n")

	ignored, err := IsIgnoredByCommittedGitignore(context.Background(), dir, "x.secret")
	if err != nil {
		t.Fatal(err)
	}
	if ignored {
		t.Fatalf("a match against an uncommitted, never-added .gitignore = true, want false")
	}
}

func TestIsIgnoredByCommittedGitignore_DirtyCommittedGitignoreDoesNotCount(t *testing.T) {
	dir := scratchRepo(t)
	writeFileOrFatal(t, filepath.Join(dir, ".gitignore"), "*.log\n")
	runGitOrFatal(t, dir, "add", ".gitignore")
	runGitOrFatal(t, dir, "commit", "-q", "-m", "ignore logs")

	// The matching line itself never changes; an unrelated, uncommitted
	// addition to the same file is enough to withhold trust.
	writeFileOrFatal(t, filepath.Join(dir, ".gitignore"), "*.log\n*.tmp\n")

	ignored, err := IsIgnoredByCommittedGitignore(context.Background(), dir, "app.log")
	if err != nil {
		t.Fatal(err)
	}
	if ignored {
		t.Fatalf("a match against a .gitignore with an uncommitted local edit = true, want false")
	}
}

func TestIsIgnoredByCommittedGitignore_NotIgnored(t *testing.T) {
	dir := scratchRepo(t)
	writeFileOrFatal(t, filepath.Join(dir, ".gitignore"), "*.log\n")
	runGitOrFatal(t, dir, "add", ".gitignore")
	runGitOrFatal(t, dir, "commit", "-q", "-m", "ignore logs")

	ignored, err := IsIgnoredByCommittedGitignore(context.Background(), dir, "plain.txt")
	if err != nil {
		t.Fatal(err)
	}
	if ignored {
		t.Fatalf("a path matching no pattern at all = true, want false")
	}
}

func TestIsIgnoredByCommittedGitignore_NotARepositoryIsAnError(t *testing.T) {
	dir := t.TempDir() // no `git init`: confidently not a repository

	if _, err := IsIgnoredByCommittedGitignore(context.Background(), dir, "anything"); err == nil {
		t.Fatalf("a target directory with no repository at all returned no error")
	}
}

// TestIsIgnoredByCommittedGitignore_PathTraversalRelPathIsError is adversarial:
// a relPath that escapes dir via ".." (a caller bug, or a hostile absPath
// upstream) must never resolve into a silent (true, nil) for a location
// outside the repository entirely. git itself refuses a check-ignore target
// outside the repo (exit 128), which checkIgnoreSource's default case
// already surfaces as a real error -- this pins that git refuses first, so a
// caller-side traversal can never widen into an accidental exemption.
func TestIsIgnoredByCommittedGitignore_PathTraversalRelPathIsError(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	parent := t.TempDir()
	dir := filepath.Join(parent, "repo")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	runGitOrFatal(t, dir, "init", "-q", "-b", "scratchbr")
	runGitOrFatal(t, dir, "config", "user.name", "Test User")
	runGitOrFatal(t, dir, "config", "user.email", "test@example.com")
	runGitOrFatal(t, dir, "config", "commit.gpgsign", "false")
	writeFileOrFatal(t, filepath.Join(dir, ".gitignore"), "*.txt\n")
	runGitOrFatal(t, dir, "add", ".gitignore")
	runGitOrFatal(t, dir, "commit", "-q", "-m", "ignore txt")
	writeFileOrFatal(t, filepath.Join(parent, "outside.txt"), "secret\n")

	ignored, err := IsIgnoredByCommittedGitignore(context.Background(), dir, "../outside.txt")
	if err == nil {
		t.Fatalf("a relPath escaping the repository via .. returned no error (ignored=%v)", ignored)
	}
	if ignored {
		t.Fatalf("a relPath escaping the repository via .. answered ignored=true, want false alongside the error")
	}
}

// TestIsIgnoredByCommittedGitignore_DirOnlyPatternDoesNotMatchSameNamedFile
// pins a git-check-ignore behavior this function's fail-closed guarantee
// depends on: a directory-only pattern ("logs/") must not match a plain file
// that merely shares the name. If this ever regressed, a file named to match a
// directory-only ignore rule would wrongly earn the exemption.
func TestIsIgnoredByCommittedGitignore_DirOnlyPatternDoesNotMatchSameNamedFile(t *testing.T) {
	dir := scratchRepo(t)
	writeFileOrFatal(t, filepath.Join(dir, ".gitignore"), "logs/\n")
	runGitOrFatal(t, dir, "add", ".gitignore")
	runGitOrFatal(t, dir, "commit", "-q", "-m", "ignore logs dir")
	writeFileOrFatal(t, filepath.Join(dir, "logs"), "not a directory\n")

	ignored, err := IsIgnoredByCommittedGitignore(context.Background(), dir, "logs")
	if err != nil {
		t.Fatal(err)
	}
	if ignored {
		t.Fatalf("a plain file merely named like a directory-only pattern = true, want false")
	}
}

// TestIsIgnoredByCommittedGitignore_NegatedPatternDoesNotCount is the
// regression pin for a fail-OPEN defect: `git check-ignore -v` counts a
// negated (`!`) match as a match, printing the `!` rule and exiting 0 for a
// path that is explicitly NOT ignored. Reading that exit status as the ignore
// verdict handed the exception to every path a committed `.gitignore`
// re-includes -- typically a TRACKED file, the exact case the exception must
// never cover. The verdict comes from the non-verbose form for this reason.
func TestIsIgnoredByCommittedGitignore_NegatedPatternDoesNotCount(t *testing.T) {
	dir := scratchRepo(t)
	writeFileOrFatal(t, filepath.Join(dir, ".gitignore"), "*.log\n!keep.log\n")
	writeFileOrFatal(t, filepath.Join(dir, "keep.log"), "kept content\n")
	runGitOrFatal(t, dir, "add", ".gitignore", "keep.log")
	runGitOrFatal(t, dir, "commit", "-q", "-m", "ignore logs but keep one")

	ignored, err := IsIgnoredByCommittedGitignore(context.Background(), dir, "keep.log")
	if err != nil {
		t.Fatal(err)
	}
	if ignored {
		t.Fatalf("a path a committed `!` rule re-includes = true, want false: git does not ignore it at all")
	}

	// The sibling path the same file DOES ignore still answers true, so the
	// fix narrowed the answer rather than disabling the exception.
	ignored, err = IsIgnoredByCommittedGitignore(context.Background(), dir, "other.log")
	if err != nil {
		t.Fatal(err)
	}
	if !ignored {
		t.Fatalf("other.log against the same committed rule = false, want true")
	}
}

// TestIsIgnoredByCommittedGitignore_TrackedDespiteRuleDoesNotCount is the
// second fail-OPEN regression pin. A force-added path (`git add -f` under an
// ignore rule, or a committed file inside an ignored directory) is real
// repository history: a worktree CAN hold it, so the "no worktree can ever
// hold this" premise the exception rests on does not apply and the ordinary
// deny must stand. Consulting the index is what produces that answer, which
// is why the check-ignore calls pass no --no-index.
func TestIsIgnoredByCommittedGitignore_TrackedDespiteRuleDoesNotCount(t *testing.T) {
	dir := scratchRepo(t)
	writeFileOrFatal(t, filepath.Join(dir, ".gitignore"), "build/\n")
	if err := os.Mkdir(filepath.Join(dir, "build"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileOrFatal(t, filepath.Join(dir, "build", "committed.txt"), "tracked anyway\n")
	runGitOrFatal(t, dir, "add", ".gitignore")
	runGitOrFatal(t, dir, "add", "-f", "build/committed.txt")
	runGitOrFatal(t, dir, "commit", "-q", "-m", "ignore build, commit one file in it")

	ignored, err := IsIgnoredByCommittedGitignore(context.Background(), dir, "build/committed.txt")
	if err != nil {
		t.Fatal(err)
	}
	if ignored {
		t.Fatalf("a tracked, force-added path under a committed ignore rule = true, want false")
	}

	// Staged but not yet committed counts the same: the index entry alone is
	// enough for git to answer "not ignored", so the exception stays off.
	writeFileOrFatal(t, filepath.Join(dir, "build", "staged.txt"), "staged\n")
	runGitOrFatal(t, dir, "add", "-f", "build/staged.txt")
	ignored, err = IsIgnoredByCommittedGitignore(context.Background(), dir, "build/staged.txt")
	if err != nil {
		t.Fatal(err)
	}
	if ignored {
		t.Fatalf("a staged, force-added path under a committed ignore rule = true, want false")
	}

	// An untracked sibling in the same ignored directory still answers true.
	ignored, err = IsIgnoredByCommittedGitignore(context.Background(), dir, "build/untracked.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !ignored {
		t.Fatalf("an untracked path under the same committed rule = false, want true")
	}
}

// TestIsIgnoredByCommittedGitignore_ColonSourceCannotBorrowCommittedTrust is
// the parse-ambiguity pin. A `-v` line's source field can itself contain a
// colon, so splitting on the first colon alone would truncate a source named
// ".gitignore:evil" -- an uncommitted file a local core.excludesFile points
// at -- down to ".gitignore", whose committed, unmodified content would then
// vouch for a rule that was never committed at all.
func TestIsIgnoredByCommittedGitignore_ColonSourceCannotBorrowCommittedTrust(t *testing.T) {
	dir := scratchRepo(t)
	writeFileOrFatal(t, filepath.Join(dir, ".gitignore"), "committed.txt\n")
	runGitOrFatal(t, dir, "add", ".gitignore")
	runGitOrFatal(t, dir, "commit", "-q", "-m", "ignore committed.txt")
	// A local excludes file whose own name ends in a colon-suffixed
	// ".gitignore", pointed at by repo-local config, never committed.
	writeFileOrFatal(t, filepath.Join(dir, ".gitignore:evil"), "evil.txt\n")
	runGitOrFatal(t, dir, "config", "core.excludesFile", ".gitignore:evil")

	ignored, err := IsIgnoredByCommittedGitignore(context.Background(), dir, "evil.txt")
	if err != nil {
		t.Fatal(err)
	}
	if ignored {
		t.Fatalf("a match sourced from an uncommitted \".gitignore:evil\" = true, want false")
	}
}

// TestIsIgnoredByCommittedGitignore_ColonInCommittedSourcePathFailsClosed
// records the accepted cost of the parse hardening above: a colon anywhere in
// a legitimately committed nested `.gitignore`'s own path makes the `-v` line
// ambiguous, and an ambiguous line answers false. The exception is simply not
// available for such a path -- a conservative deny, never a wrong allow.
func TestIsIgnoredByCommittedGitignore_ColonInCommittedSourcePathFailsClosed(t *testing.T) {
	dir := scratchRepo(t)
	if err := os.Mkdir(filepath.Join(dir, "we:ird"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileOrFatal(t, filepath.Join(dir, "we:ird", ".gitignore"), "x.log\n")
	runGitOrFatal(t, dir, "add", "we:ird/.gitignore")
	runGitOrFatal(t, dir, "commit", "-q", "-m", "ignore x.log in a colon-named directory")

	ignored, err := IsIgnoredByCommittedGitignore(context.Background(), dir, "we:ird/x.log")
	if err != nil {
		t.Fatal(err)
	}
	if ignored {
		t.Fatalf("an ambiguous -v line answered true; an unparseable source must fail closed")
	}
}

// TestIsIgnoredByCommittedGitignore_UnbornHeadIsError pins the last
// fail-closed path by test rather than by reading: with no commit yet there is
// no HEAD to compare against, `git show HEAD:.gitignore` fails with something
// other than its two "not in HEAD" messages, and that must surface as an error
// -- which the gate reads as "not covered" -- never as a plain absence that
// could be mistaken for a decided answer.
func TestIsIgnoredByCommittedGitignore_UnbornHeadIsError(t *testing.T) {
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	dir := t.TempDir()
	runGitOrFatal(t, dir, "init", "-q", "-b", "main")
	writeFileOrFatal(t, filepath.Join(dir, ".gitignore"), "app.log\n")
	runGitOrFatal(t, dir, "add", ".gitignore") // staged, never committed: no HEAD exists

	ignored, err := IsIgnoredByCommittedGitignore(context.Background(), dir, "app.log")
	if err == nil {
		t.Fatalf("a repository with no HEAD returned no error (ignored=%v)", ignored)
	}
	if ignored {
		t.Fatalf("a repository with no HEAD answered ignored=true, want false alongside the error")
	}
}

// TestIsIgnoredByCommittedGitignore_CancelledContextIsError covers the caller's
// timeout: the gate bounds this lookup so a wedged git cannot hang the hook,
// and an expired context must arrive as an error (which the gate reads as "not
// covered") rather than as a decided answer.
func TestIsIgnoredByCommittedGitignore_CancelledContextIsError(t *testing.T) {
	dir := scratchRepo(t)
	writeFileOrFatal(t, filepath.Join(dir, ".gitignore"), "app.log\n")
	runGitOrFatal(t, dir, "add", ".gitignore")
	runGitOrFatal(t, dir, "commit", "-q", "-m", "ignore logs")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ignored, err := IsIgnoredByCommittedGitignore(ctx, dir, "app.log")
	if err == nil {
		t.Fatalf("a cancelled context returned no error (ignored=%v)", ignored)
	}
	if ignored {
		t.Fatalf("a cancelled context answered ignored=true, want false alongside the error")
	}
}

func TestParseVerboseIgnoreLine(t *testing.T) {
	cases := []struct {
		name       string
		line       string
		wantSource string
		wantNeg    bool
		wantOK     bool
	}{
		{"root gitignore", ".gitignore:1:*.log\tapp.log", ".gitignore", false, true},
		{"nested gitignore", "sub/.gitignore:12:*.tmp\tsub/f.tmp", "sub/.gitignore", false, true},
		{"info exclude", ".git/info/exclude:1:e.txt\te.txt", ".git/info/exclude", false, true},
		{"absolute excludesfile", "/home/u/.gitignore_global:3:*.bak\tf.bak", "/home/u/.gitignore_global", false, true},
		{"negated pattern", ".gitignore:2:!keep.log\tkeep.log", ".gitignore", true, true},
		{"pattern containing a colon", ".gitignore:2:a:b\ta:b", ".gitignore", false, true},
		{"colon in source path", "we:ird/.gitignore:1:x.log\twe:ird/x.log", "", false, false},
		{"colon-suffixed source name", ".gitignore:evil:1:evil.txt\tevil.txt", "", false, false},
		{"no colon at all", "garbage", "", false, false},
		{"one colon only", ".gitignore:1", "", false, false},
		{"empty linenum", ".gitignore::*.log\tapp.log", "", false, false},
		{"empty line", "", "", false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			source, negated, ok := parseVerboseIgnoreLine(c.line)
			if ok != c.wantOK || negated != c.wantNeg || (ok && source != c.wantSource) {
				t.Fatalf("parseVerboseIgnoreLine(%q) = (%q, %v, %v), want (%q, %v, %v)",
					c.line, source, negated, ok, c.wantSource, c.wantNeg, c.wantOK)
			}
		})
	}
}
