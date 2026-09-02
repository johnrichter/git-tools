package detect

import (
	"encoding/hex"
	"testing"
)

// TestSDET_SC15_WriteExempt_WrongDigestWithLeadingFlag_Denied closes the
// task's explicit ask: the write allowance (sc15Exempt) shares sc15Identity
// with the read allowance, and a wrong-digest-plus-leading-flag case already
// exists for the read side
// (TestDecide_Bash_SC15ReadAllowance/wrong-digest-with-leading-flag-still-denies),
// but not, until now, one naming the write side explicitly. A planted
// wrong-digest binary must still be denied a write-class exemption (e.g.
// `worktree add`) even when a leading --repo/--config is present -- the new
// gitToolsOperands routing in sc15VerbAllowed must not bypass the identity
// check that runs before it in sc15Exempt.
func TestSDET_SC15_WriteExempt_WrongDigestWithLeadingFlag_Denied(t *testing.T) {
	cases := []struct {
		name    string
		command string
	}{
		{"worktree-add-leading-repo-spaced", sc15Bin + " --repo /other worktree add /repo/wt2 main"},
		{"merge-leading-config-glued", sc15Bin + " --config=/other merge main"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := newFakeFS().dir("/repo/.git").dir("/other/.git").file(sc15Bin, sc15BinContent)
			v := testVerbs(t)
			d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{
				ToolName: "Bash", CWD: "/repo", Command: c.command,
				ProvisionedBinPath:   sc15Bin,
				ProvisionedBinDigest: "0000000000000000000000000000000000000000000000000000000000000000", // wrong, on purpose
			})
			if !d.Deny {
				t.Fatalf("Decide(cmd=%q) with a wrong pinned digest: got allow, want deny -- a planted wrong-digest binary must still fail sc15Identity regardless of a leading flag", c.command)
			}
		})
	}
}

// TestSDET_SC15_NoProvisionedParams_DeniesEveryShape pins the one mechanism
// that denies EVERY git-tools shape uniformly, which the leading-flag bug does
// not: sc15IdentityCause returns sc15IdentityNoParams whenever
// ProvisionedBinPath or ProvisionedBinDigest arrives empty, voiding both the
// read allowance and the write exemption for any verb, however spelled. The
// PreToolUse wrapper produces exactly that argv -- the gate exec'd with
// neither -provisioned-bin nor -provisioned-digest -- whenever the git-tools
// binary at the plugin-data root is absent or fails to match its pinned digest
// for the currently pinned tag, while the wrapper still execs the gate itself.
//
// This is the shape to reach for when a report names more denied shapes than a
// classifier defect can explain: the leading-flag fix accounts for one of the
// four shapes in
// TestDecide_Bash_OutOfWorkspaceTargetRepo_ReportedShapes, and a
// stale or missing local CLI accounts for all four at once.
func TestSDET_SC15_NoProvisionedParams_DeniesEveryShape(t *testing.T) {
	cases := []struct {
		name    string
		command string
	}{
		{"attempt2-flag-after-verb-worktree-list", sc15Bin + " worktree list --repo /outside"},
		{"attempt3-flag-before-verb-worktree-list", sc15Bin + " --repo /outside worktree list"},
		{"worktree-add", sc15Bin + " worktree add /outside/wt main"},
		{"merge", sc15Bin + " merge main"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := newFakeFS().dir("/repo/.git").dir("/outside/.git").file(sc15Bin, sc15BinContent)
			v := testVerbs(t)
			// Same command, same binary, same digest on disk -- the ONLY
			// difference from a fully-provisioned call is that the wrapper
			// never supplied -provisioned-bin/-provisioned-digest, which is
			// exactly what pretooluse-worktree-gate.sh's fallback produces
			// when the on-disk CLI digest doesn't match its pinned row.
			d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{
				ToolName: "Bash", CWD: "/repo", Command: c.command,
				ProvisionedBinPath: "", ProvisionedBinDigest: "",
			})
			if !d.Deny {
				t.Fatalf("Decide(cmd=%q) with empty provisioned params: got allow, want deny (sc15IdentityNoParams must void every allowance, whatever the binary on disk hashes to)", c.command)
			}
		})
	}
}

// TestSDET_SC15_CommandSubstitutionInLeadingFlagValue_Denied is an
// adversarial probe of task item 1's fourth named gap: whether the widened
// leading-flag read-allowance has a bad interaction with SC16's interior-scan
// recursion when the smuggled substitution sits INSIDE the leading flag's own
// value token, not as a separate trailing operand (the shape the existing
// "command-substituted-list-at-depth-not-zero-denies" case already covers).
// scanBash's own comment says sc15Exempt/sc15ReadAllowed waive only the
// top-level piece's class tally, never the SC16 recursion into
// decomposableInteriors -- this proves that holds when the substitution is
// glued into --repo's value ahead of the verb, which is the new surface this
// fix's gitToolsOperands change made reachable (a leading flag was
// previously simply unrecognized and denied outright, so this shape never
// reached the read-allowance codepath pre-fix at all).
func TestSDET_SC15_CommandSubstitutionInLeadingFlagValue_Denied(t *testing.T) {
	correctDigest := hex.EncodeToString(sha256Sum(sc15BinContent))
	cases := []struct {
		name    string
		command string
	}{
		{"substitution-as-repo-flag-value-spaced", sc15Bin + ` --repo $(touch /repo/pwned) worktree list`},
		{"substitution-glued-into-repo-flag-value", sc15Bin + ` --repo=$(touch /repo/pwned) worktree list`},
		{"backtick-as-repo-flag-value", sc15Bin + " --repo `touch /repo/pwned` worktree list"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := newFakeFS().dir("/repo/.git").file(sc15Bin, sc15BinContent)
			v := testVerbs(t)
			d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{
				ToolName: "Bash", CWD: "/repo", Command: c.command,
				ProvisionedBinPath: sc15Bin, ProvisionedBinDigest: correctDigest,
			})
			if !d.Deny {
				t.Fatalf("Decide(cmd=%q): got allow, want deny -- a command substitution smuggled into --repo's own value must still face SC16's interior scan even though the outer piece is read-allowed", c.command)
			}
		})
	}
}

// TestSDET_SC15_OtherVerbs_LeadingFlag_GluedAndSpaced_AndStacked covers task
// item 1's first two named gaps against the implementer's own admitted scope
// limits: SC15 verbs other than worktree list/add (push, resign, branch
// delete, worktree remove) with a leading flag in both spaced and glued form,
// and multiple leading flags stacked before the verb.
func TestSDET_SC15_OtherVerbs_LeadingFlag_GluedAndSpaced_AndStacked(t *testing.T) {
	correctDigest := hex.EncodeToString(sha256Sum(sc15BinContent))
	cases := []struct {
		name     string
		command  string
		wantDeny bool
	}{
		// push/resign: single-token landing verbs. A leading --repo still
		// voids the allowance via sc15Retargets (which scans unconditionally
		// on position), so these must still deny -- but for the RIGHT reason
		// (retarget veto), not because the verb went undetected.
		{"push-leading-repo-spaced-denies-via-retarget", sc15Bin + " --repo /other push", true},
		{"push-leading-repo-glued-denies-via-retarget", sc15Bin + " --repo=/other push", true},
		{"resign-leading-repo-spaced-denies-via-retarget", sc15Bin + " --repo /other resign --apply", true},
		{"branch-delete-leading-repo-spaced-denies-via-retarget", sc15Bin + " --repo /other branch delete stale", true},
		{"worktree-remove-leading-repo-spaced-denies-via-retarget", sc15Bin + " --repo /other worktree remove /repo/wt", true},

		// Stacked leading flags with no retargeting flag among them
		// (--privacy-tier and --max-binary-bytes are root persistent flags
		// that do not retarget) ahead of a landing verb: must find the verb
		// and allow, proving gitToolsOperands' loop correctly walks past
		// MULTIPLE consumed flag+value pairs, not just one.
		{"stacked-nonretargeting-flags-before-merge-allowed", sc15Bin + " --privacy-tier confidential --max-binary-bytes 1024 merge main", false},
		{"stacked-nonretargeting-flags-glued-before-push-allowed", sc15Bin + " --privacy-tier=confidential --max-binary-bytes=1024 push", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := newFakeFS().dir("/repo/.git").dir("/other/.git").dir("/repo/wt/.git").file(sc15Bin, sc15BinContent)
			v := testVerbs(t)
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

// TestSDET_GitToolsOperands_DoubleDashStopsFlagSkip probes task item 1's
// third named gap: whether gitToolsOperands' "--" handling, or a gap in its
// value-consuming option table, could let a real verb be smuggled PAST the
// fixed checks -- i.e. cause sc15VerbAllowed/sc15ReadVerb to see a DIFFERENT,
// more permissive verb than the one the real CLI (cobra) would actually
// dispatch to, in the ALLOW direction. `--` ends flag parsing in both cobra
// and gitToolsOperands, so a verb placed after `--` must still be found.
//
// The read cases expect allow even with a leading --repo, because
// sc15ReadAllowed never calls sc15Retargets: reading a repo the caller is
// pointed at writes nothing, so only the write exemption carries a retarget
// veto. The write case in the same table shows that veto still firing, so the
// pair proves gitToolsOperands walks a value-consuming flag AND THEN a `--` to
// the verb without either allowance losing its own policy.
func TestSDET_GitToolsOperands_DoubleDashStopsFlagSkip(t *testing.T) {
	correctDigest := hex.EncodeToString(sha256Sum(sc15BinContent))
	cases := []struct {
		name     string
		command  string
		wantDeny bool
	}{
		{"double-dash-before-verb-still-found-allowed", sc15Bin + " -- worktree list", false},
		{"double-dash-after-a-leading-flag-read-verb-still-allowed", sc15Bin + " --repo /other -- worktree list", false},
		{"double-dash-after-leading-flag-write-verb-still-denied-via-retarget", sc15Bin + " --repo /other -- merge main", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := newFakeFS().dir("/repo/.git").dir("/other/.git").file(sc15Bin, sc15BinContent)
			v := testVerbs(t)
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
