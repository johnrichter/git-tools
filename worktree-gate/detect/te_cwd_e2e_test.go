package detect

// End-to-end adversarial coverage for M3.P1.T1's decideBash cwd resolution:
// drives Decide itself (not just resolveEffectiveCWD) so the verdict, not
// just the resolved string, is pinned against SC-CWD-RESOLVER-CONTRACT
// cases and topology combinations the shared corpus and existing suites
// don't already combine.

import "testing"

// TestDecide_Bash_EffectiveCWD_DrivesVerdict_NotSessionCWD covers both
// directions: an effective cwd that lands in a worktree allows even though
// the session cwd is the primary checkout, and the reverse -- an effective
// cwd that lands back in the primary checkout denies even though the
// session cwd started in a worktree. Session-cwd-only resolution (the pre-
// fix behavior) would get both of these backwards.
func TestDecide_Bash_EffectiveCWD_DrivesVerdict_NotSessionCWD(t *testing.T) {
	fs := newFakeFS().
		dir("/repo/.git").
		file("/repo/wt/.git", "gitdir: /repo/.git/worktrees/wt\n")
	v := testVerbs(t)

	cases := []struct {
		name       string
		sessionCWD string
		command    string
		wantDeny   bool
	}{
		{
			name:       "session-primary-cd-into-worktree-allows",
			sessionCWD: "/repo",
			command:    "cd wt && rm -rf build",
			wantDeny:   false,
		},
		{
			name:       "session-worktree-cd-back-to-primary-denies",
			sessionCWD: "/repo/wt",
			command:    "cd /repo && rm -rf build",
			wantDeny:   true,
		},
		{
			name:       "session-primary-chained-relative-cd-into-worktree-allows",
			sessionCWD: "/repo",
			command:    "cd wt && cd . && rm -rf build",
			wantDeny:   false,
		},
		{
			name:       "session-primary-dash-C-into-worktree-still-write-classified-allows",
			sessionCWD: "/repo",
			command:    "git -C wt commit -am x",
			wantDeny:   false,
		},
		{
			name:       "unresolvable-cd-target-denies-regardless-of-session-topology",
			sessionCWD: "/repo/wt",
			command:    "cd \"$TARGET\" && rm -rf build",
			wantDeny:   true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{
				ToolName: "Bash", CWD: c.sessionCWD, Command: c.command,
			})
			if d.Deny != c.wantDeny {
				t.Errorf("Decide(CWD=%q, Command=%q) deny=%v, want deny=%v (reason=%q)",
					c.sessionCWD, c.command, d.Deny, c.wantDeny, d.Reason)
			}
		})
	}
}

// TestDecide_Bash_SC9_Variants extends the single opaque-script fixture
// with the exact residual named by the task: a primary-checkout ./build.sh
// that writes .claude/settings.json is denied purely because the command
// is unclassifiable, not because the gate inspected the script's contents
// or its target file.
func TestDecide_Bash_SC9_Variants(t *testing.T) {
	fs := primaryFS()
	v := testVerbs(t)

	cases := []struct {
		name    string
		command string
	}{
		{"relative-opaque-script", "./build.sh"},
		{"opaque-script-with-args", "./build.sh --release"},
		{"opaque-script-piped", "./build.sh | tee build.log"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{
				ToolName: "Bash", CWD: "/repo", Command: c.command,
			})
			if !d.Deny {
				t.Errorf("SC9: %q from a primary checkout must deny (opaque script, conservative default); got allow", c.command)
			}
		})
	}
}
