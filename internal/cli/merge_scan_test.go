// Adversarial end-to-end proof for prospectiveMergeScanDir (merge_scan.go):
// merge's content-guardrail gate must scan the tree a merge would actually
// produce, not the target's current, pre-merge tree, and it must do so with
// no leaked scratch worktree and no way for a doomed trial merge to let an
// unscanned result land. Every case here drives the built binary against a
// real, disposable git repository rather than asserting against the code
// that implements the behavior.
//
// A content-aware fixture betterleaks binary (writeContentAwareBetterleaksBinary)
// stands in for the real credentials/PII/financial scanner: it greps whatever
// files githooks hands it, in whatever directory it is invoked against, for
// one fixture marker literal, and reports a finding only when that literal is
// actually present. That makes it a genuine, content-dependent probe of which
// tree the gate looked at, not a canned answer this test bakes in.
package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// findingMarker is a fabricated, SSN-shaped PII literal this file's own
// fixture scanner treats as its one finding signature. Its only job is to be
// a literal these tests can plant, remove, or leave in place and then check
// for verbatim in a resulting tree, proving which tree was scanned. The
// leading group is one the Social Security Administration has never issued
// and never will, so this cannot be any real person's number — a shape-only
// claim about a plausible-looking group would not carry that guarantee. It is
// written in fragments so the file never carries the value contiguously.
const findingMarker = "666-95-" + "3287"

// findingMarkerRuleID is the fixture rule id the scanner below reports the
// finding under, given a "pii-" prefix so githooks.categoryForRuleID buckets
// it as "pii" — exercising the same categorized path
// TestScanGate_GoverningDiagnosticCategoryMatchesFindingCategory (scan_gate_test.go)
// already proves for a different fixture rule id.
const findingMarkerRuleID = "pii-fixture-merge-scan"

// writeContentAwareBetterleaksBinary writes an executable POSIX shell script
// standing in for the betterleaks binary githooks.ScanCredentials shells out
// to (see runBetterleaksBatch in go/githooks): it parses the fixed flag
// sequence that call always passes, then greps every trailing file argument
// — each relative to the script's own working directory, which githooks sets
// to the directory being scanned — for marker. A file scans clean unless it
// actually contains marker at the moment the scan runs, so this genuinely
// answers "does the scanned directory's real content carry the marker",
// rather than returning a fixed verdict regardless of what is asked.
//
// It exits 1 on every invocation, mirroring real betterleaks' own exit code
// when it has something to report; githooks.ScanCredentials looks at stdout's
// JSON, not the exit code.
func writeContentAwareBetterleaksBinary(t *testing.T, marker, ruleID string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fixture-content-betterleaks")
	script := fmt.Sprintf(`#!/bin/sh
shift
while [ "$#" -gt 0 ]; do
  case "$1" in
    --config|--gitleaks-ignore-path|--report-format|--report-path)
      shift 2 ;;
    --ignore-gitleaks-allow=true|--no-banner)
      shift ;;
    *)
      break ;;
  esac
done
entries=""
for f in "$@"; do
  if [ -f "$f" ] && grep -qF %q "$f" 2>/dev/null; then
    entry='{"RuleID":"%s","Description":"fixture PII finding","Secret":%q,"File":"'"$f"'"}'
    if [ -z "$entries" ]; then
      entries="$entry"
    else
      entries="$entries,$entry"
    fi
  fi
done
printf '[%%s]\n' "$entries"
exit 1
`, marker, ruleID, marker)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// setContentAwareBetterleaks points GIT_TOOLS_BETTERLEAKS_BIN at a fresh
// writeContentAwareBetterleaksBinary for the duration of the calling test —
// the one env var every case in this file needs, so the merge it drives
// exercises the real scanTree -> scanCredentials -> githooks.BuildHookResult
// chain scanGate uses in production, rather than a hand-rolled stand-in for
// scanGate's own result handling.
func setContentAwareBetterleaks(t *testing.T) {
	t.Helper()
	t.Setenv(betterleaksBinEnvVar, writeContentAwareBetterleaksBinary(t, findingMarker, findingMarkerRuleID))
}

// findingBearingConfig and cleanConfig are the two bodies this file's fixture
// file (widget.conf) alternates between: carrying the finding marker, or not.
func findingBearingConfig() string { return "widget_value = " + findingMarker + "\n" }
func cleanConfig() string          { return "widget_value = clean\n" }

// worktreePaths lists dir's own worktrees (its own primary checkout included)
// by parsing `git worktree list --porcelain`'s "worktree <path>" lines, in
// git's own reported order — the ground truth for whether
// prospectiveMergeScanDir's scratch worktree was actually cleaned up, read
// directly from git rather than inferred from the CLI's own exit code.
func worktreePaths(t *testing.T, dir string) []string {
	t.Helper()
	out := runGit(t, dir, "worktree", "list", "--porcelain")
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		if p, ok := strings.CutPrefix(line, "worktree "); ok {
			paths = append(paths, p)
		}
	}
	return paths
}

// assertNoLeakedWorktree fails unless dir's worktree list is byte-for-byte
// the same, in the same order, as it was in before — the evidence a scratch
// merge-scan worktree from prospectiveMergeScanDir was fully removed, under
// whatever outcome (scan pass, scan refusal, conflict-skip, or a worktree-
// preparation failure) the merge attempt in between produced.
func assertNoLeakedWorktree(t *testing.T, dir string, before []string) {
	t.Helper()
	after := worktreePaths(t, dir)
	if len(after) != len(before) {
		t.Fatalf("worktree list changed: before=%v after=%v", before, after)
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("worktree list changed: before=%v after=%v", before, after)
		}
	}
}

// Case 1 (core fix, positive): target's current tip already carries the
// finding; a source branch's own commit removes it and adds nothing else
// flagged. The merge must now land — the bug this fix answers is that it
// never could — and the resulting tree must actually be finding-free, not
// merely reported clean.
func TestProspectiveMergeScan_RemediatingSourceLandsDespiteTargetsExistingFinding(t *testing.T) {
	bin := buildCLI(t)
	dir := signingRepo(t)
	setContentAwareBetterleaks(t)

	commitFile(t, dir, "widget.conf", findingBearingConfig(), "add widget config with finding")
	runGit(t, dir, "branch", "feature")
	runGit(t, dir, "checkout", "-q", "feature")
	tip := commitFile(t, dir, "widget.conf", cleanConfig(), "remediate widget config")
	runGit(t, dir, "checkout", "-q", "main")

	before := worktreePaths(t, dir)

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0 (a remediating merge must land despite the target's pre-existing finding): %+v", r.Status, exit, r)
	}
	if got := runGit(t, dir, "rev-parse", "HEAD"); got != tip {
		t.Fatalf("main did not fast-forward to feature's remediating tip: got %s want %s", got, tip)
	}
	content, err := os.ReadFile(filepath.Join(dir, "widget.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(content), findingMarker) {
		t.Fatalf("the merged tree still carries the finding marker: %q", content)
	}
	assertNoLeakedWorktree(t, dir, before)
}

// Case 2 (no false clearance): target's current tip carries the finding; the
// source branch makes an unrelated change and does not remove it, so the
// merge's own prospective result still carries the finding. The merge must
// still refuse with the existing precondition_unmet content-guardrail
// diagnostic, proven by inspecting the actual refusal, not just the exit
// code.
func TestProspectiveMergeScan_NoFalseClearanceWhenSourceLeavesFindingInPlace(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	setContentAwareBetterleaks(t)

	head := commitFile(t, dir, "widget.conf", findingBearingConfig(), "add widget config with finding")
	runGit(t, dir, "branch", "feature")
	runGit(t, dir, "checkout", "-q", "feature")
	commitFile(t, dir, "unrelated.txt", "unrelated\n", "unrelated feature work")
	runGit(t, dir, "checkout", "-q", "main")

	before := worktreePaths(t, dir)

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature")
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("status=%s exit=%d, want precondition_unmet/30 (the merge's prospective result still carries the finding): %+v", r.Status, exit, r)
	}
	assertRefusalNamesFinding(t, r, "widget.conf")
	if got := runGit(t, dir, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD moved from %s to %s — merge landed despite the surviving finding", head, got)
	}
	if got := runGit(t, dir, "rev-parse", "main"); got != head {
		t.Fatalf("main advanced from %s to %s — merge landed despite the surviving finding", head, got)
	}
	assertNoLeakedWorktree(t, dir, before)
}

// Case 3 (no new-finding regression): target is clean; the source branch
// introduces a brand-new finding. The merge must still refuse the same way
// case 2 does — the fix must not have widened into "never scan a merge that
// touches a previously-clean target".
func TestProspectiveMergeScan_NewFindingInSourceStillRefuses(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	setContentAwareBetterleaks(t)
	head := runGit(t, dir, "rev-parse", "HEAD")

	runGit(t, dir, "branch", "feature")
	runGit(t, dir, "checkout", "-q", "feature")
	commitFile(t, dir, "widget.conf", findingBearingConfig(), "add widget config with finding")
	runGit(t, dir, "checkout", "-q", "main")

	before := worktreePaths(t, dir)

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature")
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("status=%s exit=%d, want precondition_unmet/30 (the merge introduces a brand-new finding): %+v", r.Status, exit, r)
	}
	assertRefusalNamesFinding(t, r, "widget.conf")
	if got := runGit(t, dir, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD moved from %s to %s — merge landed despite the new finding", head, got)
	}
	assertNoLeakedWorktree(t, dir, before)
}

// Case 4 (baseline unaffected): target clean, source clean — the merge lands
// exactly as it would have before this fix, with the scanner wired up but
// nothing for it to flag.
func TestProspectiveMergeScan_CleanTargetAndSourceMergesAsBefore(t *testing.T) {
	bin := buildCLI(t)
	dir := signingRepo(t)
	setContentAwareBetterleaks(t)

	runGit(t, dir, "branch", "feature")
	runGit(t, dir, "checkout", "-q", "feature")
	tip := commitFile(t, dir, "feature.txt", "feature\n", "feature work")
	runGit(t, dir, "checkout", "-q", "main")

	before := worktreePaths(t, dir)

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	if got := runGit(t, dir, "rev-parse", "HEAD"); got != tip {
		t.Fatalf("main did not fast-forward to feature tip: got %s want %s", got, tip)
	}
	assertNoLeakedWorktree(t, dir, before)
}

// Case 5 (the conflict-skip claim, adversarial): target and source conflict
// on the same line, and the source's conflicting commit also carries the
// finding marker — content that a scan of the tree it would produce, if one
// ever ran, would flag. The trial merge must fail on the conflict before any
// scan runs, and the command must fall through to the real merge's own,
// unaffected conflict handling: exit 41, the target ref and working tree
// exactly as they were before the attempt, and no scratch worktree left
// registered — nothing here should be able to make an unscanned result land.
func TestProspectiveMergeScan_ConflictingTrialMergeSkipsScanAndTouchesNothing(t *testing.T) {
	bin := buildCLI(t)
	dir := signingRepo(t)
	setContentAwareBetterleaks(t)

	runGit(t, dir, "checkout", "-q", "-b", "feature", "main")
	runGit(t, dir, "config", "commit.gpgsign", "false")
	commitFile(t, dir, "base.txt", "feature change "+findingMarker+"\n", "feature changes base and carries a finding")
	runGit(t, dir, "config", "commit.gpgsign", "true")
	runGit(t, dir, "checkout", "-q", "main")
	head := commitFile(t, dir, "base.txt", "main change\n", "main changes base")

	before := worktreePaths(t, dir)
	statusBefore := runGit(t, dir, "status", "--porcelain")

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature", "--message", "merge feature")
	if r.Status != "conflict" || exit != 41 {
		t.Fatalf("status=%s exit=%d, want conflict/41 (a genuinely conflicting trial merge must fall through to the real merge's own conflict handling, not report a scan verdict): %+v", r.Status, exit, r)
	}
	if got := runGit(t, dir, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD moved from %s to %s — a refused conflicting merge must not touch the target", head, got)
	}
	if got := runGit(t, dir, "rev-parse", "main"); got != head {
		t.Fatalf("main advanced from %s to %s — a refused conflicting merge must not touch the target", head, got)
	}
	if got := runGit(t, dir, "status", "--porcelain"); got != statusBefore {
		t.Fatalf("working tree changed across the refused merge: before=%q after=%q", statusBefore, got)
	}
	assertNoLeakedWorktree(t, dir, before)
}

// Case 6 (cleanup completeness under an injected worktree-preparation
// failure): pointing TMPDIR at a regular file makes os.MkdirTemp fail before
// any scratch worktree is even added, so prospectiveMergeScanDir must refuse
// loudly (internal/90) — not silently skip the scan and let the merge fall
// through unguarded — and must still leave no worktree registered, exactly
// like every other outcome case 1-5 already exercises inline via
// assertNoLeakedWorktree.
func TestProspectiveMergeScan_WorktreePreparationFailureRefusesAndLeaksNoWorktree(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	head := runGit(t, dir, "rev-parse", "HEAD")
	runGit(t, dir, "branch", "feature")
	runGit(t, dir, "checkout", "-q", "feature")
	commitFile(t, dir, "feature.txt", "feature\n", "feature work")
	runGit(t, dir, "checkout", "-q", "main")

	before := worktreePaths(t, dir)

	badTMPDir := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(badTMPDir, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", badTMPDir)

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature")
	if r.Status != "internal" || exit != 90 {
		t.Fatalf("status=%s exit=%d, want internal/90 (a scratch-worktree preparation failure must refuse loudly, not silently skip the scan): %+v", r.Status, exit, r)
	}
	if got := runGit(t, dir, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD moved from %s to %s — an internal failure preparing the scan must not touch the target", head, got)
	}
	assertNoLeakedWorktree(t, dir, before)
}

// Case 7 (--dry-run reflects the prospective result too): repeating case 1
// and case 3 with --dry-run must not regress to reporting a would-merge
// verdict computed off the wrong tree. Case 1's remediation must dry-run
// clean; case 3's new finding must still refuse via the content guardrail,
// never report a false would_merge.
func TestProspectiveMergeScan_DryRunReflectsProspectiveResult(t *testing.T) {
	t.Run("remediation_dry_runs_clean", func(t *testing.T) {
		bin := buildCLI(t)
		dir := signingRepo(t)
		setContentAwareBetterleaks(t)

		head := commitFile(t, dir, "widget.conf", findingBearingConfig(), "add widget config with finding")
		runGit(t, dir, "branch", "feature")
		runGit(t, dir, "checkout", "-q", "feature")
		commitFile(t, dir, "widget.conf", cleanConfig(), "remediate widget config")
		runGit(t, dir, "checkout", "-q", "main")

		before := worktreePaths(t, dir)

		r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature", "--dry-run")
		if r.Status != "success" || exit != 0 {
			t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
		}
		if r.Data["would_merge"] != true {
			t.Fatalf("dry run of a clean remediation did not report would_merge: %+v", r.Data)
		}
		if got := runGit(t, dir, "rev-parse", "HEAD"); got != head {
			t.Fatalf("a dry run moved HEAD: got %s want %s", got, head)
		}
		assertNoLeakedWorktree(t, dir, before)
	})

	t.Run("new_finding_still_refuses", func(t *testing.T) {
		bin := buildCLI(t)
		dir := initRepo(t)
		setContentAwareBetterleaks(t)
		head := runGit(t, dir, "rev-parse", "HEAD")

		runGit(t, dir, "branch", "feature")
		runGit(t, dir, "checkout", "-q", "feature")
		commitFile(t, dir, "widget.conf", findingBearingConfig(), "add widget config with finding")
		runGit(t, dir, "checkout", "-q", "main")

		before := worktreePaths(t, dir)

		r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature", "--dry-run")
		if r.Status != "precondition_unmet" || exit != 30 {
			t.Fatalf("status=%s exit=%d, want precondition_unmet/30 (a dry run must not report a false would_merge over a prospective result carrying a finding): %+v", r.Status, exit, r)
		}
		if r.Data["would_merge"] == true {
			t.Fatalf("dry run reported would_merge despite the prospective finding: %+v", r.Data)
		}
		assertRefusalNamesFinding(t, r, "widget.conf")
		if got := runGit(t, dir, "rev-parse", "HEAD"); got != head {
			t.Fatalf("a dry-run refusal still moved HEAD: got %s want %s", got, head)
		}
		assertNoLeakedWorktree(t, dir, before)
	})
}

// installApprovingCommitMsgHook gives dir a commit-msg hook that accepts only
// a message beginning "approved:" — the shape of a repository that enforces a
// message convention. Git composes its own default message for a merge commit
// no one supplied one for, so this hook rejects a trial merge that commits and
// accepts the operator's own --message: exactly the divergence case 8 exists
// to hold closed.
func installApprovingCommitMsgHook(t *testing.T, dir string) {
	t.Helper()
	hook := filepath.Join(dir, ".git", "hooks", "commit-msg")
	script := "#!/bin/sh\ngrep -q '^approved:' \"$1\" || { echo 'message not approved' >&2; exit 1; }\n"
	if err := os.WriteFile(hook, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

// Case 8 (the skip-on-failed-trial claim, adversarial): the claim that a
// failed trial merge is safe to skip the scan over rests on the trial failing
// only where the real merge cannot succeed either. A repository enforcing a
// commit-message convention breaks that if the trial commits anything: the
// trial's message is git's own default, which the hook rejects, while the
// operator's --message is one the hook already accepted — so the trial fails
// and the real merge lands. The merge must be refused on its content anyway.
// The clean sub-case holds the other half: disarming the trial must not
// disarm the hook's authority over the merge that really lands.
func TestProspectiveMergeScan_CommitMsgHookDoesNotLetAMergeLandUnscanned(t *testing.T) {
	t.Run("finding_still_refuses", func(t *testing.T) {
		bin := buildCLI(t)
		dir := signingRepo(t)
		setContentAwareBetterleaks(t)
		head := runGit(t, dir, "rev-parse", "HEAD")

		runGit(t, dir, "branch", "feature")
		runGit(t, dir, "checkout", "-q", "feature")
		commitFile(t, dir, "widget.conf", findingBearingConfig(), "add widget config with finding")
		runGit(t, dir, "checkout", "-q", "main")
		installApprovingCommitMsgHook(t, dir)

		before := worktreePaths(t, dir)

		r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature", "--fast-forward", "never", "--message", "approved: land feature")
		if r.Status != "precondition_unmet" || exit != 30 {
			t.Fatalf("status=%s exit=%d, want precondition_unmet/30 (a commit-msg hook the trial cannot satisfy must not let the merge land unscanned): %+v", r.Status, exit, r)
		}
		assertRefusalNamesFinding(t, r, "widget.conf")
		if got := runGit(t, dir, "rev-parse", "HEAD"); got != head {
			t.Fatalf("HEAD moved from %s to %s — the merge landed without its content ever being scanned", head, got)
		}
		assertNoLeakedWorktree(t, dir, before)
	})

	t.Run("clean_merge_still_lands_under_the_hook", func(t *testing.T) {
		bin := buildCLI(t)
		dir := signingRepo(t)
		setContentAwareBetterleaks(t)

		runGit(t, dir, "branch", "feature")
		runGit(t, dir, "checkout", "-q", "feature")
		commitFile(t, dir, "feature.txt", "feature\n", "feature work")
		runGit(t, dir, "checkout", "-q", "main")
		installApprovingCommitMsgHook(t, dir)

		before := worktreePaths(t, dir)

		r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature", "--fast-forward", "never", "--message", "approved: land feature")
		if r.Status != "success" || exit != 0 {
			t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
		}
		assertNoLeakedWorktree(t, dir, before)

		r, exit = runCLI(t, bin, "--repo", dir, "merge", "feature", "--fast-forward", "never", "--message", "unapproved message")
		if r.Status != "precondition_unmet" || exit != 30 {
			t.Fatalf("status=%s exit=%d, want precondition_unmet/30 (the hook still governs the message the real merge commits): %+v", r.Status, exit, r)
		}
	})
}

// Case 9 (octopus): two sources at once is the shape where a trial merge is
// most likely to diverge from the real one, since one source can be clean
// while another is not. Both halves must hold: the scan must see the tree the
// whole octopus would produce, and a partially-conflicting octopus must fail
// the trial and fall through to the real merge's own conflict handling rather
// than landing anything unscanned.
func TestProspectiveMergeScan_OctopusScansTheWholeProspectiveResult(t *testing.T) {
	t.Run("finding_in_one_source_refuses_the_whole_octopus", func(t *testing.T) {
		bin := buildCLI(t)
		dir := signingRepo(t)
		setContentAwareBetterleaks(t)
		head := runGit(t, dir, "rev-parse", "HEAD")

		runGit(t, dir, "checkout", "-q", "-b", "clean-source", "main")
		commitFile(t, dir, "clean.txt", "clean\n", "clean source work")
		runGit(t, dir, "checkout", "-q", "-b", "flagged-source", "main")
		commitFile(t, dir, "widget.conf", findingBearingConfig(), "add widget config with finding")
		runGit(t, dir, "checkout", "-q", "main")

		before := worktreePaths(t, dir)

		r, exit := runCLI(t, bin, "--repo", dir, "merge", "clean-source", "flagged-source")
		if r.Status != "precondition_unmet" || exit != 30 {
			t.Fatalf("status=%s exit=%d, want precondition_unmet/30 (one flagged source must refuse the whole octopus): %+v", r.Status, exit, r)
		}
		assertRefusalNamesFinding(t, r, "widget.conf")
		if got := runGit(t, dir, "rev-parse", "HEAD"); got != head {
			t.Fatalf("HEAD moved from %s to %s — the octopus landed despite the finding", head, got)
		}
		assertNoLeakedWorktree(t, dir, before)
	})

	t.Run("one_conflicting_source_falls_through_to_conflict_handling", func(t *testing.T) {
		bin := buildCLI(t)
		dir := signingRepo(t)
		setContentAwareBetterleaks(t)

		runGit(t, dir, "checkout", "-q", "-b", "clean-source", "main")
		commitFile(t, dir, "clean.txt", "clean\n", "clean source work")
		runGit(t, dir, "checkout", "-q", "-b", "conflicting-source", "main")
		commitFile(t, dir, "base.txt", "source change "+findingMarker+"\n", "conflicting source carries a finding")
		runGit(t, dir, "checkout", "-q", "main")
		head := commitFile(t, dir, "base.txt", "main change\n", "main changes base")

		before := worktreePaths(t, dir)

		r, exit := runCLI(t, bin, "--repo", dir, "merge", "clean-source", "conflicting-source")
		if exit == 0 {
			t.Fatalf("a partially-conflicting octopus must not land: status=%s exit=%d %+v", r.Status, exit, r)
		}
		if got := runGit(t, dir, "rev-parse", "HEAD"); got != head {
			t.Fatalf("HEAD moved from %s to %s — a refused octopus must not touch the target", head, got)
		}
		assertNoLeakedWorktree(t, dir, before)
	})
}
