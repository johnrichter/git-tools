package detect

import (
	"encoding/hex"
	"strings"
	"testing"
)

// TestClassifyPiece_GitToolsAlwaysWritesByDefault pins the classifier-bypass
// fix: any command whose first word names the provisioned CLI, whatever verb
// follows, classifies write. verbs.json carries no git-tools pattern at all,
// so before this default every one of these fell to ClassUncertain. No lookup
// table separates a read set from a write set here; the default is
// unconditional.
func TestClassifyPiece_GitToolsAlwaysWritesByDefault(t *testing.T) {
	v := testVerbs(t)
	commands := []string{
		"git-tools merge main",               // on the eleven-verb list
		"/plugin-data/bin/git-tools push",    // full provisioned path, also on the list
		"git-tools scan secrets",             // not on the eleven-verb list
		"git-tools some-future-subcommand",   // unrecognized verb entirely
		"./git-tools worktree add ../wt ref", // relative spelling
	}
	for _, cmd := range commands {
		if got := ClassifyBash(v, cmd); got != ClassWrite {
			t.Errorf("ClassifyBash(%q) = %v, want ClassWrite", cmd, got)
		}
	}
}

// TestClassifyPiece_GitToolsNeverReadsByNameAlone confirms a git-tools-shaped
// command can never reach ClassRead through classifyPiece itself -- the only
// route to ClassRead for this CLI is sc15ReadAllowed's independent,
// digest-verified check in scanBash, exercised separately below.
func TestClassifyPiece_GitToolsNeverReadsByNameAlone(t *testing.T) {
	v := testVerbs(t)
	commands := []string{
		"git-tools worktree list",
		"git-tools branch list",
		"GIT-TOOLS worktree list",
	}
	for _, cmd := range commands {
		if got := ClassifyBash(v, cmd); got == ClassRead {
			t.Errorf("ClassifyBash(%q) = ClassRead; a git-tools command must never read by name alone", cmd)
		}
	}
}

// TestDecide_Bash_SC15ReadAllowance_SurvivesGitToolsDefault re-runs the
// existing digest-verified read allowance now that classifyPiece's
// git-tools default returns ClassWrite instead of ClassUncertain: scanBash's
// gate for applying sc15ReadAllowed must still fire on that Write, or a
// legitimate `worktree list`/`branch list` call regresses to denied.
func TestDecide_Bash_SC15ReadAllowance_SurvivesGitToolsDefault(t *testing.T) {
	const bin = "/plugin-data/bin/git-tools"
	const content = "PROVISIONED-CLI-BYTES"
	digest := hex.EncodeToString(sha256Sum(content))
	v := testVerbs(t)

	for _, cmd := range []string{bin + " worktree list", bin + " branch list"} {
		fs := newFakeFS().dir("/repo/.git").file(bin, content)
		d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{
			ToolName: "Bash", CWD: "/repo", Command: cmd,
			ProvisionedBinPath: bin, ProvisionedBinDigest: digest,
		})
		if d.Deny {
			t.Errorf("Decide(cmd=%q) from a primary checkout with a verified digest must be ALLOWED, got deny: %s", cmd, d.Reason)
		}
	}
}

// TestDecide_Bash_GitToolsBypass_MergeRepoRetargetFromWorktree_Denied is the
// live bypass this fix closes: `git-tools merge <branch> --repo <other>` run
// from inside an already-sanctioned worktree used to fall to ClassUncertain
// (the CLI's own name was unrecognized), which never reaches SC20's
// named-path check and so landed the merge straight into the other
// repository's primary checkout. It must now deny -- and so must the same
// retarget spelled `-C` or `--worktree`, since each reaches the same
// destination through gitToolsDestinations.
func TestDecide_Bash_GitToolsBypass_MergeRepoRetargetFromWorktree_Denied(t *testing.T) {
	const bin = "/plugin-data/bin/git-tools"
	const content = "PROVISIONED-CLI-BYTES"
	digest := hex.EncodeToString(sha256Sum(content))
	v := testVerbs(t)

	spellings := []string{"--repo /other", "-C /other", "--worktree /other"}
	for _, flag := range spellings {
		t.Run(flag, func(t *testing.T) {
			fs := newFakeFS().
				file("/repo/wt/.git", "gitdir: /repo/.git/worktrees/wt\n").
				dir("/other/.git").
				file(bin, content)
			d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{
				ToolName: "Bash", CWD: "/repo/wt", Command: bin + " merge feature " + flag,
				ProvisionedBinPath: bin, ProvisionedBinDigest: digest,
			})
			if !d.Deny {
				t.Fatal("expected deny: a retargeted git-tools merge must not land in another repository's primary checkout, whatever cwd it runs from")
			}
			if !strings.Contains(d.Reason, "/other") {
				t.Errorf("denial %q does not name the retargeted primary checkout", d.Reason)
			}
		})
	}
}

// TestDecide_Bash_GitToolsDestinations_FlagOrderCannotHideATarget pins the
// other half of the same bypass: SC20 only sees what gitToolsDestinations
// enumerates, so a flag placed ahead of the verb word or between the verb and
// its path must not shift that path out of view. Every spelling below names a
// path in ANOTHER repository's primary checkout and is run from a sanctioned
// worktree -- the cwd leg allows it, leaving the named-path rule as the only
// thing standing between the call and that checkout.
func TestDecide_Bash_GitToolsDestinations_FlagOrderCannotHideATarget(t *testing.T) {
	const bin = "/plugin-data/bin/git-tools"
	const content = "PROVISIONED-CLI-BYTES"
	digest := hex.EncodeToString(sha256Sum(content))
	v := testVerbs(t)

	commands := []string{
		"git-tools worktree add /other/x ref",                    // no flag: the baseline
		"git-tools --strict worktree add /other/x ref",           // root bool flag before the verb
		"git-tools --config /tmp/c.yaml worktree add /other/x r", // root value flag before the verb
		"git-tools --config=/tmp/c.yaml worktree add /other/x r", // its =-joined spelling
		"git-tools -C /repo/wt worktree add /other/x ref",        // -C shorthand (separate token) before the verb: its own value is a benign worktree, so only the operand-skip can catch the target
		"git-tools -C/repo/wt worktree add /other/x ref",         // its glued spelling
		"git-tools worktree add --branch b /other/x ref",         // the verb's own value flag
		"git-tools worktree add --force /other/x ref",            // the verb's own bool flag
		"git-tools worktree remove /other/x",
		"git-tools --strict worktree remove /other/x",
		"git-tools worktree remove --landing-target main /other/x",
	}
	for _, cmd := range commands {
		fs := newFakeFS().
			file("/repo/wt/.git", "gitdir: /repo/.git/worktrees/wt\n").
			dir("/other/.git").
			file(bin, content)
		d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{
			ToolName: "Bash", CWD: "/repo/wt", Command: cmd,
			ProvisionedBinPath: bin, ProvisionedBinDigest: digest,
		})
		if !d.Deny {
			t.Errorf("Decide(cmd=%q) from a worktree was ALLOWED; it names %q in another repository's primary checkout", cmd, "/other/x")
			continue
		}
		if !strings.Contains(d.Reason, "/other/x") {
			t.Errorf("denial of %q does not name the target /other/x: %s", cmd, d.Reason)
		}
	}
}

// TestDecide_Bash_GitToolsSanctionCause_NamesSpecificFailure pins Fix 2: a
// git-tools-shaped write denied from a primary checkout because it fails
// SC15's identity check names the specific cause, and the recognized verb
// when that verb is on the eleven-verb list, rather than the generic "may
// modify" wording every other denial in this family used to share.
func TestDecide_Bash_GitToolsSanctionCause_NamesSpecificFailure(t *testing.T) {
	const bin = "/plugin-data/bin/git-tools"
	const content = "PROVISIONED-CLI-BYTES"
	correctDigest := hex.EncodeToString(sha256Sum(content))
	v := testVerbs(t)

	cases := []struct {
		name        string
		command     string
		argPath     string // "" => bin (correct)
		argDigest   string // "" => correctDigest
		wantPhrases []string
	}{
		{
			name:        "digest-mismatch-names-cause-and-eleven-verb-list-match",
			command:     bin + " merge main",
			argDigest:   "0000000000000000000000000000000000000000000000000000000000000000",
			wantPhrases: []string{"`merge`", "pinned digest"},
		},
		{
			name:        "bare-word-names-cause-without-a-recognized-verb-arg",
			command:     "git-tools merge main",
			wantPhrases: []string{"cannot be sanctioned here", "provisioned absolute path"},
		},
		{
			name:        "unrecognized-verb-names-cause-without-a-verb",
			command:     bin + " scan secrets",
			argDigest:   "0000000000000000000000000000000000000000000000000000000000000000",
			wantPhrases: []string{"cannot be sanctioned here", "pinned digest"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := newFakeFS().dir("/repo/.git").file(bin, content)
			path, digest := bin, correctDigest
			if c.argPath != "" {
				path = c.argPath
			}
			if c.argDigest != "" {
				digest = c.argDigest
			}
			d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{
				ToolName: "Bash", CWD: "/repo", Command: c.command,
				ProvisionedBinPath: path, ProvisionedBinDigest: digest,
			})
			if !d.Deny {
				t.Fatalf("expected deny for %q from a primary checkout", c.command)
			}
			if strings.Contains(d.Reason, "may modify") {
				t.Errorf("denial %q still uses the generic message; expected a cause-specific one", d.Reason)
			}
			for _, want := range c.wantPhrases {
				if !strings.Contains(d.Reason, want) {
					t.Errorf("denial %q does not contain %q", d.Reason, want)
				}
			}
		})
	}
}
