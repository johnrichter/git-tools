package detect

import (
	"encoding/hex"
	"testing"
)

// TestDecide_Bash_MergeConfigRetargetContradiction_ReportedShape reproduces
// the exact two-step live report: `git-tools merge --config <path outside
// the repo>` denied from the primary checkout and told to move into a
// worktree, then the natural retry -- `--repo <the primary checkout>` from
// that worktree, to point the merge back where merge is designed to run --
// denied again and told the opposite. Both denials were individually
// correct; together they closed every path, since nothing could satisfy
// "move into a worktree" and "move back to the primary checkout" at once.
//
// Root cause: sc15Retargets and gitToolsDestinations treated --config the
// same as --repo. --config only names a policy file to read (loadConfigFile);
// only --repo selects which repository merge acts on, and internal/cli's
// loadConfigForDir keeps it that way by assigning repoDirForConfig's answer
// over Config.Repo, so no config file's "repo" key can retarget the verb
// either. Step 1's --config value was never a retarget, so
// voiding the sanctioned-landing exemption for it was wrong; that wrong
// denial is what forced the retry that produced step 2's real, correct
// denial. Narrowing both functions to --repo alone leaves step 1 allowed --
// merge runs from the primary checkout as designed -- and step 2 still
// denied, since --repo genuinely retargets into a primary checkout, which
// SC20's named-path rule denies regardless of the caller's own cwd.
func TestDecide_Bash_MergeConfigRetargetContradiction_ReportedShape(t *testing.T) {
	const bin = "/plugin-data/bin/git-tools"
	const content = "PROVISIONED-CLI-BYTES"
	digest := hex.EncodeToString(sha256Sum(content))
	v := testVerbs(t)

	newFS := func() *fakeFS {
		return newFakeFS().
			dir("/repo/.git").
			file("/repo/wt/.git", "gitdir: /repo/.git/worktrees/wt\n").
			dir("/outside/policy").
			file(bin, content)
	}

	cases := []struct {
		name     string
		cwd      string
		command  string
		wantDeny bool
	}{
		{
			name:     "step1-merge-config-outside-path-from-primary-checkout-now-allowed",
			cwd:      "/repo",
			command:  bin + " merge --config /outside/policy/git-tools.yaml main",
			wantDeny: false,
		},
		{
			name:     "step2-retry-with-repo-retargeted-to-primary-from-worktree-still-denied",
			cwd:      "/repo/wt",
			command:  bin + " merge --repo /repo main",
			wantDeny: true,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := newFS()
			d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{
				ToolName:             "Bash",
				CWD:                  c.cwd,
				Command:              c.command,
				ProvisionedBinPath:   bin,
				ProvisionedBinDigest: digest,
			})
			if d.Deny != c.wantDeny {
				t.Errorf("%s: Decide deny=%v, want %v (reason=%q)", c.name, d.Deny, c.wantDeny, d.Reason)
			}
		})
	}
}
