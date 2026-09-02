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

// TestSDET_SC15_NoProvisionedParams_DeniesEveryShape is an independent probe
// of the unresolved "attempt 2" mystery in
// SWE_REPORT_worktree-gate-out-of-workspace-repo.md: the report could not
// reproduce a denial for `worktree list --repo <dir>` (flag after verb, no
// cd) against the pre-fix code, since sc15ReadVerb's positional read matched
// that shape even before this fix. This test proves an entirely independent
// mechanism that WOULD deny every git-tools shape uniformly, unrelated to
// the leading-flag bug: sc15IdentityCause returns sc15IdentityNoParams
// whenever ProvisionedBinPath or ProvisionedBinDigest arrives empty (see
// decide.go sc15IdentityCause), which the wrapper
// (pretooluse-worktree-gate.sh) produces whenever the on-disk git-tools
// binary at the plugin-data root fails to match its pinned digest for the
// currently pinned tag -- its fallback is `exec "$gate_bin_path"` with
// NEITHER -provisioned-bin NOR -provisioned-digest. This denies not just
// attempt 2 but ALL FOUR reported shapes uniformly, matching the report's
// "denying every attempted shape" framing better than the leading-flag bug
// does (which only bit two of the four shapes).
func TestSDET_SC15_NoProvisionedParams_DeniesEveryShape(t *testing.T) {
	correctDigest := hex.EncodeToString(sha256Sum(sc15BinContent))
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
				t.Fatalf("Decide(cmd=%q) with empty provisioned params: got allow, want deny (sc15IdentityNoParams should void every allowance) -- correctDigest=%s unused here by design", c.command, correctDigest)
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
// The second case initially asserted deny, reasoning that a leading --repo
// should still void the allowance same as it does for a write verb. That
// assumption was wrong and the test itself was the bug: sc15ReadAllowed
// (unlike sc15Exempt, the write allowance) never calls sc15Retargets at
// all -- an existing pinned case
// (TestDecide_Bash_SC15ReadAllowance/repo-flag-before-verb-still-read-allowed)
// already establishes that `worktree list` stays read-allowed with a leading
// --repo, by design: reading a different repo is not a location-sensitive
// write, so only the write exemption's retarget veto applies. Corrected to
// wantDeny=false; this now positively confirms gitToolsOperands walks a
// value-consuming flag AND THEN a `--` correctly to find the verb.
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
