package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnrichter/claude-shared-tooling/go/git"
)

func TestWorktreeRegisteredAt_MatchesByResolvedPath(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "review")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}

	list := []git.WorktreeInfo{{Path: target, Branch: "review"}}

	if !worktreeRegisteredAt(list, dir, target) {
		t.Fatalf("worktreeRegisteredAt(%q) = false, want true: entry is in the list", target)
	}
	if !worktreeRegisteredAt(list, dir, target+string(os.PathSeparator)) {
		t.Fatalf("worktreeRegisteredAt with a trailing separator = false, want true: same resolved path")
	}
}

func TestWorktreeRegisteredAt_MissingEntryIsUnverified(t *testing.T) {
	dir := t.TempDir()
	registered := filepath.Join(dir, "other")
	claimed := filepath.Join(dir, "review")

	list := []git.WorktreeInfo{{Path: registered, Branch: "other"}}

	if worktreeRegisteredAt(list, dir, claimed) {
		t.Fatalf("worktreeRegisteredAt(%q) = true, want false: %s was never registered", claimed, claimed)
	}
}

func TestWorktreeRegisteredAt_EmptyListIsUnverified(t *testing.T) {
	dir := t.TempDir()
	if worktreeRegisteredAt(nil, dir, filepath.Join(dir, "review")) {
		t.Fatal("worktreeRegisteredAt(nil list) = true, want false")
	}
}

// TestWorktreeRegisteredAt_RelativePathResolvesAgainstRepoDir covers the
// case `worktree add` actually hits: a relative <path> argument is git's
// concept of "relative to the repository", not the calling process's
// current directory. A registered entry with an absolute path under
// repoDir must match a caller-supplied relative path resolved against
// repoDir, regardless of what the process's cwd happens to be.
func TestWorktreeRegisteredAt_RelativePathResolvesAgainstRepoDir(t *testing.T) {
	repoDir := t.TempDir()
	target := filepath.Join(repoDir, "review")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}

	list := []git.WorktreeInfo{{Path: target, Branch: "review"}}

	if !worktreeRegisteredAt(list, repoDir, "review") {
		t.Fatalf("worktreeRegisteredAt(%q, repoDir=%q) = false, want true: relative path should resolve against repoDir", "review", repoDir)
	}
}

// --- cleanupWorktree fixture tests -------------------------------------------
//
// These exercise the one shared rule set both cleanup paths call. Each fixture
// is a real repository so the no-work-loss, cardinality, wrong-branch, and
// detached-head rules are judged against genuine local refs -- never the
// network, so the suite stays hermetic.

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

func TestCleanupWorktree_HappyPath_RemovesWhenLanded(t *testing.T) {
	dir, repo := cleanupFixture(t)
	wt := addWorktreeBranch(t, dir, "feature", "main")

	out, err := cleanupWorktree(context.Background(), repo, wt, cleanupOptions{LandingTarget: "main"})
	if err != nil {
		t.Fatalf("cleanupWorktree: %v", err)
	}
	if out.Refusal != "" || !out.Removed {
		t.Fatalf("want removed with no refusal, got refusal=%q removed=%v", out.Refusal, out.Removed)
	}
	if _, statErr := os.Stat(wt); !os.IsNotExist(statErr) {
		t.Fatalf("worktree %s still present after removal", wt)
	}
}

func TestCleanupWorktree_UnmergedCommits_RefusesThroughBothPaths(t *testing.T) {
	dir, repo := cleanupFixture(t)
	wt := addWorktreeBranch(t, dir, "feature", "main")
	commitIn(t, wt, "feature.txt", "feature work") // now ahead of main

	// Standalone path: landing target is main, feature carries an unreachable
	// commit -> refuse, nothing removed.
	standalone, err := cleanupWorktree(context.Background(), repo, wt, cleanupOptions{LandingTarget: "main"})
	if err != nil {
		t.Fatalf("standalone cleanupWorktree: %v", err)
	}
	if standalone.RefusalKind != refusalUnmergedWork || standalone.Removed {
		t.Fatalf("standalone: want unmerged-work refusal and nothing removed, got kind=%d removed=%v refusal=%q", standalone.RefusalKind, standalone.Removed, standalone.Refusal)
	}
	if standalone.Unmerged != 1 {
		t.Fatalf("standalone: unmerged count = %d, want 1", standalone.Unmerged)
	}

	// Merge path: same fixture, landing is the branch merged onto (main, checked
	// out in repo.Dir). The very same rule must refuse -- proving both entry
	// points share one rule set that cannot diverge.
	merge, err := cleanupWorktree(context.Background(), repo, wt, cleanupOptions{MergedBranches: []string{"feature"}})
	if err != nil {
		t.Fatalf("merge cleanupWorktree: %v", err)
	}
	if merge.RefusalKind != refusalUnmergedWork || merge.Removed {
		t.Fatalf("merge: want unmerged-work refusal and nothing removed, got kind=%d removed=%v refusal=%q", merge.RefusalKind, merge.Removed, merge.Refusal)
	}
	if _, statErr := os.Stat(wt); statErr != nil {
		t.Fatalf("worktree %s was disturbed by a refusing cleanup: %v", wt, statErr)
	}
}

func TestCleanupWorktree_LandingUnresolved_Refuses(t *testing.T) {
	dir, repo := cleanupFixture(t)
	wt := addWorktreeBranch(t, dir, "feature", "main") // no upstream, no origin/HEAD

	out, err := cleanupWorktree(context.Background(), repo, wt, cleanupOptions{})
	if err != nil {
		t.Fatalf("cleanupWorktree: %v", err)
	}
	if out.RefusalKind != refusalLandingUnresolved || out.Removed {
		t.Fatalf("want landing-unresolved refusal, got kind=%d removed=%v refusal=%q", out.RefusalKind, out.Removed, out.Refusal)
	}
}

func TestCleanupWorktree_NestedSubWorktree_Refuses(t *testing.T) {
	dir, repo := cleanupFixture(t)
	slug := addWorktreeBranch(t, dir, "slug", "main")
	nested := filepath.Join(slug, "task")
	cgit(t, dir, "worktree", "add", "-q", "-b", "task", nested, "main")

	out, err := cleanupWorktree(context.Background(), repo, slug, cleanupOptions{LandingTarget: "main"})
	if err != nil {
		t.Fatalf("cleanupWorktree: %v", err)
	}
	if out.RefusalKind != refusalLiveSubWorktree || out.Removed {
		t.Fatalf("want live-sub-worktree refusal, got kind=%d removed=%v refusal=%q", out.RefusalKind, out.Removed, out.Refusal)
	}
}

func TestCleanupWorktree_SwitchedBranch_MergePathRefuses(t *testing.T) {
	dir, repo := cleanupFixture(t)
	wt := addWorktreeBranch(t, dir, "other", "main") // on a branch we did not merge

	out, err := cleanupWorktree(context.Background(), repo, wt, cleanupOptions{MergedBranches: []string{"feature"}})
	if err != nil {
		t.Fatalf("cleanupWorktree: %v", err)
	}
	if out.RefusalKind != refusalBranchNotMerged || out.Removed {
		t.Fatalf("want branch-not-merged refusal, got kind=%d removed=%v refusal=%q", out.RefusalKind, out.Removed, out.Refusal)
	}
}

func TestCleanupWorktree_DetachedHead_Refuses(t *testing.T) {
	dir, repo := cleanupFixture(t)
	wt := filepath.Join(t.TempDir(), "detached")
	cgit(t, dir, "worktree", "add", "-q", "--detach", wt, "main")

	out, err := cleanupWorktree(context.Background(), repo, wt, cleanupOptions{LandingTarget: "main"})
	if err != nil {
		t.Fatalf("cleanupWorktree: %v", err)
	}
	if out.RefusalKind != refusalDetachedHead || out.Removed {
		t.Fatalf("want detached-head refusal, got kind=%d removed=%v refusal=%q", out.RefusalKind, out.Removed, out.Refusal)
	}
}

func TestCleanupWorktree_Force_OverridesAndReports(t *testing.T) {
	dir, repo := cleanupFixture(t)
	wt := addWorktreeBranch(t, dir, "feature", "main")
	commitIn(t, wt, "feature.txt", "feature work")

	out, err := cleanupWorktree(context.Background(), repo, wt, cleanupOptions{LandingTarget: "main", Force: true})
	if err != nil {
		t.Fatalf("cleanupWorktree: %v", err)
	}
	if !out.Removed || !out.Forced {
		t.Fatalf("force: want removed and forced, got removed=%v forced=%v refusal=%q", out.Removed, out.Forced, out.Refusal)
	}
	if out.Unmerged != 1 {
		t.Fatalf("force: want the overridden unmerged count (1) reported, got %d", out.Unmerged)
	}
	if _, statErr := os.Stat(wt); !os.IsNotExist(statErr) {
		t.Fatalf("force: worktree %s still present", wt)
	}
}

func TestCleanupWorktree_DryRun_ReportsWithoutRemoving(t *testing.T) {
	dir, repo := cleanupFixture(t)
	wt := addWorktreeBranch(t, dir, "feature", "main")

	out, err := cleanupWorktree(context.Background(), repo, wt, cleanupOptions{LandingTarget: "main", DryRun: true})
	if err != nil {
		t.Fatalf("cleanupWorktree: %v", err)
	}
	if out.Refusal != "" || out.Removed {
		t.Fatalf("dry-run: want no refusal and nothing removed, got refusal=%q removed=%v", out.Refusal, out.Removed)
	}
	if _, statErr := os.Stat(wt); statErr != nil {
		t.Fatalf("dry-run disturbed the worktree: %v", statErr)
	}
}

func TestCleanupWorktree_UnregisteredPath_Refuses(t *testing.T) {
	_, repo := cleanupFixture(t)
	out, err := cleanupWorktree(context.Background(), repo, filepath.Join(t.TempDir(), "nope"), cleanupOptions{LandingTarget: "main"})
	if err != nil {
		t.Fatalf("cleanupWorktree: %v", err)
	}
	if out.RefusalKind != refusalNotRegistered || out.Removed {
		t.Fatalf("want not-registered refusal, got kind=%d removed=%v", out.RefusalKind, out.Removed)
	}
}
