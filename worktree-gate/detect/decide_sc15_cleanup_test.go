package detect

import "testing"

// TestSC15VerbAllowed_WorktreeRemoveSanctioned pins that worktree remove joins
// the sanctioned landing verbs, that a bare worktree (no subverb) and every
// non-landing verb still fall through, so sanctioning the standalone cleanup
// opened exactly one new verb and nothing else.
func TestSC15VerbAllowed_WorktreeRemoveSanctioned(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"merge", []string{"merge", "main"}, true},
		{"push", []string{"push", "origin", "main"}, true},
		{"worktree-add", []string{"worktree", "add", "/p", "main"}, true},
		{"worktree-remove", []string{"worktree", "remove", "/p"}, true},
		{"worktree-bare", []string{"worktree"}, false},
		{"worktree-list-not-a-landing-verb", []string{"worktree", "list"}, false},
		{"worktree-prune-not-sanctioned", []string{"worktree", "prune"}, false},
		{"resign-excluded", []string{"resign", "main"}, false},
		{"empty", nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sc15VerbAllowed(c.args); got != c.want {
				t.Errorf("sc15VerbAllowed(%q) = %v, want %v", c.args, got, c.want)
			}
		})
	}
}

// TestSC15ForcesCleanup_OnlyCleanupVerbs pins the gate's refusal meaning of
// --force: it voids the sanction only for the two cleanup paths (merge and
// worktree remove), in both the bare and glued spellings, and leaves worktree
// add's own --force sanctioned. A cleanup verb without --force is not voided.
func TestSC15ForcesCleanup_OnlyCleanupVerbs(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want bool
	}{
		{"merge-force", []string{"merge", "main", "--force"}, true},
		{"merge-force-glued", []string{"merge", "main", "--force=true"}, true},
		{"merge-cleanup-force", []string{"merge", "main", "--cleanup", "--force"}, true},
		{"merge-cleanup-only-not-voided", []string{"merge", "main", "--cleanup"}, false},
		{"merge-plain-not-voided", []string{"merge", "main"}, false},
		{"worktree-remove-force", []string{"worktree", "remove", "/p", "--force"}, true},
		{"worktree-remove-plain-not-voided", []string{"worktree", "remove", "/p"}, false},
		{"worktree-add-force-stays-sanctioned", []string{"worktree", "add", "/p", "main", "--force"}, false},
		{"push-force-not-a-cleanup-verb", []string{"push", "origin", "main", "--force"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sc15ForcesCleanup(c.args); got != c.want {
				t.Errorf("sc15ForcesCleanup(%q) = %v, want %v", c.args, got, c.want)
			}
		})
	}
}
