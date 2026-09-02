package detect

import (
	"encoding/hex"
	"strings"
	"testing"
)

// TestDecide_Bash_OutOfWorkspaceTargetRepo_ReportedShapes reproduces the four
// command shapes from a live report: a target repo outside the active
// workspace, carrying a submodule, hit against the digest-verified
// provisioned CLI. Exactly ONE of the four is the defect this file's fix
// addresses -- attempt 3, the leading --repo before the verb: sc15ReadVerb
// checked args[0]/args[1] positionally, so a leading flag hid the verb from
// it, leaving the read allowance unavailable and the call denied under SC20's
// named-path rule.
//
// The other three are pinned here as controls, because a report naming four
// denials against a bug that explains one is itself evidence about the cause:
//   - attempt 2 (--repo AFTER the verb) was ALREADY allowed pre-fix; the
//     positional read matched that spelling. Verified by running the
//     pre-existing suite against the pre-fix code, where only attempt 3
//     fails, and pinned independently by the pre-existing case
//     TestDecide_Bash_SC15ReadAllowance/repo-flag-does-not-void-read-allowed.
//     Do not describe this shape as fixed.
//   - attempts 1 and 4 (a bare `cd` plus `worktree add` with no --repo, and
//     plain `git worktree add`) are denied before and after: neither invokes
//     the CLI by its exact provisioned absolute path, so SC15 identity never
//     holds and the call falls to the ordinary primary-checkout write denial.
//     Correct by design, not this defect.
//
// So this fix cannot account for a report of all four being denied. The one
// mechanism that denies all four uniformly is a total identity failure, which
// TestSDET_SC15_NoProvisionedParams_DeniesEveryShape covers.
//
// The submodule is present in the fixture and is never on the path either
// shape's cwd or named operand resolves through, so its own Indeterminate
// classification (TestClassifyGitEntry_Submodule) plays no part in any of the
// four verdicts.
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
			name:     "attempt2-worktree-list-repo-flag-after-verb-allowed-before-and-after-fix",
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

// TestDecide_Bash_TargetRepoRemedy_NamesIdentityPrecondition pins that the
// named-path denial's remedy stays actionable in the one case that produced
// the live report: the CLI invoked by its exact provisioned absolute path,
// with the wrapper supplying no provisioned params because the binary on disk
// failed its pinned digest. That denial is reached inside SC20's named-path
// rule, ahead of the class tally where gitToolsSanctionCause would otherwise
// name the identity failure, so the remedy text is the caller's only signal.
// Without it the remedy hands back `worktree list --repo <dir>` -- the exact
// command just denied -- and the caller has no way to tell a classifier bug
// from an unverified local binary. Asserted on the remedy's substance, not its
// wording, alongside the deny() suffix contract it must not break.
func TestDecide_Bash_TargetRepoRemedy_NamesIdentityPrecondition(t *testing.T) {
	const bin = "/plugin-data/bin/git-tools"
	const content = "PROVISIONED-CLI-BYTES"
	v := testVerbs(t)

	fs := newFakeFS().
		dir("/workspace/.git").
		dir("/outside/target/.git").
		file(bin, content)

	// The wrapper's own fallback argv: it execs the gate with neither
	// -provisioned-bin nor -provisioned-digest once the CLI on disk stops
	// matching its pinned row, which is what makes this remedy load-bearing.
	d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{
		ToolName: "Bash",
		CWD:      "/workspace",
		Command:  bin + " worktree list --repo /outside/target",
	})
	if !d.Deny {
		t.Fatal("expected deny: with no provisioned params the read allowance is unavailable")
	}
	if !strings.Contains(d.Reason, "worktree list --repo <dir>") {
		t.Fatalf("remedy no longer names the sanctioned read spelling: %q", d.Reason)
	}
	if !strings.Contains(d.Remedy, "digest") {
		t.Fatalf("remedy %q recommends the very command that was denied without naming the identity precondition that voided it", d.Remedy)
	}
	if strings.Contains(d.Remedy, " -- ") {
		t.Fatalf("remedy %q contains deny()'s join token, breaking Reason's suffix contract", d.Remedy)
	}
	if !strings.HasSuffix(d.Reason, " -- "+d.Remedy) {
		t.Fatalf("Reason %q must close with the structured remedy %q", d.Reason, d.Remedy)
	}
}
