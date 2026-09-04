// Tests for the three new directory/ref selectors: -C as a working alias for
// --repo, --worktree resolving a linked worktree by name, and --branch
// standing in for sign/resign/rebase's positional ref — plus the usage
// errors each one's own contract requires (mutual exclusion with -C/--repo,
// a name that does not resolve, a name that resolves to the main worktree,
// and a positional ref given alongside --branch).
package cli_test

import (
	"path/filepath"
	"testing"
)

// TestDashC_AliasWorksAsRepoSelector proves -C and --repo share one flag: a
// verb driven with -C behaves exactly as TestSign_ResignsTipCommitWithIdenticalTree
// does with --repo.
func TestDashC_AliasWorksAsRepoSelector(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	configureSigningKey(t, dir)
	oldHead := commitFile(t, dir, "next.txt", "next\n", "next")
	oldTree := runGit(t, dir, "rev-parse", "HEAD^{tree}")

	r, exit := runCLI(t, bin, "-C", dir, "sign", "HEAD")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	newHead, _ := r.Data["new_head"].(string)
	if newHead == "" || newHead == oldHead {
		t.Fatalf("-C sign did not produce a new head: %+v", r.Data)
	}
	if got := runGit(t, dir, "rev-parse", newHead+"^{tree}"); got != oldTree {
		t.Fatalf("-C sign changed the tree: old %s new %s", oldTree, got)
	}
}

// TestWorktreeFlag_ResolvesToLinkedWorktree proves --worktree opens the
// named linked worktree, not the main one: the process runs from the main
// checkout (via runCLIIn, no --repo/-C given) yet sign's target is
// "review"'s own HEAD, and the main checkout's HEAD is left untouched.
func TestWorktreeFlag_ResolvesToLinkedWorktree(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	configureSigningKey(t, dir)
	mainHead := commitFile(t, dir, "main.txt", "main\n", "main second")

	wtPath := filepath.Join(t.TempDir(), "review")
	runGit(t, dir, "worktree", "add", "-b", "review", wtPath, "HEAD")
	reviewHead := commitFile(t, wtPath, "review.txt", "review\n", "review third")
	reviewTree := runGit(t, wtPath, "rev-parse", "HEAD^{tree}")

	r, exit := runCLIIn(t, bin, dir, "--worktree", "review", "sign", "HEAD")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	newHead, _ := r.Data["new_head"].(string)
	if newHead == "" || newHead == reviewHead {
		t.Fatalf("--worktree sign did not produce a new head: %+v", r.Data)
	}
	if got := runGit(t, wtPath, "rev-parse", newHead+"^{tree}"); got != reviewTree {
		t.Fatalf("--worktree sign changed the tree: old %s new %s", reviewTree, got)
	}
	if got := runGit(t, wtPath, "rev-parse", "HEAD"); got != newHead {
		t.Fatalf("review's HEAD did not move to the resigned commit: got %s want %s", got, newHead)
	}
	if got := runGit(t, dir, "rev-parse", "HEAD"); got != mainHead {
		t.Fatalf("the main checkout's HEAD moved from %s to %s; --worktree must retarget, not the main checkout too", mainHead, got)
	}
}

// TestWorktreeFlag_NoMatch_IsUsageError covers a --worktree name that
// `git worktree list` does not carry.
func TestWorktreeFlag_NoMatch_IsUsageError(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)

	r, exit := runCLIIn(t, bin, dir, "--worktree", "does-not-exist", "branch", "list")
	if r.Status != "usage" || exit != 50 {
		t.Fatalf("status=%s exit=%d, want usage/50: %+v", r.Status, exit, r)
	}
}

// TestWorktreeFlag_MainWorktree_IsUsageError covers a --worktree name that
// resolves to the repository's own main working tree (always list[0]),
// which must be refused rather than silently treated as a no-op selector.
func TestWorktreeFlag_MainWorktree_IsUsageError(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	name := filepath.Base(dir)

	r, exit := runCLIIn(t, bin, dir, "--worktree", name, "branch", "list")
	if r.Status != "usage" || exit != 50 {
		t.Fatalf("status=%s exit=%d, want usage/50: %+v", r.Status, exit, r)
	}
}

// TestWorktreeFlag_WithDashC_IsUsageError covers -C/--repo and --worktree
// both naming a directory: neither spelling wins over the other, so passing
// both is refused.
func TestWorktreeFlag_WithDashC_IsUsageError(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)

	r, exit := runCLI(t, bin, "-C", dir, "--worktree", "review", "branch", "list")
	if r.Status != "usage" || exit != 50 {
		t.Fatalf("status=%s exit=%d, want usage/50: %+v", r.Status, exit, r)
	}
}

// TestBranchFlag_SignUsesFlagInPlaceOfPositional mirrors
// TestSign_ResignsTipCommitWithIdenticalTree with --branch replacing the
// positional ref.
func TestBranchFlag_SignUsesFlagInPlaceOfPositional(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	configureSigningKey(t, dir)
	oldHead := commitFile(t, dir, "next.txt", "next\n", "next")
	oldTree := runGit(t, dir, "rev-parse", "HEAD^{tree}")

	r, exit := runCLI(t, bin, "--repo", dir, "sign", "--branch", "HEAD")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	newHead, _ := r.Data["new_head"].(string)
	if newHead == "" || newHead == oldHead {
		t.Fatalf("--branch sign did not produce a new head: %+v", r.Data)
	}
	if got := runGit(t, dir, "rev-parse", newHead+"^{tree}"); got != oldTree {
		t.Fatalf("--branch sign changed the tree: old %s new %s", oldTree, got)
	}
}

// TestBranchFlag_ResignUsesFlagInPlaceOfPositional mirrors
// TestResign_RangeAcrossBase with --branch replacing the positional ref.
func TestBranchFlag_ResignUsesFlagInPlaceOfPositional(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	configureSigningKey(t, dir)
	base := runGit(t, dir, "rev-parse", "HEAD")
	commitFile(t, dir, "a.txt", "a\n", "a")
	oldHead := commitFile(t, dir, "b.txt", "b\n", "b")

	r, exit := runCLI(t, bin, "--repo", dir, "resign", "--base", base, "--branch", "HEAD")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	newHead, _ := r.Data["new_head"].(string)
	if newHead == "" || newHead == oldHead {
		t.Fatalf("--branch resign did not produce a new head: %+v", r.Data)
	}
}

// TestBranchFlag_RebaseUsesFlagInPlaceOfPositional mirrors
// TestRebase_ReplaysCommitsOntoUpstream with --branch replacing the
// positional upstream.
func TestBranchFlag_RebaseUsesFlagInPlaceOfPositional(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	runGit(t, dir, "branch", "feature")
	commitFile(t, dir, "main-only.txt", "main\n", "main advances")
	runGit(t, dir, "checkout", "-q", "feature")
	commitFile(t, dir, "feature-only.txt", "feature\n", "feature work")

	r, exit := runCLI(t, bin, "--repo", dir, "rebase", "--branch", "main")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	mainHead := runGit(t, dir, "rev-parse", "main")
	featureParent := runGit(t, dir, "rev-parse", "HEAD^")
	if featureParent != mainHead {
		t.Fatalf("feature was not replayed onto main: parent=%s main=%s", featureParent, mainHead)
	}
}

// TestBranchFlag_PositionalAndFlagTogether_IsUsageError covers, for each of
// the three verbs that register --branch, a positional ref given alongside
// it — both name the same thing a different way, so the pair is refused
// before either value is used for anything.
func TestBranchFlag_PositionalAndFlagTogether_IsUsageError(t *testing.T) {
	bin := buildCLI(t)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"sign", []string{"sign", "HEAD", "--branch", "HEAD"}},
		{"resign", []string{"resign", "--base", "HEAD", "HEAD", "--branch", "HEAD"}},
		{"rebase", []string{"rebase", "main", "--branch", "main"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := initRepo(t)
			r, exit := runCLI(t, bin, append([]string{"--repo", dir}, tc.args...)...)
			if r.Status != "usage" || exit != 50 {
				t.Fatalf("status=%s exit=%d, want usage/50: %+v", r.Status, exit, r)
			}
		})
	}
}

// TestRebase_NoUpstreamGiven_IsUsageError covers rebase's own case:
// resolveRefSelector's def is empty for rebase alone among the three verbs,
// since it has no ref that makes sense without one, so neither a positional
// upstream nor --branch leaves nothing to rebase onto.
func TestRebase_NoUpstreamGiven_IsUsageError(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)

	r, exit := runCLI(t, bin, "--repo", dir, "rebase")
	if r.Status != "usage" || exit != 50 {
		t.Fatalf("status=%s exit=%d, want usage/50: %+v", r.Status, exit, r)
	}
}
