package detect

import "testing"

// TestSC15VerbAllowed_WorktreeRemoveSanctioned pins that worktree remove and
// resign join the sanctioned landing verbs, that a bare worktree (no subverb)
// and every non-landing verb still fall through, so sanctioning these two
// standalone verbs opened exactly them and nothing else. A leading persistent
// flag (--repo, --config) shifts the verb's position but must not shift which
// verbs this admits: gitToolsOperands skips it here the same way it already
// does for gitToolsDestinations, so the two checks agree on where the verb
// sits.
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
		{"resign-sanctioned", []string{"resign", "main"}, true},
		{"empty", nil, false},
		{"repo-flag-before-verb-still-sanctioned", []string{"--repo", "/other", "worktree", "add", "/p", "main"}, true},
		{"glued-repo-flag-before-verb-still-sanctioned", []string{"--repo=/other", "worktree", "remove", "/p"}, true},
		{"repo-flag-before-non-landing-verb-still-refused", []string{"--repo", "/other", "worktree", "prune"}, false},
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
// Neither cleanup verb's current CLI declares a --force flag at all; this
// predicate defends against an older provisioned binary whose merge and
// worktree remove once accepted one (see sc15ForcesCleanup), so a case here
// stays pinned even though today's binary can never produce it. A leading
// persistent flag must not hide the cleanup verb from this check either --
// the same shift that sc15VerbAllowed corrects for must be corrected here too,
// or a --force riding a shifted-position cleanup verb would go undetected.
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
		{"repo-flag-before-merge-force-still-detected", []string{"--repo", "/other", "merge", "main", "--force"}, true},
		{"repo-flag-before-worktree-remove-force-still-detected", []string{"--repo=/other", "worktree", "remove", "/p", "--force"}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sc15ForcesCleanup(c.args); got != c.want {
				t.Errorf("sc15ForcesCleanup(%q) = %v, want %v", c.args, got, c.want)
			}
		})
	}
}
