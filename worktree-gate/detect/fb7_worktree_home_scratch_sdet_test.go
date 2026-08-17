package detect

import "testing"

// TestSDET_FB7_LiveWorktreeUnderHome_UnaffectedByExemption pins acceptance
// criterion #2: a path under the worktree home that IS a live worktree itself
// (its own .git redirect resolves KindWorktree) must continue to return
// Decision{} via the KindWorktree branch, never reaching
// isWorktreeHomeScratch. This guards against a future refactor that checks
// the scratch-home membership before the worktree-liveness walk, which would
// still allow the call but for the wrong reason and could regress if the
// exemption's scope narrows later.
func TestSDET_FB7_LiveWorktreeUnderHome_UnaffectedByExemption(t *testing.T) {
	v := testVerbs(t)
	fs := newFakeFS().
		dir("/repo/.git").
		file("/repo/.claude/worktrees/live/.git", "gitdir: /repo/.git/worktrees/live\n").
		file("/repo/tracked.md", "tracked\n")

	// The named-path leg's KindWorktree branch (continue, unaffected by FB7)
	// is only one of two legs the overall Decide verdict depends on: the cwd
	// leg independently denies any write-class piece run FROM a primary
	// checkout regardless of what its named target resolves to (SC20's
	// pre-existing, unrelated cwd short-circuit). So the named-target leg is
	// pinned from the two cwd positions where it is decisive; the primary-cwd
	// case is asserted separately below to show the denial there traces to
	// the cwd leg, not to any regression in the target's own KindWorktree
	// classification.
	for _, cwd := range []string{"/tmp", "/repo/.claude/worktrees/live"} {
		t.Run("cwd="+cwd, func(t *testing.T) {
			d := Decide(fs.lstat, fs.readFile, v, nil, Input{
				ToolName: "Bash",
				CWD:      cwd,
				Command:  "rm /repo/.claude/worktrees/live",
			})
			if d.Deny {
				t.Errorf("live worktree under home: got deny=true reason=%q, want allowed (KindWorktree short-circuit)", d.Reason)
			}
		})
	}

	t.Run("cwd=/repo (denied by the cwd leg, not the target)", func(t *testing.T) {
		d := Decide(fs.lstat, fs.readFile, v, nil, Input{
			ToolName: "Bash",
			CWD:      "/repo",
			Command:  "rm /repo/.claude/worktrees/live",
		})
		if !d.Deny {
			t.Errorf("rm from primary-checkout cwd: got allowed, want denied by the cwd leg (pre-existing, unrelated to FB7)")
		}
	})
}

// TestSDET_FB7_ScratchExemption_Boundaries adversarially probes the
// isWorktreeHomeScratch boundary: the home directory itself, a sibling
// directory whose name merely shares the "worktrees" prefix, a nested scratch
// path two levels deep, and the exemption's interaction with cwd -- it must
// deny from a primary-checkout cwd (the disclosed residual) and allow from
// outside-any-repo and inside-a-worktree cwds, exactly as pinned in the
// corpus, but here exercised with an independently constructed fixture to
// catch a corpus-only false green.
func TestSDET_FB7_ScratchExemption_Boundaries(t *testing.T) {
	v := testVerbs(t)
	fs := newFakeFS().
		dir("/repo/.git").
		file("/repo/wt/.git", "gitdir: /repo/.git/worktrees/wt\n").
		file("/repo/tracked.md", "tracked\n")

	cases := []struct {
		name      string
		target    string
		cwd       string
		wantDeny  bool
		wantAllow bool
	}{
		{
			name:     "home-directory-itself-from-outside-repo",
			target:   "/repo/.claude/worktrees",
			cwd:      "/tmp",
			wantDeny: true, // rel == "." excluded by isWorktreeHomeScratch: this IS the home, not a descendant
		},
		{
			name:     "sibling-lookalike-name-not-exempt",
			target:   "/repo/.claude/worktrees-extra/thing",
			cwd:      "/tmp",
			wantDeny: true, // must not match on string prefix alone
		},
		{
			name:     "nested-two-levels-deep-scratch-allowed-outside-repo",
			target:   "/repo/.claude/worktrees/dangling/nested/file",
			cwd:      "/tmp",
			wantDeny: false,
		},
		{
			name:     "nested-two-levels-deep-scratch-allowed-inside-worktree",
			target:   "/repo/.claude/worktrees/dangling/nested/file",
			cwd:      "/repo/wt",
			wantDeny: false,
		},
		{
			name:     "nested-two-levels-deep-scratch-denied-from-primary-cwd",
			target:   "/repo/.claude/worktrees/dangling/nested/file",
			cwd:      "/repo",
			wantDeny: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := Decide(fs.lstat, fs.readFile, v, nil, Input{
				ToolName: "Bash",
				CWD:      c.cwd,
				Command:  "rm " + c.target,
			})
			if d.Deny != c.wantDeny {
				t.Errorf("rm %s (cwd=%s): deny=%v, want deny=%v (reason=%q)", c.target, c.cwd, d.Deny, c.wantDeny, d.Reason)
			}
		})
	}
}

// TestSDET_FB7_WriteFileTool_NotExempted pins that the FB7 exemption is
// wired only into the Bash named-path leg (SC20), never into the Write/Edit
// tool's decideFileWrite path, so a Write/Edit whose FilePath resolves under
// the worktree home in a primary checkout must still deny via the existing
// KindPrimary branch.
func TestSDET_FB7_WriteFileTool_NotExempted(t *testing.T) {
	fs := newFakeFS().
		dir("/repo/.git").
		file("/repo/tracked.md", "tracked\n")

	d := Decide(fs.lstat, fs.readFile, testVerbs(t), nil, Input{
		ToolName: "Write",
		FilePath: "/repo/.claude/worktrees/scratch-thing",
	})
	if !d.Deny {
		t.Errorf("Write into worktree-home path under primary checkout: got allowed, want denied (FB7 is Bash-only)")
	}
}
