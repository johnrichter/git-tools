// gitignore.go answers one narrow question for worktree-gate/detect's SC23
// exception: is a path ignored by a `.gitignore` rule that is itself already
// committed at HEAD, as opposed to a pattern from `.git/info/exclude`, a
// global core.excludesFile, or an uncommitted edit to a `.gitignore` file --
// and as opposed to a path git does not really ignore at all, whether because
// a negating rule re-includes it or because it is tracked despite the rule.
// Every answer here is conservative: an error, or any signal this file
// cannot resolve narrowly, comes back as (false, err) or (false, nil) --
// never (true, nil) on a guess.
package gitexec

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"

	"github.com/johnrichter/claude-shared-tooling/go/git"
)

// IsIgnoredByCommittedGitignore reports whether relPath (relative to dir,
// slash-separated, dir being the repository root) is ignored by a
// `.gitignore` rule already committed at HEAD in dir. It answers false, not
// an error, when the path is not ignored at all, when the path is tracked in
// dir (git itself reports a tracked path as not ignored, which is the answer
// the caller needs: tracked content exists in history, so a worktree can hold
// it and the ordinary remedy applies), when the only match traces to
// `.git/info/exclude` or a global core.excludesFile (neither is committed
// repository content), or when the matching `.gitignore` file itself carries
// any uncommitted change relative to HEAD.
//
// The ignore verdict and the matching rule's source are asked in two separate
// git calls on purpose. `git check-ignore -v` cannot answer the verdict: git
// documents that a NEGATED pattern counts as a match under -v, so -v exits 0
// and prints an `!` rule for a path that is explicitly NOT ignored. Only the
// non-verbose form's exit status is the ignore verdict, so it decides here and
// the -v line is read for nothing but the source path. Any parse the -v line
// cannot resolve unambiguously therefore fails closed instead of borrowing a
// committed file's trust.
func IsIgnoredByCommittedGitignore(ctx context.Context, dir, relPath string) (bool, error) {
	ignored, err := checkIgnored(ctx, dir, relPath)
	if err != nil || !ignored {
		return false, err
	}
	source, matched, err := checkIgnoreSource(ctx, dir, relPath)
	if err != nil || !matched {
		return false, err
	}
	if filepath.IsAbs(source) || filepath.Base(source) != ".gitignore" {
		return false, nil // .git/info/exclude, or a global excludesfile: not committed repo content
	}
	committed, exists, err := showCommitted(ctx, dir, source)
	if err != nil || !exists {
		return false, err
	}
	working, err := os.ReadFile(filepath.Join(dir, source))
	if err != nil {
		return false, err
	}
	return bytes.Equal(committed, working), nil
}

// checkIgnored runs `git check-ignore -q -- relPath` in dir and returns git's
// own ignore verdict: exit 0 means ignored, exit 1 means not ignored (not an
// error), any other non-zero exit is a real error. The quiet form is used
// rather than -v because -v's exit status also counts a negated (`!`) match as
// a match, which would read an explicitly re-included path as ignored.
//
// No --no-index: consulting the index is what makes a force-added path
// (`git add -f` under an ignore rule, or a committed file inside an ignored
// directory) answer "not ignored" here, which is correct for this caller --
// such a path IS in history, so a worktree can hold it and the exception must
// not cover it. Dropping --no-index costs nothing for a path that does not
// exist yet: git answers from the ignore rules alone, since an absent path has
// no index entry either way.
func checkIgnored(ctx context.Context, dir, relPath string) (bool, error) {
	args := []string{"check-ignore", "-q", "--", relPath}
	res, err := RunGit(ctx, dir, args...)
	if err != nil {
		return false, err
	}
	switch res.ExitCode {
	case 0:
		return true, nil
	case 1:
		return false, nil
	default:
		return false, &git.CommandError{Args: args, ExitCode: res.ExitCode, Stderr: strings.TrimSpace(string(res.Stderr))}
	}
}

// checkIgnoreSource runs `git check-ignore -v -- relPath` in dir and reports
// the ignore file whose rule matched. matched is false, with no error, on exit
// 1 and on any -v line this cannot parse unambiguously; any other non-zero
// exit is a real error. It is called only after checkIgnored has already
// settled the verdict, so a negated match reaching here would be a git
// behavior change rather than the ordinary case -- it is rejected anyway,
// keeping this helper's own answer honest in isolation.
func checkIgnoreSource(ctx context.Context, dir, relPath string) (source string, matched bool, err error) {
	args := []string{"check-ignore", "-v", "--", relPath}
	res, err := RunGit(ctx, dir, args...)
	if err != nil {
		return "", false, err
	}
	switch res.ExitCode {
	case 1:
		return "", false, nil
	case 0:
		source, negated, ok := parseVerboseIgnoreLine(strings.TrimRight(string(res.Stdout), "\n"))
		if !ok || negated {
			return "", false, nil
		}
		return source, true, nil
	default:
		return "", false, &git.CommandError{Args: args, ExitCode: res.ExitCode, Stderr: strings.TrimSpace(string(res.Stderr))}
	}
}

// parseVerboseIgnoreLine splits one `git check-ignore -v` line, whose format
// is "<source>:<linenum>:<pattern>\t<pathname>", into the source path and
// whether the matched pattern is a negation. ok is false for any line that
// does not fit that format exactly.
//
// The first colon is trusted to end the source field ONLY when a "<linenum>:"
// follows it, because a source path can legitimately contain a colon: POSIX
// allows one in a filename (a committed "we:ird/.gitignore"), and a relative
// core.excludesFile is echoed here exactly as configured. Without that
// check, a source named ".gitignore:<anything>" -- an uncommitted, locally
// configured excludes file -- would truncate to ".gitignore" and inherit the
// committed root file's trust. An ambiguous line answers ok=false, which the
// caller turns into a plain deny.
func parseVerboseIgnoreLine(line string) (source string, negated, ok bool) {
	source, rest, found := strings.Cut(line, ":")
	if !found {
		return "", false, false
	}
	linenum, pattern, found := strings.Cut(rest, ":")
	if !found || linenum == "" || strings.TrimLeft(linenum, "0123456789") != "" {
		return "", false, false
	}
	return source, strings.HasPrefix(pattern, "!"), true
}

// showCommitted returns relPath's content as committed at HEAD in dir, and
// whether that path exists at HEAD at all. exists is false, with no error,
// only for git's own two "not in HEAD" answers -- "does not exist in" (path
// absent everywhere) and "exists on disk, but not in" (an untracked or
// newly-added file) -- every other non-zero exit (no HEAD yet, a genuinely
// broken repository) is a real error, never mistaken for a plain absence.
func showCommitted(ctx context.Context, dir, relPath string) (content []byte, exists bool, err error) {
	args := []string{"show", "HEAD:" + relPath}
	res, err := RunGit(ctx, dir, args...)
	if err != nil {
		return nil, false, err
	}
	if res.ExitCode == 0 {
		return res.Stdout, true, nil
	}
	stderr := string(res.Stderr)
	if strings.Contains(stderr, "does not exist in") || strings.Contains(stderr, "exists on disk, but not in") {
		return nil, false, nil
	}
	return nil, false, &git.CommandError{Args: args, ExitCode: res.ExitCode, Stderr: strings.TrimSpace(string(res.Stderr))}
}
