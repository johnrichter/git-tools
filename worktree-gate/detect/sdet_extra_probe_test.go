package detect

import "testing"

// Adversarial: former exemption cases (CLAUDE.md, DESIGN.md-style, and other
// tracking-doc-shaped paths) at the primary checkout must all deny now, and
// the Decide signature must not accept any trackingDocs argument (compile-time
// check by call shape below).
func TestSDET_FormerExemptPaths_AllDeny(t *testing.T) {
	cases := []string{
		"/repo/CLAUDE.md",
		"/repo/.dat/some-project/notes.md",
		"/repo/AGENTS.md",
		"/repo/docs/DESIGN.md",
	}
	for _, p := range cases {
		fs := newFakeFS().dir("/repo/.git")
		d := Decide(fs.lstat, fs.readFile, nil, Verbs{}, nil, Input{
			ToolName: "Write",
			FilePath: p,
		})
		if !d.Deny {
			t.Errorf("path %s: expected deny, got allow: %+v", p, d)
		}
	}
}

// Same paths, but the call is inside a worktree: should allow (worktree
// isolation invariant unaffected by exemption removal).
func TestSDET_FormerExemptPaths_AllowInsideWorktree(t *testing.T) {
	cases := []string{
		"/repo/.claude/worktrees/wt1/CLAUDE.md",
		"/repo/.claude/worktrees/wt1/docs/DESIGN.md",
	}
	for _, p := range cases {
		fs := newFakeFS().dir("/repo/.git").file("/repo/.claude/worktrees/wt1/.git", "gitdir: /repo/.git/worktrees/wt1\n")
		d := Decide(fs.lstat, fs.readFile, nil, Verbs{}, nil, Input{
			ToolName: "Write",
			FilePath: p,
			CWD:      "/repo/.claude/worktrees/wt1",
		})
		if d.Deny {
			t.Errorf("path %s: expected allow inside worktree, got deny: %+v", p, d)
		}
	}
}
