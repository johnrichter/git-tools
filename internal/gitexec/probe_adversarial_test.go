// probe_adversarial_test.go is scratch, independent-verification-only
// probing added by a second-round test-engineer pass. It talks to the real
// git binary directly (not through IsIgnoredByCommittedGitignore) to confirm
// the raw behaviors the production code's fixes rely on, and separately
// re-exercises IsIgnoredByCommittedGitignore against a few extra adversarial
// shapes beyond gitignore_test.go's existing cases. Not meant to be a
// permanent addition to the suite; left here as durable evidence of what was
// hand-checked.
package gitexec

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func rawGit(t *testing.T, dir string, args ...string) (stdout string, exitCode int) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("git %s: %v", strings.Join(args, " "), err)
		}
	}
	return string(out), code
}

// TestRaw_NegatedMatch_VerboseExitsZero independently confirms defect #1's
// premise directly against the installed git binary: -v exits 0 (a "match")
// for a path a `!` rule re-includes, while -q exits 1 (not ignored). If this
// git behavior ever changed, checkIgnored's reliance on -q's exit code alone
// would need re-examination.
func TestRaw_NegatedMatch_VerboseExitsZero(t *testing.T) {
	dir := scratchRepo(t)
	writeFileOrFatal(t, filepath.Join(dir, ".gitignore"), "*.log\n!keep.log\n")
	runGitOrFatal(t, dir, "add", ".gitignore")
	runGitOrFatal(t, dir, "commit", "-q", "-m", "x")
	// keep.log stays untracked on purpose: a tracked path short-circuits
	// check-ignore to "not ignored" via the index regardless of negation
	// (that's defect #2's shape, not this one), which would confound this
	// probe of negation in isolation.
	writeFileOrFatal(t, filepath.Join(dir, "keep.log"), "kept\n")

	vout, vcode := rawGit(t, dir, "check-ignore", "-v", "--", "keep.log")
	t.Logf("raw -v: out=%q code=%d", vout, vcode)
	if vcode != 0 {
		t.Fatalf("git check-ignore -v on a negated-match path exited %d, want 0 (this test's premise about git's own behavior no longer holds): %q", vcode, vout)
	}
	if !strings.Contains(vout, "!keep.log") {
		t.Fatalf("git check-ignore -v output = %q, want it to print the negation rule", vout)
	}
	_, qcode := rawGit(t, dir, "check-ignore", "-q", "--", "keep.log")
	if qcode != 1 {
		t.Fatalf("git check-ignore -q on a negated-match path exited %d, want 1 (not ignored)", qcode)
	}
}

// TestRaw_NoIndex_MisreportsTrackedPathAsIgnored independently confirms
// defect #2's premise: with --no-index, a force-added tracked path under an
// ignore rule is reported ignored=true (exit 0), while without --no-index
// (consulting the index, as production checkIgnored now does) it is
// ignored=false (exit 1).
func TestRaw_NoIndex_MisreportsTrackedPathAsIgnored(t *testing.T) {
	dir := scratchRepo(t)
	writeFileOrFatal(t, filepath.Join(dir, ".gitignore"), "build/\n")
	if err := os.Mkdir(filepath.Join(dir, "build"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileOrFatal(t, filepath.Join(dir, "build", "committed.txt"), "tracked anyway\n")
	runGitOrFatal(t, dir, "add", ".gitignore")
	runGitOrFatal(t, dir, "add", "-f", "build/committed.txt")
	runGitOrFatal(t, dir, "commit", "-q", "-m", "x")

	_, noIndexCode := rawGit(t, dir, "check-ignore", "-q", "--no-index", "--", "build/committed.txt")
	if noIndexCode != 0 {
		t.Fatalf("--no-index check-ignore on a tracked, force-added path exited %d, want 0 (this test's premise about git's own behavior no longer holds)", noIndexCode)
	}
	_, indexedCode := rawGit(t, dir, "check-ignore", "-q", "--", "build/committed.txt")
	if indexedCode != 1 {
		t.Fatalf("index-aware check-ignore on the same tracked path exited %d, want 1 (not ignored)", indexedCode)
	}
}

// TestRaw_ExcludesFileEchoedVerbatimAsSource independently confirms defect
// #3's raw premise: a relative core.excludesFile value containing a colon is
// echoed verbatim as the -v source field, ending in ":evil" -- exactly the
// shape parseVerboseIgnoreLine must refuse to treat as ".gitignore".
func TestRaw_ExcludesFileEchoedVerbatimAsSource(t *testing.T) {
	dir := scratchRepo(t)
	writeFileOrFatal(t, filepath.Join(dir, ".gitignore"), "committed.txt\n")
	runGitOrFatal(t, dir, "add", ".gitignore")
	runGitOrFatal(t, dir, "commit", "-q", "-m", "x")
	writeFileOrFatal(t, filepath.Join(dir, ".gitignore:evil"), "evil.txt\n")
	runGitOrFatal(t, dir, "config", "core.excludesFile", ".gitignore:evil")

	vout, vcode := rawGit(t, dir, "check-ignore", "-v", "--", "evil.txt")
	if vcode != 0 {
		t.Fatalf("check-ignore -v on evil.txt exited %d, want 0", vcode)
	}
	if !strings.HasPrefix(vout, ".gitignore:evil:") {
		t.Fatalf("raw -v output = %q, want it to start with the excludes-file name verbatim (.gitignore:evil:...) -- if this ever stops being true, defect #3's premise is gone", vout)
	}
	source, negated, ok := parseVerboseIgnoreLine(strings.TrimRight(vout, "\n"))
	if ok {
		t.Fatalf("parseVerboseIgnoreLine(%q) = (%q, %v, true), want ok=false: this exact adversarial line must fail closed", vout, source, negated)
	}
}

// TestAdversarial_NegatedDirPattern_PathInsideReincludedDir combines
// negation with a directory pattern: a broad directory-level ignore with a
// negated re-inclusion of one file *inside* it. This exercises the same
// negation defect through a shape gitignore_test.go's existing negation case
// (flat file patterns) does not cover.
func TestAdversarial_NegatedDirPattern_PathInsideReincludedDir(t *testing.T) {
	dir := scratchRepo(t)
	// "vendor/*" (not "vendor/") excludes only vendor's contents, not the
	// directory entry itself -- git's own docs are explicit that a file
	// cannot be re-included once a *parent directory* is excluded, so
	// "vendor/" plus "!vendor/keep/" would leave keep/ ignored regardless of
	// the negation; "vendor/*" keeps the negation effective.
	writeFileOrFatal(t, filepath.Join(dir, ".gitignore"), "vendor/*\n!vendor/keep/\n!vendor/keep/**\n")
	if err := os.MkdirAll(filepath.Join(dir, "vendor", "keep"), 0o755); err != nil {
		t.Fatal(err)
	}
	runGitOrFatal(t, dir, "add", ".gitignore")
	runGitOrFatal(t, dir, "commit", "-q", "-m", "x")
	// f.txt stays untracked on purpose, same reasoning as the negation probe
	// above: tracking it would confound negation with defect #2's shape.
	writeFileOrFatal(t, filepath.Join(dir, "vendor", "keep", "f.txt"), "kept\n")

	rqout, rqcode := rawGit(t, dir, "check-ignore", "-q", "--", "vendor/keep/f.txt")
	rvout, rvcode := rawGit(t, dir, "check-ignore", "-v", "--", "vendor/keep/f.txt")
	t.Logf("raw -q: out=%q code=%d ; raw -v: out=%q code=%d", rqout, rqcode, rvout, rvcode)

	ignored, err := IsIgnoredByCommittedGitignore(context.Background(), dir, "vendor/keep/f.txt")
	if err != nil {
		t.Fatal(err)
	}
	if ignored {
		t.Fatalf("vendor/keep/f.txt re-included by a negated dir pattern = true, want false")
	}

	// Sibling still inside vendor/ but outside the re-included subdir stays
	// covered by the broad rule.
	ignored, err = IsIgnoredByCommittedGitignore(context.Background(), dir, "vendor/other.txt")
	if err != nil {
		t.Fatal(err)
	}
	if !ignored {
		t.Fatalf("vendor/other.txt (outside the negated subdir) = false, want true")
	}
}

// TestAdversarial_SymlinkGitignoreNeverGrantsExemption probes a shape not
// covered elsewhere: a .gitignore path that is itself a symlink on disk.
// `git show HEAD:<path>` on a symlink blob returns the link's target text
// verbatim (e.g. "real-ignore"), never the dereferenced content, while
// os.ReadFile on the same path follows the symlink and returns the
// dereferenced content (e.g. "*.log\n"). Those two can never byte-compare
// equal, so IsIgnoredByCommittedGitignore permanently reads a symlinked
// .gitignore as "dirty" and withholds the exemption -- even when the
// symlink and its target are both committed and nothing is locally
// modified. This is a fail-closed limitation (the exemption is simply
// unavailable through a symlinked .gitignore), not a security defect: it
// never grants an exemption it shouldn't, it just never grants one here at
// all. Recorded so a future change to showCommitted/the working-file read
// doesn't accidentally start comparing dereferenced-to-dereferenced (which
// would need its own scrutiny for a fail-open risk) without this behavior
// change being deliberate.
func TestAdversarial_SymlinkGitignoreNeverGrantsExemption(t *testing.T) {
	dir := scratchRepo(t)
	writeFileOrFatal(t, filepath.Join(dir, "real-ignore"), "*.log\n")
	if err := os.Symlink("real-ignore", filepath.Join(dir, ".gitignore")); err != nil {
		t.Skipf("symlinks unsupported in this environment: %v", err)
	}
	runGitOrFatal(t, dir, "add", "real-ignore", ".gitignore")
	runGitOrFatal(t, dir, "commit", "-q", "-m", "x")

	ignored, err := IsIgnoredByCommittedGitignore(context.Background(), dir, "app.log")
	if err != nil {
		t.Fatal(err)
	}
	if ignored {
		t.Fatalf("app.log via a symlinked .gitignore, clean and fully committed = true, want false (known fail-closed limitation: see comment)")
	}
}
