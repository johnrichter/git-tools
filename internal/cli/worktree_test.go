package cli

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/johnrichter/claude-shared-tooling/go/git"
	"github.com/johnrichter/git-tools/internal/worktreeclean"
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

	out, err := worktreeclean.Cleanup(context.Background(), repo, wt, worktreeclean.Options{LandingTarget: "main"})
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
	standalone, err := worktreeclean.Cleanup(context.Background(), repo, wt, worktreeclean.Options{LandingTarget: "main"})
	if err != nil {
		t.Fatalf("standalone cleanupWorktree: %v", err)
	}
	if standalone.RefusalKind != worktreeclean.RefusalUnmergedWork || standalone.Removed {
		t.Fatalf("standalone: want unmerged-work refusal and nothing removed, got kind=%d removed=%v refusal=%q", standalone.RefusalKind, standalone.Removed, standalone.Refusal)
	}
	if standalone.Unmerged != 1 {
		t.Fatalf("standalone: unmerged count = %d, want 1", standalone.Unmerged)
	}

	// Merge path: same fixture, landing is the branch merged onto (main, checked
	// out in repo.Dir). The very same rule must refuse -- proving both entry
	// points share one rule set that cannot diverge.
	merge, err := worktreeclean.Cleanup(context.Background(), repo, wt, worktreeclean.Options{MergedBranches: []string{"feature"}})
	if err != nil {
		t.Fatalf("merge cleanupWorktree: %v", err)
	}
	if merge.RefusalKind != worktreeclean.RefusalUnmergedWork || merge.Removed {
		t.Fatalf("merge: want unmerged-work refusal and nothing removed, got kind=%d removed=%v refusal=%q", merge.RefusalKind, merge.Removed, merge.Refusal)
	}
	if _, statErr := os.Stat(wt); statErr != nil {
		t.Fatalf("worktree %s was disturbed by a refusing cleanup: %v", wt, statErr)
	}
}

func TestCleanupWorktree_LandingUnresolved_Refuses(t *testing.T) {
	dir, repo := cleanupFixture(t)
	wt := addWorktreeBranch(t, dir, "feature", "main") // no upstream, no origin/HEAD

	out, err := worktreeclean.Cleanup(context.Background(), repo, wt, worktreeclean.Options{})
	if err != nil {
		t.Fatalf("cleanupWorktree: %v", err)
	}
	if out.RefusalKind != worktreeclean.RefusalLandingUnresolved || out.Removed {
		t.Fatalf("want landing-unresolved refusal, got kind=%d removed=%v refusal=%q", out.RefusalKind, out.Removed, out.Refusal)
	}
}

func TestCleanupWorktree_NestedSubWorktree_Refuses(t *testing.T) {
	dir, repo := cleanupFixture(t)
	slug := addWorktreeBranch(t, dir, "slug", "main")
	nested := filepath.Join(slug, "task")
	cgit(t, dir, "worktree", "add", "-q", "-b", "task", nested, "main")

	out, err := worktreeclean.Cleanup(context.Background(), repo, slug, worktreeclean.Options{LandingTarget: "main"})
	if err != nil {
		t.Fatalf("cleanupWorktree: %v", err)
	}
	if out.RefusalKind != worktreeclean.RefusalLiveSubWorktree || out.Removed {
		t.Fatalf("want live-sub-worktree refusal, got kind=%d removed=%v refusal=%q", out.RefusalKind, out.Removed, out.Refusal)
	}
}

func TestCleanupWorktree_SwitchedBranch_MergePathRefuses(t *testing.T) {
	dir, repo := cleanupFixture(t)
	wt := addWorktreeBranch(t, dir, "other", "main") // on a branch we did not merge

	out, err := worktreeclean.Cleanup(context.Background(), repo, wt, worktreeclean.Options{MergedBranches: []string{"feature"}})
	if err != nil {
		t.Fatalf("cleanupWorktree: %v", err)
	}
	if out.RefusalKind != worktreeclean.RefusalBranchNotMerged || out.Removed {
		t.Fatalf("want branch-not-merged refusal, got kind=%d removed=%v refusal=%q", out.RefusalKind, out.Removed, out.Refusal)
	}
}

func TestCleanupWorktree_DetachedHead_Refuses(t *testing.T) {
	dir, repo := cleanupFixture(t)
	wt := filepath.Join(t.TempDir(), "detached")
	cgit(t, dir, "worktree", "add", "-q", "--detach", wt, "main")

	out, err := worktreeclean.Cleanup(context.Background(), repo, wt, worktreeclean.Options{LandingTarget: "main"})
	if err != nil {
		t.Fatalf("cleanupWorktree: %v", err)
	}
	if out.RefusalKind != worktreeclean.RefusalDetachedHead || out.Removed {
		t.Fatalf("want detached-head refusal, got kind=%d removed=%v refusal=%q", out.RefusalKind, out.Removed, out.Refusal)
	}
}

func TestCleanupWorktree_DryRun_ReportsWithoutRemoving(t *testing.T) {
	dir, repo := cleanupFixture(t)
	wt := addWorktreeBranch(t, dir, "feature", "main")

	out, err := worktreeclean.Cleanup(context.Background(), repo, wt, worktreeclean.Options{LandingTarget: "main", DryRun: true})
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

// TestCleanupWorktree_DirtyTree_RefusedAndStaysPresent covers the refusal
// SC-C5 says the rule set itself must raise, before git is ever asked to
// remove anything: a worktree whose checked-out branch is fully reachable
// from its landing target (no unmerged-work refusal fires) but whose tree
// carries an uncommitted, untracked file. No flag anywhere in this call
// chain can override it -- WorktreeRemoveOptions is never given Force.
func TestCleanupWorktree_DirtyTree_RefusedAndStaysPresent(t *testing.T) {
	dir, repo := cleanupFixture(t)
	wt := addWorktreeBranch(t, dir, "feature", "main") // no commits beyond main
	if err := os.WriteFile(filepath.Join(wt, "dirty.txt"), []byte("uncommitted\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := worktreeclean.Cleanup(context.Background(), repo, wt, worktreeclean.Options{LandingTarget: "main"})
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if out.RefusalKind != worktreeclean.RefusalDirtyTree || out.Removed {
		t.Fatalf("want dirty-tree refusal, got kind=%d removed=%v refusal=%q", out.RefusalKind, out.Removed, out.Refusal)
	}
	if len(out.UntrackedPaths) != 1 || out.UntrackedPaths[0] != "dirty.txt" {
		t.Fatalf("UntrackedPaths = %v, want [dirty.txt]", out.UntrackedPaths)
	}
	if len(out.ModifiedPaths) != 0 {
		t.Fatalf("ModifiedPaths = %v, want none", out.ModifiedPaths)
	}
	if _, statErr := os.Stat(wt); statErr != nil {
		t.Fatalf("worktree %s was removed despite being dirty: %v", wt, statErr)
	}
}

// TestCleanupWorktree_ModifiedTrackedFile_RefusesAndListsPath proves a
// tracked file changed in place (no new untracked path) is caught too, and
// lands in ModifiedPaths rather than UntrackedPaths.
func TestCleanupWorktree_ModifiedTrackedFile_RefusesAndListsPath(t *testing.T) {
	dir, repo := cleanupFixture(t)
	wt := addWorktreeBranch(t, dir, "feature", "main")
	if err := os.WriteFile(filepath.Join(wt, "base.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := worktreeclean.Cleanup(context.Background(), repo, wt, worktreeclean.Options{LandingTarget: "main"})
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if out.RefusalKind != worktreeclean.RefusalDirtyTree || out.Removed {
		t.Fatalf("want dirty-tree refusal, got kind=%d removed=%v refusal=%q", out.RefusalKind, out.Removed, out.Refusal)
	}
	if len(out.ModifiedPaths) != 1 || out.ModifiedPaths[0] != "base.txt" {
		t.Fatalf("ModifiedPaths = %v, want [base.txt]", out.ModifiedPaths)
	}
	if len(out.UntrackedPaths) != 0 {
		t.Fatalf("UntrackedPaths = %v, want none", out.UntrackedPaths)
	}
	if !strings.Contains(out.Refusal, "commit it") || !strings.Contains(out.Refusal, "ignore it") || !strings.Contains(out.Refusal, "delete it deliberately") {
		t.Fatalf("refusal %q does not name all three remedies", out.Refusal)
	}
	if strings.Contains(out.Refusal, "force") || strings.Contains(out.Refusal, "--force") {
		t.Fatalf("refusal %q still names the removed --force override", out.Refusal)
	}
	if _, statErr := os.Stat(wt); statErr != nil {
		t.Fatalf("worktree %s was removed despite being dirty: %v", wt, statErr)
	}
}

// TestCleanupWorktree_UntrackedAndModifiedTogether_ListsBoth proves both
// categories are reported in the same refusal when both are present.
func TestCleanupWorktree_UntrackedAndModifiedTogether_ListsBoth(t *testing.T) {
	dir, repo := cleanupFixture(t)
	wt := addWorktreeBranch(t, dir, "feature", "main")
	if err := os.WriteFile(filepath.Join(wt, "base.txt"), []byte("changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "new.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := worktreeclean.Cleanup(context.Background(), repo, wt, worktreeclean.Options{LandingTarget: "main"})
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if out.RefusalKind != worktreeclean.RefusalDirtyTree || out.Removed {
		t.Fatalf("want dirty-tree refusal, got kind=%d removed=%v refusal=%q", out.RefusalKind, out.Removed, out.Refusal)
	}
	if len(out.UntrackedPaths) != 1 || out.UntrackedPaths[0] != "new.txt" {
		t.Fatalf("UntrackedPaths = %v, want [new.txt]", out.UntrackedPaths)
	}
	if len(out.ModifiedPaths) != 1 || out.ModifiedPaths[0] != "base.txt" {
		t.Fatalf("ModifiedPaths = %v, want [base.txt]", out.ModifiedPaths)
	}
}

// TestCleanupWorktree_IgnoredFileAlone_IsNotDirt proves an ignored file, on
// its own, does not trip the dirty-tree refusal -- the operator ruling is
// that only an untracked (non-ignored) or modified tracked file is a signal.
func TestCleanupWorktree_IgnoredFileAlone_IsNotDirt(t *testing.T) {
	dir, repo := cleanupFixture(t)
	// Commit the ignore rule on main before branching, so feature starts out
	// fully landed and the only thing left in its tree is the ignored file.
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("ignored.txt\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cgit(t, dir, "add", ".gitignore")
	cgit(t, dir, "commit", "-q", "-m", "ignore ignored.txt")
	wt := addWorktreeBranch(t, dir, "feature", "main")
	if err := os.WriteFile(filepath.Join(wt, "ignored.txt"), []byte("ignored\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out, err := worktreeclean.Cleanup(context.Background(), repo, wt, worktreeclean.Options{LandingTarget: "main"})
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if out.RefusalKind == worktreeclean.RefusalDirtyTree {
		t.Fatalf("an ignored-only file wrongly tripped the dirty-tree refusal: %+v", out)
	}
	if out.Refusal != "" || !out.Removed {
		t.Fatalf("want removal with no refusal, got refusal=%q removed=%v", out.Refusal, out.Removed)
	}
}

// TestCleanupWorktree_UnmergedWork_StatesCountAndLandingTarget proves the
// unmerged-work refusal names both the commit count and the landing target
// it measured against -- the dirty-tree rule must not shadow this refusal
// when the tree itself is clean.
func TestCleanupWorktree_UnmergedWork_StatesCountAndLandingTarget(t *testing.T) {
	dir, repo := cleanupFixture(t)
	wt := addWorktreeBranch(t, dir, "feature", "main")
	commitIn(t, wt, "feature.txt", "feature work")
	commitIn(t, wt, "feature2.txt", "more feature work")

	out, err := worktreeclean.Cleanup(context.Background(), repo, wt, worktreeclean.Options{LandingTarget: "main"})
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if out.RefusalKind != worktreeclean.RefusalUnmergedWork || out.Removed {
		t.Fatalf("want unmerged-work refusal, got kind=%d removed=%v refusal=%q", out.RefusalKind, out.Removed, out.Refusal)
	}
	if !strings.Contains(out.Refusal, "2") || !strings.Contains(out.Refusal, "main") {
		t.Fatalf("refusal %q does not state the commit count and landing target", out.Refusal)
	}
}

func TestCleanupWorktree_UnregisteredPath_Refuses(t *testing.T) {
	_, repo := cleanupFixture(t)
	out, err := worktreeclean.Cleanup(context.Background(), repo, filepath.Join(t.TempDir(), "nope"), worktreeclean.Options{LandingTarget: "main"})
	if err != nil {
		t.Fatalf("cleanupWorktree: %v", err)
	}
	if out.RefusalKind != worktreeclean.RefusalNotRegistered || out.Removed {
		t.Fatalf("want not-registered refusal, got kind=%d removed=%v", out.RefusalKind, out.Removed)
	}
}

// --- Force-removal regression guard ------------------------------------------
//
// SC-C1 requires that no git-tools path pass Force: true to WorktreeRemove --
// scoped, per this task's file surface, to the merge/worktree-remove CLI
// path: internal/cli and internal/worktreeclean. (worktree-gate/lifecycle is
// a distinct pool-management tool with its own, unrelated Force option --
// out of this task's scope, not touched here.) This is a static, adversarial
// guard against the plumbing being re-threaded by a later change: it parses
// every WorktreeRemove(...) call in that source (balancing parens itself,
// since a simple substring/line-based grep would miss a call whose Force
// field sits on a different line than "WorktreeRemove(") and fails if any
// call's argument text mentions Force at all -- true, false, or a variable --
// since Options no longer has a Force field for any of those to name.
func TestNoWorktreeRemoveCallSitePassesForce(t *testing.T) {
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	scanRoots := []string{
		filepath.Join(repoRoot, "internal", "cli"),
		filepath.Join(repoRoot, "internal", "worktreeclean"),
	}
	var calls []string
	for _, root := range scanRoots {
		walkErr := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return nil
			}
			src, readErr := os.ReadFile(path)
			if readErr != nil {
				return readErr
			}
			calls = append(calls, extractCalls(string(src), "WorktreeRemove(")...)
			return nil
		})
		if walkErr != nil {
			t.Fatal(walkErr)
		}
	}
	if len(calls) == 0 {
		t.Fatal("found no WorktreeRemove call site to check -- the guard itself is broken")
	}
	for _, call := range calls {
		if strings.Contains(call, "Force") {
			t.Fatalf("a WorktreeRemove call site still mentions Force: %s", call)
		}
	}
}

// extractCalls returns, for every occurrence of marker in src, the balanced-
// paren argument text that follows it (marker's own trailing "(" already
// opens depth 1).
func extractCalls(src, marker string) []string {
	var out []string
	for i := 0; ; {
		idx := strings.Index(src[i:], marker)
		if idx < 0 {
			break
		}
		start := i + idx + len(marker)
		depth := 1
		j := start
		for ; j < len(src) && depth > 0; j++ {
			switch src[j] {
			case '(':
				depth++
			case ')':
				depth--
			}
		}
		out = append(out, src[start:j])
		i = j
	}
	return out
}
