package detect

import (
	"encoding/hex"
	"testing"
)

// TestDecide_Bash_OutOfWorkspaceTargetRepo_ReportedShapes reproduces the four
// command shapes from a live report: a target repo outside the active
// workspace, carrying a submodule, hit against the digest-verified
// provisioned CLI. Two shapes (worktree list --repo, flag before and after
// the verb) are the confirmed defect this file's fix addresses: sc15ReadVerb
// checked args[0]/args[1] positionally, so a leading --repo hid the verb from
// it, leaving the read allowance unavailable and the call denied under
// SC20's named-path rule -- whose own remedy text names the very shape that
// was failing. The other two (a bare `cd` into the target plus `worktree
// add` with no --repo, and plain `git worktree add`) are denied both before
// and after this fix: neither invokes the CLI by its exact provisioned
// absolute path, so SC15 identity never holds and the call falls to the
// ordinary primary-checkout write denial -- correct, by-design behavior, not
// this defect. The submodule is present in the fixture and is never on the
// path either shape's cwd or named operand resolves through, so its own
// Indeterminate classification (TestClassifyGitEntry_Submodule) plays no
// part in any of the four verdicts.
func TestDecide_Bash_OutOfWorkspaceTargetRepo_ReportedShapes(t *testing.T) {
	const bin = "/plugin-data/bin/git-tools"
	const content = "PROVISIONED-CLI-BYTES"
	digest := hex.EncodeToString(sha256Sum(content))
	v := testVerbs(t)

	newFS := func() *fakeFS {
		return newFakeFS().
			dir("/workspace/.git").
			dir("/outside/target/.git").
			dir("/outside/target/.git/modules/lib").
			file("/outside/target/vendor/lib/.git", "gitdir: ../.git/modules/lib\n").
			file(bin, content)
	}

	cases := []struct {
		name     string
		cwd      string
		command  string
		wantDeny bool
	}{
		{
			name:     "attempt1-cd-into-target-worktree-add-no-repo-flag-denied-by-design",
			cwd:      "/outside/target",
			command:  "cd /outside/target && git-tools worktree add ../review origin/main",
			wantDeny: true,
		},
		{
			name:     "attempt2-worktree-list-repo-flag-after-verb-now-allowed",
			cwd:      "/workspace",
			command:  bin + " worktree list --repo /outside/target",
			wantDeny: false,
		},
		{
			name:     "attempt3-worktree-list-repo-flag-before-verb-now-allowed",
			cwd:      "/workspace",
			command:  bin + " --repo /outside/target worktree list",
			wantDeny: false,
		},
		{
			name:     "attempt4-plain-git-worktree-add-denied-by-design",
			cwd:      "/outside/target",
			command:  "git worktree add ../review origin/main",
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
