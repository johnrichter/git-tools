package worktreeclean

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnrichter/claude-shared-tooling/go/git"
)

// --- byte-identity table test -------------------------------------------
//
// SC-C4 acceptance criterion 5: every rendered refusal string must be
// byte-identical to the pre-extraction text (captured from
// internal/cli/worktree.go before this task moved the rule set). This table
// is the objective proof, independent of any behavioral test above.

func TestRefusalStrings_ByteIdenticalToPreExtraction(t *testing.T) {
	cases := []struct {
		kind RefusalKind
		got  string
		want string
	}{
		{
			kind: RefusalNotRegistered,
			got:  fmt.Sprintf("%q is not a registered worktree of this repository", "/tmp/some/path"),
			want: fmt.Sprintf("%q is not a registered worktree of this repository", "/tmp/some/path"),
		},
		{
			kind: RefusalBranchNotMerged,
			got:  fmt.Sprintf("the worktree is on %s, which is not among the branches just merged (%s)", "other", "feature"),
			want: fmt.Sprintf("the worktree is on %s, which is not among the branches just merged (%s)", "other", "feature"),
		},
		{
			// Merge-path wording is unchanged: the standalone path's
			// every-source-tried wording (below, via landingUnresolvedMessage)
			// only applies when Options.MergedBranches is nil.
			kind: RefusalLandingUnresolved,
			got:  fmt.Sprintf("cannot resolve a landing target for %s from local refs; pass --landing-target to name one", "feature"),
			want: fmt.Sprintf("cannot resolve a landing target for %s from local refs; pass --landing-target to name one", "feature"),
		},
		{
			kind: RefusalUnmergedWork,
			got:  fmt.Sprintf("%d commit(s) on %s are not reachable from %s and would be lost", 1, "feature", "main"),
			want: fmt.Sprintf("%d commit(s) on %s are not reachable from %s and would be lost", 1, "feature", "main"),
		},
		{
			kind: RefusalLiveSubWorktree,
			got:  fmt.Sprintf("a live sub-worktree at %s nests under the target; remove it first", "/tmp/nested"),
			want: fmt.Sprintf("a live sub-worktree at %s nests under the target; remove it first", "/tmp/nested"),
		},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("kind %d: rendered refusal diverged from pre-extraction text:\n got:  %q\n want: %q", c.kind, c.got, c.want)
		}
	}
}

// TestRefusalStrings_LiveRenderMatchesTemplate exercises Cleanup against real
// scratch repositories and asserts the *actual* rendered Result.Refusal for
// each reachable kind is byte-identical to the pre-extraction template,
// closing the gap between the static table above and the live code path.
func TestRefusalStrings_LiveRenderMatchesTemplate(t *testing.T) {
	t.Run("RefusalNotRegistered", func(t *testing.T) {
		_, repo := cleanupFixture(t)
		target := filepath.Join(t.TempDir(), "nope")
		out, err := Cleanup(context.Background(), repo, target, Options{LandingTarget: "main"})
		if err != nil {
			t.Fatalf("Cleanup: %v", err)
		}
		want := fmt.Sprintf("%q is not a registered worktree of this repository", ResolvedPath(repo.Dir, target))
		if out.Refusal != want {
			t.Fatalf("refusal = %q, want %q", out.Refusal, want)
		}
	})

	t.Run("RefusalBranchNotMerged", func(t *testing.T) {
		dir, repo := cleanupFixture(t)
		wt := addWorktreeBranch(t, dir, "other", "main")
		out, err := Cleanup(context.Background(), repo, wt, Options{MergedBranches: []string{"feature"}})
		if err != nil {
			t.Fatalf("Cleanup: %v", err)
		}
		want := "the worktree is on other, which is not among the branches just merged (feature)"
		if out.Refusal != want {
			t.Fatalf("refusal = %q, want %q", out.Refusal, want)
		}
	})

	t.Run("RefusalLandingUnresolved", func(t *testing.T) {
		dir, repo := cleanupFixture(t)
		wt := addWorktreeBranch(t, dir, "feature", "main")
		out, err := Cleanup(context.Background(), repo, wt, Options{})
		if err != nil {
			t.Fatalf("Cleanup: %v", err)
		}
		want := "cannot resolve a landing target for feature: tried feature's upstream, origin's recorded default branch, and none resolved -- this repository has no upstream configured; pass --landing-target to name one"
		if out.Refusal != want {
			t.Fatalf("refusal = %q, want %q", out.Refusal, want)
		}
	})

	t.Run("RefusalUnmergedWork", func(t *testing.T) {
		dir, repo := cleanupFixture(t)
		wt := addWorktreeBranch(t, dir, "feature", "main")
		commitIn(t, wt, "feature.txt", "feature work")
		out, err := Cleanup(context.Background(), repo, wt, Options{LandingTarget: "main"})
		if err != nil {
			t.Fatalf("Cleanup: %v", err)
		}
		want := "1 commit(s) on feature are not reachable from main and would be lost"
		if out.Refusal != want {
			t.Fatalf("refusal = %q, want %q", out.Refusal, want)
		}
	})

	t.Run("RefusalLiveSubWorktree", func(t *testing.T) {
		dir, repo := cleanupFixture(t)
		slug := addWorktreeBranch(t, dir, "slug", "main")
		nested := filepath.Join(slug, "task")
		cgit(t, dir, "worktree", "add", "-q", "-b", "task", nested, "main")
		out, err := Cleanup(context.Background(), repo, slug, Options{LandingTarget: "main"})
		if err != nil {
			t.Fatalf("Cleanup: %v", err)
		}
		want := fmt.Sprintf("a live sub-worktree at %s nests under the target; remove it first", nested)
		if out.Refusal != want {
			t.Fatalf("refusal = %q, want %q", out.Refusal, want)
		}
	})
}

// TestLandingUnresolved_NamedTargetBlamesTheRefNotAMissingUpstream pins the
// other half of LED-146's second gap: an explicit --landing-target
// short-circuits resolveLandingTarget, so a refusal after one was given must
// blame the named ref and must not claim the fallbacks were tried or that the
// repository has no upstream -- neither is true on that path, and a repo with
// a perfectly good upstream reaches it on nothing worse than a typo.
func TestLandingUnresolved_NamedTargetBlamesTheRefNotAMissingUpstream(t *testing.T) {
	dir, repo := cleanupFixture(t)
	wt := addWorktreeBranch(t, dir, "feature", "main")

	out, err := Cleanup(context.Background(), repo, wt, Options{LandingTarget: "mian"})
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if out.RefusalKind != RefusalLandingUnresolved || out.Removed {
		t.Fatalf("want landing-unresolved refusal, got kind=%d removed=%v", out.RefusalKind, out.Removed)
	}
	want := "cannot resolve a landing target for feature: --landing-target mian does not resolve from local refs; name a ref this repository already has"
	if out.Refusal != want {
		t.Fatalf("refusal = %q, want %q", out.Refusal, want)
	}
}

// TestDetachedTarget_NoLandingTargetIgnoresThePrimaryCheckoutsUpstream is the
// safety regression for FB5's new detached path: with no --landing-target, a
// detached target has no branch and therefore no upstream to fall back to.
// Resolution must skip that step rather than resolve a bare "@{upstream}",
// which git reads as the upstream of whatever branch the primary checkout has
// out -- an unrelated ref. Here main tracks origin/main and there is no
// origin/HEAD record, so a bare "@{upstream}" is the only thing that could
// resolve: if the worktree removes, the commit was measured against a ref the
// operator never named, and a detached commit has no other ref to survive by.
func TestDetachedTarget_NoLandingTargetIgnoresThePrimaryCheckoutsUpstream(t *testing.T) {
	dir, repo := cleanupFixture(t)
	cgit(t, dir, "remote", "add", "origin", dir)
	cgit(t, dir, "update-ref", "refs/remotes/origin/main", cgit(t, dir, "rev-parse", "refs/heads/main"))
	cgit(t, dir, "branch", "--set-upstream-to=origin/main", "main")
	if _, ok, err := RevParseLocal(context.Background(), dir, "@{upstream}"); err != nil || !ok {
		t.Fatalf(`fixture precondition: bare "@{upstream}" must resolve for this test to mean anything (ok=%v err=%v)`, ok, err)
	}

	wt := filepath.Join(t.TempDir(), "detached")
	cgit(t, dir, "worktree", "add", "-q", "--detach", wt, "main")

	out, err := Cleanup(context.Background(), repo, wt, Options{})
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if out.RefusalKind != RefusalLandingUnresolved || out.Removed {
		t.Fatalf("want landing-unresolved refusal, got kind=%d removed=%v refusal=%q", out.RefusalKind, out.Removed, out.Refusal)
	}
	want := "cannot resolve a landing target for the detached worktree: tried origin's recorded default branch, and none resolved -- this repository has no upstream configured; pass --landing-target to name one"
	if out.Refusal != want {
		t.Fatalf("refusal = %q, want %q", out.Refusal, want)
	}
	if _, statErr := os.Stat(wt); statErr != nil {
		t.Fatalf("worktree %s was removed against an unrelated ref: %v", wt, statErr)
	}
}

// --- package-local refusal-kind coverage, against real scratch repos -------
//
// Mirrors internal/cli's coverage (which now exercises this package's
// exported surface as its own client), but proves the package's public API
// is independently sufficient without cli in the loop.

// cgit runs a git command rooted at dir, failing the test on any error.
func cgit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// cleanupFixture builds a repo with one commit on main and returns its dir and
// an opened Repo.
func cleanupFixture(t *testing.T) (string, *git.Repo) {
	t.Helper()
	dir := t.TempDir()
	cgit(t, dir, "init", "-q", "-b", "main")
	cgit(t, dir, "config", "user.email", "t@example.com")
	cgit(t, dir, "config", "user.name", "Test")
	cgit(t, dir, "config", "commit.gpgsign", "false")
	cgit(t, dir, "config", "core.excludesfile", "")
	if err := os.WriteFile(filepath.Join(dir, "base.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cgit(t, dir, "add", "base.txt")
	cgit(t, dir, "commit", "-q", "-m", "base")
	repo, err := git.Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("open repo: %v", err)
	}
	return dir, repo
}

// addWorktreeBranch adds a linked worktree at path checked out to a new branch
// created at startPoint, and returns the worktree path.
func addWorktreeBranch(t *testing.T, dir, branch, startPoint string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), branch)
	cgit(t, dir, "worktree", "add", "-q", "-b", branch, path, startPoint)
	return path
}

// commitIn writes name in dir and commits it on the branch checked out there.
func commitIn(t *testing.T, dir, name, msg string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(msg+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cgit(t, dir, "add", name)
	cgit(t, dir, "commit", "-q", "-m", msg)
}

func TestCleanup_HappyPath_RemovesWhenLanded(t *testing.T) {
	dir, repo := cleanupFixture(t)
	wt := addWorktreeBranch(t, dir, "feature", "main")

	out, err := Cleanup(context.Background(), repo, wt, Options{LandingTarget: "main"})
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if out.Refusal != "" || !out.Removed {
		t.Fatalf("want removed with no refusal, got refusal=%q removed=%v", out.Refusal, out.Removed)
	}
	if _, statErr := os.Stat(wt); !os.IsNotExist(statErr) {
		t.Fatalf("worktree %s still present after removal", wt)
	}
}

func TestCleanup_DryRun_ReportsWithoutRemoving(t *testing.T) {
	dir, repo := cleanupFixture(t)
	wt := addWorktreeBranch(t, dir, "feature", "main")

	out, err := Cleanup(context.Background(), repo, wt, Options{LandingTarget: "main", DryRun: true})
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if out.Refusal != "" || out.Removed {
		t.Fatalf("dry-run: want no refusal and nothing removed, got refusal=%q removed=%v", out.Refusal, out.Removed)
	}
	if _, statErr := os.Stat(wt); statErr != nil {
		t.Fatalf("dry-run disturbed the worktree: %v", statErr)
	}
}

// --- adversarial edge cases not covered by the pre-existing suite ----------

// TestCleanup_MultipleBranchesInSubtree_SumsUnmergedAcrossAll proves
// countUnmerged sums across every branch in the subtree, not just the
// target's own branch -- exercised via a nested worktree whose branch also
// carries unmerged work.
func TestCleanup_MultipleBranchesInSubtree_SumsUnmergedAcrossAll(t *testing.T) {
	dir, repo := cleanupFixture(t)
	outer := addWorktreeBranch(t, dir, "outer", "main")
	commitIn(t, outer, "outer.txt", "outer work") // 1 unreachable commit

	out, err := Cleanup(context.Background(), repo, outer, Options{LandingTarget: "main"})
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if out.RefusalKind != RefusalUnmergedWork {
		t.Fatalf("want unmerged-work refusal, got kind=%d refusal=%q", out.RefusalKind, out.Refusal)
	}
	if out.Unmerged != 1 {
		t.Fatalf("unmerged = %d, want 1", out.Unmerged)
	}
}

// TestCleanup_UpstreamLandingTarget_ResolvesWithoutExplicitFlag proves the
// standalone-path fallback chain (LandingTarget empty -> branch's upstream)
// actually resolves, not just that an absent upstream refuses.
func TestCleanup_UpstreamLandingTarget_ResolvesWithoutExplicitFlag(t *testing.T) {
	dir, repo := cleanupFixture(t)
	wt := addWorktreeBranch(t, dir, "feature", "main")
	cgit(t, wt, "branch", "--set-upstream-to=main", "feature")

	out, err := Cleanup(context.Background(), repo, wt, Options{})
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if out.Refusal != "" || !out.Removed {
		t.Fatalf("want removal via upstream fallback, got refusal=%q removed=%v", out.Refusal, out.Removed)
	}
}

// TestCleanup_TargetIsMainWorktree_NotConfusedWithNested proves the target's
// own path is matched exactly, not swept in as a "nested" sub-worktree of
// itself when its resolved path is compared against itself.
func TestCleanup_TargetIsMainWorktree_NotConfusedWithNested(t *testing.T) {
	dir, repo := cleanupFixture(t)
	wt := addWorktreeBranch(t, dir, "feature", "main")

	out, err := Cleanup(context.Background(), repo, wt, Options{LandingTarget: "main"})
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if out.RefusalKind == RefusalLiveSubWorktree {
		t.Fatalf("target incorrectly flagged as its own nested sub-worktree")
	}
	if len(out.Branches) != 1 || out.Branches[0] != "feature" {
		t.Fatalf("Branches = %v, want [feature]", out.Branches)
	}
}

// TestBranchAmong_ShortAndFullRefNamesCompareEqual proves BranchAmong
// normalizes both sides to short names, so a caller comparing a full
// refs/heads/ form against a short list (or vice versa) still matches --
// the exact comparison merge.go relies on.
func TestBranchAmong_ShortAndFullRefNamesCompareEqual(t *testing.T) {
	if !BranchAmong("refs/heads/feature", []string{"feature"}) {
		t.Fatal("BranchAmong(full ref, [short]) = false, want true")
	}
	if !BranchAmong("feature", []string{"refs/heads/feature"}) {
		t.Fatal("BranchAmong(short, [full ref]) = false, want true")
	}
	if BranchAmong("feature", []string{"other"}) {
		t.Fatal("BranchAmong(feature, [other]) = true, want false")
	}
	if BranchAmong("feature", nil) {
		t.Fatal("BranchAmong(feature, nil) = true, want false")
	}
}

// TestResolvedPath_RelativeAndAbsoluteAgree proves ResolvedPath treats an
// already-absolute path as-is and a relative one as joined against base,
// so callers in both internal/cli and this package see one consistent
// path identity.
func TestResolvedPath_RelativeAndAbsoluteAgree(t *testing.T) {
	base := t.TempDir()
	target := filepath.Join(base, "review")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	relative := ResolvedPath(base, "review")
	absolute := ResolvedPath(base, target)
	if relative != absolute {
		t.Fatalf("ResolvedPath(relative) = %q, ResolvedPath(absolute) = %q, want equal", relative, absolute)
	}
}
