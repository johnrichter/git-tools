package detect

import (
	"encoding/hex"
	"testing"
)

// TestDecide_Bash_SC15_ConfigRepoStackedCombinations is the test-engineer
// adversarial pass for focus area 1: every stacking, ordering, and spelling
// combination of --config and --repo on a single landing-verb call. --repo
// must still void the sanctioned-landing exemption (sc15Retargets) in every
// combination that carries it, whatever position or spelling; --config alone,
// however many times repeated or wherever placed relative to the verb word,
// must never void it.
func TestDecide_Bash_SC15_ConfigRepoStackedCombinations(t *testing.T) {
	v := testVerbs(t)
	correctDigest := hex.EncodeToString(sha256Sum(sc15BinContent))

	cases := []struct {
		name     string
		command  string
		wantDeny bool
	}{
		// --config alone: never retargets, whatever its position or spelling.
		{name: "config-spaced-before-verb-allowed", command: sc15Bin + " --config /other merge main"},
		{name: "config-glued-before-verb-allowed", command: sc15Bin + " --config=/other merge main"},
		{name: "config-spaced-after-verb-allowed", command: sc15Bin + " merge --config /other main"},
		{name: "config-glued-after-verb-allowed", command: sc15Bin + " merge --config=/other main"},
		{name: "config-repeated-allowed", command: sc15Bin + " merge --config /a --config /b main"},

		// --repo alone: always retargets, whatever its position or spelling.
		{name: "repo-spaced-before-verb-denies", command: sc15Bin + " --repo /other merge main", wantDeny: true},
		{name: "repo-glued-before-verb-denies", command: sc15Bin + " --repo=/other merge main", wantDeny: true},
		{name: "repo-spaced-after-verb-denies", command: sc15Bin + " merge --repo /other main", wantDeny: true},
		{name: "repo-glued-after-verb-denies", command: sc15Bin + " merge --repo=/other main", wantDeny: true},

		// Both flags present together, in every order and spelling combination:
		// --repo must still void regardless of --config's presence or position.
		{name: "config-then-repo-both-spaced-denies", command: sc15Bin + " merge --config /other --repo /other main", wantDeny: true},
		{name: "repo-then-config-both-spaced-denies", command: sc15Bin + " merge --repo /other --config /other main", wantDeny: true},
		{name: "config-then-repo-both-glued-denies", command: sc15Bin + " merge --config=/other --repo=/other main", wantDeny: true},
		{name: "repo-then-config-both-glued-denies", command: sc15Bin + " merge --repo=/other --config=/other main", wantDeny: true},
		{name: "config-spaced-repo-glued-denies", command: sc15Bin + " merge --config /other --repo=/other main", wantDeny: true},
		{name: "repo-glued-config-spaced-denies", command: sc15Bin + " merge --repo=/other --config /other main", wantDeny: true},
		{name: "config-glued-repo-spaced-denies", command: sc15Bin + " merge --config=/other --repo /other main", wantDeny: true},
		{name: "repo-spaced-config-glued-denies", command: sc15Bin + " merge --repo /other --config=/other main", wantDeny: true},
		{name: "both-before-verb-config-first-denies", command: sc15Bin + " --config /other --repo /other merge main", wantDeny: true},
		{name: "both-before-verb-repo-first-denies", command: sc15Bin + " --repo /other --config /other merge main", wantDeny: true},
		{name: "repo-before-verb-config-after-denies", command: sc15Bin + " --repo /other merge --config /other main", wantDeny: true},
		{name: "config-before-verb-repo-after-denies", command: sc15Bin + " --config /other merge --repo /other main", wantDeny: true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := newFakeFS().dir("/repo/.git").file(sc15Bin, sc15BinContent)
			d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{
				ToolName: "Bash", CWD: "/repo", Command: c.command,
				ProvisionedBinPath: sc15Bin, ProvisionedBinDigest: correctDigest,
			})
			if d.Deny != c.wantDeny {
				t.Errorf("Decide(cmd=%q) deny=%v, want %v (reason=%q)", c.command, d.Deny, c.wantDeny, d.Reason)
			}
		})
	}
}

// TestDecide_Bash_SC15_ConfigValueCommandSubstitution_NotASmugglingPath is
// the test-engineer adversarial pass for focus area 2: a command
// substitution or backtick span embedded in --config's OWN value must not
// gain any new smuggling advantage now that --config's value is no longer
// treated as a retarget/destination path. SC16's interior scan
// (decomposableInteriors, applied in scanBash regardless of any SC15
// exemption -- see scanBash's comment on why the exemption never skips it)
// recurses into any $(...) or backtick span anywhere in the raw command line,
// including inside a flag's value, and classifies the interior on its own
// merits. A malicious interior must still be caught; a benign one must still
// leave the sanctioned merge call allowed.
func TestDecide_Bash_SC15_ConfigValueCommandSubstitution_NotASmugglingPath(t *testing.T) {
	v := testVerbs(t)
	correctDigest := hex.EncodeToString(sha256Sum(sc15BinContent))

	cases := []struct {
		name     string
		command  string
		wantDeny bool
	}{
		// A malicious write smuggled inside --config's value via $(...) must
		// still be caught by SC16's interior scan, independent of whatever
		// gitToolsDestinations/sc15Retargets now do with --config's own value.
		{
			name:     "dollar-paren-substitution-inside-config-value-writes-tracked-file-denies",
			command:  sc15Bin + " merge --config $(rm -rf /repo/tracked.md) main",
			wantDeny: true,
		},
		{
			name:     "dollar-paren-substitution-glued-config-writes-tracked-file-denies",
			command:  sc15Bin + " merge --config=$(rm -rf /repo/tracked.md) main",
			wantDeny: true,
		},
		// Backtick form of the same smuggled write.
		{
			name:     "backtick-substitution-inside-config-value-writes-tracked-file-denies",
			command:  sc15Bin + " merge --config `rm -rf /repo/tracked.md` main",
			wantDeny: true,
		},
		// A benign interior (a read) inside --config's value must not force a
		// false deny: the sanctioned merge call, plus a harmless nested read,
		// stays allowed.
		{
			name:     "dollar-paren-substitution-inside-config-value-benign-read-allowed",
			command:  sc15Bin + " merge --config $(git status) main",
			wantDeny: false,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := newFakeFS().dir("/repo/.git").file("/repo/tracked.md", "x\n").file(sc15Bin, sc15BinContent)
			d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{
				ToolName: "Bash", CWD: "/repo", Command: c.command,
				ProvisionedBinPath: sc15Bin, ProvisionedBinDigest: correctDigest,
			})
			if d.Deny != c.wantDeny {
				t.Errorf("Decide(cmd=%q) deny=%v, want %v (reason=%q)", c.command, d.Deny, c.wantDeny, d.Reason)
			}
		})
	}
}

// TestDecide_Bash_SC15_ConfigAloneAcrossAllSixLandingVerbs is the
// test-engineer adversarial pass for focus area 3: --config alone (naming an
// external path) tested against each of the six SC15-sanctioned landing
// verbs from a primary checkout. None of the five non-merge verbs may have
// regressed to a false denial (the bug's own shape) or a false allow (a
// gate weakening); each must land exactly where it landed before this fix,
// since the fix touches only how --config is read, not how any of these
// verbs' own write/verb-allowed logic works.
func TestDecide_Bash_SC15_ConfigAloneAcrossAllSixLandingVerbs(t *testing.T) {
	v := testVerbs(t)
	correctDigest := hex.EncodeToString(sha256Sum(sc15BinContent))

	cases := []struct {
		name     string
		command  string
		wantDeny bool
	}{
		{name: "merge-config-alone-allowed", command: sc15Bin + " merge --config /outside/policy.yaml main"},
		{name: "push-config-alone-allowed", command: sc15Bin + " push --config /outside/policy.yaml"},
		{name: "resign-config-alone-allowed", command: sc15Bin + " resign --config /outside/policy.yaml --apply"},
		{name: "worktree-add-config-alone-allowed", command: sc15Bin + " worktree add --config /outside/policy.yaml /repo/wt2 main"},
		{name: "worktree-remove-config-alone-allowed", command: sc15Bin + " worktree remove --config /outside/policy.yaml /repo/wt2"},
		{name: "branch-delete-config-alone-allowed", command: sc15Bin + " branch delete --config /outside/policy.yaml stale"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := newFakeFS().dir("/repo/.git").dir("/outside").file(sc15Bin, sc15BinContent)
			d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{
				ToolName: "Bash", CWD: "/repo", Command: c.command,
				ProvisionedBinPath: sc15Bin, ProvisionedBinDigest: correctDigest,
			})
			if d.Deny != c.wantDeny {
				t.Errorf("Decide(cmd=%q) deny=%v, want %v (reason=%q)", c.command, d.Deny, c.wantDeny, d.Reason)
			}
		})
	}
}
