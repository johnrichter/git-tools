package cli

import (
	"testing"
	"unsafe"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/johnrichter/claude-shared-tooling/go/githooks"
)

// TestGitToolsSkipRules_WorktreePatternIsRootAnchored proves gitToolsSkipRules'
// own .claude/worktrees/** rule matches only that literal prefix at the
// scanned root, never a worktrees/ directory nested elsewhere in the tree
// (which may be legitimately tracked content unrelated to this fleet's
// nested-worktree convention) and never a .claude/worktrees/ that itself sits
// under some other directory instead of at the root.
func TestGitToolsSkipRules_WorktreePatternIsRootAnchored(t *testing.T) {
	rule := gitToolsSkipRules[len(gitToolsSkipRules)-1]
	if rule.Pattern != ".claude/worktrees/**" || rule.Class != githooks.SkipClass {
		t.Fatalf("gitToolsSkipRules' last rule = %+v, want the .claude/worktrees/** skip rule appended after githooks.DefaultSkipRules", rule)
	}

	cases := []struct {
		path string
		want bool
	}{
		{".claude/worktrees/native/foo.go", true},
		{".claude/worktrees/native/nested/deep/bar.env", true},
		{"plugins/foo/worktrees/bar.md", false},
		{"nested/.claude/worktrees/native/foo.go", false},
	}
	for _, c := range cases {
		matched, err := doublestar.Match(rule.Pattern, c.path)
		if err != nil {
			t.Fatalf("doublestar.Match(%q, %q): %v", rule.Pattern, c.path, err)
		}
		if matched != c.want {
			t.Errorf("doublestar.Match(%q, %q) = %v, want %v", rule.Pattern, c.path, matched, c.want)
		}
	}
}

// TestGitToolsSkipRules_IsDefensiveCopyOfDefaultSkipRules proves
// gitToolsSkipRules never shares backing storage with
// githooks.DefaultSkipRules: appending its own rule onto a copy, not onto
// DefaultSkipRules directly, is what keeps a future append to
// DefaultSkipRules (or to gitToolsSkipRules itself) from silently mutating
// the other.
func TestGitToolsSkipRules_IsDefensiveCopyOfDefaultSkipRules(t *testing.T) {
	if len(githooks.DefaultSkipRules) == 0 {
		t.Fatal("githooks.DefaultSkipRules is empty; nothing to compare against")
	}
	if len(gitToolsSkipRules) <= len(githooks.DefaultSkipRules) {
		t.Fatalf("gitToolsSkipRules has %d rules, want more than DefaultSkipRules' %d (its own .claude/worktrees/** rule appended)", len(gitToolsSkipRules), len(githooks.DefaultSkipRules))
	}
	if unsafe.SliceData(githooks.DefaultSkipRules) == unsafe.SliceData(gitToolsSkipRules) {
		t.Fatal("gitToolsSkipRules shares its backing array with githooks.DefaultSkipRules — mutating one through its shared storage could mutate the other")
	}
}
