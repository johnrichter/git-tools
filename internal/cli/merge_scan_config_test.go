// Adversarial end-to-end proof that merge's content-guardrail gate resolves
// its own git-tools.yaml from the same prospective, trial-merged tree
// merge_scan_test.go already proves the gate scans for content — not from
// the target's pre-merge checkout. Every case here drives the built binary
// against a real, disposable git repository.
package cli_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// configScanFixedMarker and configScanFixedRuleID name the finding case A's
// source branch resolves by editing the flagged file itself, leaving no
// trace of the finding in the prospective tree. The remaining pairs are
// findings a config entry either exempts or stops exempting.
const (
	configScanFixedMarker  = "widget-marker-fixed-483"
	configScanFixedRuleID  = "fixture-widget-fixed-rule"
	configScanExemptMarker = "widget-marker-exempt-756"
	configScanExemptRuleID = "fixture-widget-exempt-rule"
	configScanSecondMarker = "widget-marker-second-219"
	configScanSecondRuleID = "fixture-widget-second-rule"
)

// configScanAllowlistYAML renders a git-tools.yaml carrying one
// secret_scan_extra_allowlist entry per marker/ruleID pair, or no key at all
// when handed none — the config bodies these cases toggle between.
func configScanAllowlistYAML(pairs ...[2]string) string {
	if len(pairs) == 0 {
		return "repo: .\n"
	}
	body := "secret_scan_extra_allowlist:\n"
	for _, p := range pairs {
		body += fmt.Sprintf("  - rule_id: %s\n    value: %s\n", p[1], p[0])
	}
	return body
}

// exempt names one marker/ruleID pair for configScanAllowlistYAML, so a case
// reads as the exemption set it commits rather than as index arithmetic.
func exempt(marker, ruleID string) [2]string { return [2]string{marker, ruleID} }

// runCLICapturingStderr is runCLI plus the CLI's stderr, which runCLI
// discards. The gate's own diagnostics travel in the JSON record on stdout;
// stderr carries only out-of-band warnings, so a case asserting one is or is
// not emitted needs this rather than the decoded record.
func runCLICapturingStderr(t *testing.T, bin string, args ...string) (wireResult, int, string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	_ = cmd.Run()
	exit := cmd.ProcessState.ExitCode()
	var r wireResult
	if err := json.Unmarshal(stdout.Bytes(), &r); err != nil {
		t.Fatalf("output is not valid JSON: %v\nraw: %s", err, stdout.String())
	}
	return r, exit, stderr.String()
}

// writeConfigAwareBetterleaksBinary is writeContentAwareBetterleaksBinary
// (merge_scan_test.go) plus the one behavior these cases need that a purely
// content-aware fixture cannot exercise: it also reads the --config file
// githooks always passes and suppresses any finding whose marker literal
// appears in that file — the exact shape buildBetterleaksConfig's own
// appended [[allowlists]] block always takes for a secret_scan_extra_allowlist
// entry (see githooks.buildBetterleaksConfig). A finding is therefore
// reported or exempted by whatever config this fixture is actually handed at
// scan time, not by a verdict these tests bake in ahead of running it.
// git-tools.yaml itself is never a scan candidate here: its own exemption
// entries carry the marker literal in cleartext by construction, which would
// otherwise make the config file a spurious finding of its own — a fixture
// simplification these cases do not need to exercise.
func writeConfigAwareBetterleaksBinary(t *testing.T, markers ...[2]string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "fixture-config-aware-betterleaks")
	var checks string
	for _, m := range markers {
		marker, ruleID := m[0], m[1]
		checks += fmt.Sprintf(`  if grep -qF %q "$f" 2>/dev/null; then
    if [ -z "$config" ] || ! grep -qF %q "$config" 2>/dev/null; then
      entry='{"RuleID":"%s","Description":"fixture finding","Secret":%q,"File":"'"$f"'"}'
      if [ -z "$entries" ]; then entries="$entry"; else entries="$entries,$entry"; fi
    fi
  fi
`, marker, marker, ruleID, marker)
	}
	script := fmt.Sprintf(`#!/bin/sh
shift
config=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    --config)
      config="$2"; shift 2 ;;
    --gitleaks-ignore-path|--report-format|--report-path)
      shift 2 ;;
    --ignore-gitleaks-allow=true|--no-banner)
      shift ;;
    *)
      break ;;
  esac
done
entries=""
for f in "$@"; do
  case "$f" in
    git-tools.yaml) continue ;;
  esac
%s
done
printf '[%%s]\n' "$entries"
exit 1
`, checks)
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return bin
}

// Case A (core fix, combined): the target's tree carries two findings and no
// exemption for either. The source branch resolves one by editing the file
// itself, and separately adds a secret_scan_extra_allowlist entry for the
// other, unrelated finding it never touches. Both remedies land on the same
// prospective tree, so the merge must succeed.
func TestProspectiveMergeScan_SourceContentFixAndConfigExemptionBothTakeEffect(t *testing.T) {
	bin := buildCLI(t)
	dir := signingRepo(t)
	t.Setenv("GIT_TOOLS_BETTERLEAKS_BIN", writeConfigAwareBetterleaksBinary(t,
		[2]string{configScanFixedMarker, configScanFixedRuleID},
		[2]string{configScanExemptMarker, configScanExemptRuleID}))

	commitFile(t, dir, "widget-a.conf", "widget_value = "+configScanFixedMarker+"\n", "add widget-a with a finding")
	commitFile(t, dir, "widget-b.conf", "widget_value = "+configScanExemptMarker+"\n", "add widget-b with a different finding")
	runGit(t, dir, "branch", "feature")
	runGit(t, dir, "checkout", "-q", "feature")
	commitFile(t, dir, "widget-a.conf", "widget_value = clean\n", "remediate widget-a")
	tip := commitFile(t, dir, "git-tools.yaml", configScanAllowlistYAML(exempt(configScanExemptMarker, configScanExemptRuleID)), "exempt widget-b's finding")
	runGit(t, dir, "checkout", "-q", "main")

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0 (the content fix and the config exemption must both take effect on the same prospective tree): %+v", r.Status, exit, r)
	}
	if got := runGit(t, dir, "rev-parse", "HEAD"); got != tip {
		t.Fatalf("main did not fast-forward to feature tip: got %s want %s", got, tip)
	}
}

// Case B (config half, isolated): the target's tree is already clean of
// content findings except one its own current git-tools.yaml does not
// exempt. The source branch's only change is adding that exemption — no
// content change at all. Decoupled from case A so a regression in the
// content half cannot hide behind the config half's own fix.
func TestProspectiveMergeScan_ConfigOnlyExemptionTakesEffectWithNoContentChange(t *testing.T) {
	bin := buildCLI(t)
	dir := signingRepo(t)
	t.Setenv("GIT_TOOLS_BETTERLEAKS_BIN", writeConfigAwareBetterleaksBinary(t,
		[2]string{configScanExemptMarker, configScanExemptRuleID}))

	commitFile(t, dir, "widget.conf", "widget_value = "+configScanExemptMarker+"\n", "add widget with a finding")
	runGit(t, dir, "branch", "feature")
	runGit(t, dir, "checkout", "-q", "feature")
	tip := commitFile(t, dir, "git-tools.yaml", configScanAllowlistYAML(exempt(configScanExemptMarker, configScanExemptRuleID)), "exempt the finding")
	runGit(t, dir, "checkout", "-q", "main")

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0 (a source branch's own config-only exemption must take effect): %+v", r.Status, exit, r)
	}
	if got := runGit(t, dir, "rev-parse", "HEAD"); got != tip {
		t.Fatalf("main did not fast-forward to feature tip: got %s want %s", got, tip)
	}
}

// Case C (explicit --config is never re-resolved against the scratch tree):
// --config names a fixed file outside the repository, carrying no
// exemption. The source branch's own implicit-default git-tools.yaml (now
// ignored, since --config was given explicitly) carries an exemption that
// would otherwise have let the finding through. The merge must still refuse
// — proving the explicit flag's own file, not the scratch tree's implicit
// default, governs.
func TestProspectiveMergeScan_ExplicitConfigFlagIgnoresBranchsOwnImplicitDefault(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	t.Setenv("GIT_TOOLS_BETTERLEAKS_BIN", writeConfigAwareBetterleaksBinary(t,
		[2]string{configScanExemptMarker, configScanExemptRuleID}))

	externalConfig := filepath.Join(t.TempDir(), "external-git-tools.yaml")
	if err := os.WriteFile(externalConfig, []byte(configScanAllowlistYAML()), 0o644); err != nil {
		t.Fatal(err)
	}

	head := commitFile(t, dir, "widget.conf", "widget_value = "+configScanExemptMarker+"\n", "add widget with a finding")
	runGit(t, dir, "branch", "feature")
	runGit(t, dir, "checkout", "-q", "feature")
	commitFile(t, dir, "git-tools.yaml", configScanAllowlistYAML(exempt(configScanExemptMarker, configScanExemptRuleID)), "exempt the finding (ignored: --config overrides this)")
	runGit(t, dir, "checkout", "-q", "main")

	r, exit := runCLI(t, bin, "--repo", dir, "--config", externalConfig, "merge", "feature")
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("status=%s exit=%d, want precondition_unmet/30 (an explicit --config must govern, not the branch's own implicit-default git-tools.yaml): %+v", r.Status, exit, r)
	}
	assertRefusalNamesFinding(t, r, "widget.conf")
	if got := runGit(t, dir, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD moved from %s to %s — merge landed despite the explicit --config refusing", head, got)
	}
}

// Case D (narrowing an exemption is a stricter merge, and stricter governs):
// the target's current git-tools.yaml already carries the exemption; the
// source branch's own git-tools.yaml drops it — the branch's version has
// fewer entries than main's, not more — and touches nothing else. The
// resulting prospective config is the branch's own, narrower one, so the
// finding it no longer exempts must refuse the merge: honoring whichever
// config the prospective tree actually carries can only ever make the scan
// at least as strict as the looser side, never let a dropped exemption's
// finding slip through on the strength of the side that still had it.
func TestProspectiveMergeScan_SourceNarrowingConfigMakesTheMergeStricter(t *testing.T) {
	bin := buildCLI(t)
	dir := signingRepo(t)
	t.Setenv("GIT_TOOLS_BETTERLEAKS_BIN", writeConfigAwareBetterleaksBinary(t,
		[2]string{configScanExemptMarker, configScanExemptRuleID}))

	commitFile(t, dir, "widget.conf", "widget_value = "+configScanExemptMarker+"\n", "add widget with a finding")
	head := commitFile(t, dir, "git-tools.yaml", configScanAllowlistYAML(exempt(configScanExemptMarker, configScanExemptRuleID)), "exempt the finding")
	runGit(t, dir, "branch", "feature")
	runGit(t, dir, "checkout", "-q", "feature")
	commitFile(t, dir, "git-tools.yaml", configScanAllowlistYAML(), "narrow: drop the exemption")
	runGit(t, dir, "checkout", "-q", "main")

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature")
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("status=%s exit=%d, want precondition_unmet/30 (the branch's own narrower config must govern, refusing the finding it no longer exempts): %+v", r.Status, exit, r)
	}
	assertRefusalNamesFinding(t, r, "widget.conf")
	if got := runGit(t, dir, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD moved from %s to %s — merge landed despite the branch's narrower config refusing", head, got)
	}
	if got := runGit(t, dir, "rev-parse", "main"); got != head {
		t.Fatalf("main advanced from %s to %s — merge landed despite the branch's narrower config refusing", head, got)
	}
}

// Case E (one commit both widens and narrows): cases B and D each move the
// exemption set in one direction only, so either could pass against a config
// that is really main's unioned with the branch's. Here one commit drops the
// exemption for widget-a's finding and adds one for widget-b's, and the
// prospective tree still carries both findings — so each of the three
// candidate configs names a different outcome, and only the branch's own,
// governing exactly, refuses on widget-a: main's alone would refuse on
// widget-b, and a union of the two would let the merge land.
func TestProspectiveMergeScan_OneCommitWideningAndNarrowingIsReflectedBothWays(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	t.Setenv("GIT_TOOLS_BETTERLEAKS_BIN", writeConfigAwareBetterleaksBinary(t,
		[2]string{configScanExemptMarker, configScanExemptRuleID},
		[2]string{configScanSecondMarker, configScanSecondRuleID}))

	commitFile(t, dir, "widget-a.conf", "widget_value = "+configScanExemptMarker+"\n", "add widget-a with a finding")
	commitFile(t, dir, "widget-b.conf", "widget_value = "+configScanSecondMarker+"\n", "add widget-b with a different finding")
	head := commitFile(t, dir, "git-tools.yaml", configScanAllowlistYAML(exempt(configScanExemptMarker, configScanExemptRuleID)), "exempt widget-a's finding only")
	runGit(t, dir, "branch", "feature")
	runGit(t, dir, "checkout", "-q", "feature")
	commitFile(t, dir, "git-tools.yaml", configScanAllowlistYAML(exempt(configScanSecondMarker, configScanSecondRuleID)), "swap which finding is exempt")
	runGit(t, dir, "checkout", "-q", "main")

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature")
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("status=%s exit=%d, want precondition_unmet/30 (a union of both sides' exemptions would wrongly let this land): %+v", r.Status, exit, r)
	}
	assertRefusalNamesFinding(t, r, "widget-a.conf")
	if got := runGit(t, dir, "rev-parse", "main"); got != head {
		t.Fatalf("main advanced from %s to %s — merge landed despite the exemption the branch dropped", head, got)
	}
}

// Case F (octopus): with two sources, the exemption and the finding it covers
// can live in different sources, so neither source's own tree decides the
// answer on its own — only the combined trial-merged tree does. Both halves
// must hold: the exemption one source carries clears a finding another source
// brings, and a finding no source's config exempts still refuses.
func TestProspectiveMergeScan_OctopusJudgesTheCombinedTreesConfig(t *testing.T) {
	t.Run("exemption_in_one_source_clears_a_finding_from_another", func(t *testing.T) {
		bin := buildCLI(t)
		dir := signingRepo(t)
		t.Setenv("GIT_TOOLS_BETTERLEAKS_BIN", writeConfigAwareBetterleaksBinary(t,
			[2]string{configScanExemptMarker, configScanExemptRuleID}))

		runGit(t, dir, "checkout", "-q", "-b", "config-source", "main")
		commitFile(t, dir, "git-tools.yaml", configScanAllowlistYAML(exempt(configScanExemptMarker, configScanExemptRuleID)), "exempt the finding")
		runGit(t, dir, "checkout", "-q", "-b", "content-source", "main")
		commitFile(t, dir, "widget.conf", "widget_value = "+configScanExemptMarker+"\n", "add widget with a finding")
		runGit(t, dir, "checkout", "-q", "main")

		r, exit := runCLI(t, bin, "--repo", dir, "merge", "config-source", "content-source")
		if r.Status != "success" || exit != 0 {
			t.Fatalf("status=%s exit=%d, want success/0 (the octopus's own combined tree carries both the finding and its exemption): %+v", r.Status, exit, r)
		}
	})

	t.Run("finding_no_sources_config_exempts_still_refuses", func(t *testing.T) {
		bin := buildCLI(t)
		dir := signingRepo(t)
		t.Setenv("GIT_TOOLS_BETTERLEAKS_BIN", writeConfigAwareBetterleaksBinary(t,
			[2]string{configScanExemptMarker, configScanExemptRuleID},
			[2]string{configScanSecondMarker, configScanSecondRuleID}))
		head := runGit(t, dir, "rev-parse", "HEAD")

		runGit(t, dir, "checkout", "-q", "-b", "config-source", "main")
		commitFile(t, dir, "git-tools.yaml", configScanAllowlistYAML(exempt(configScanExemptMarker, configScanExemptRuleID)), "exempt one finding only")
		runGit(t, dir, "checkout", "-q", "-b", "content-source", "main")
		commitFile(t, dir, "widget.conf", "widget_value = "+configScanSecondMarker+"\n", "add widget with an unexempted finding")
		runGit(t, dir, "checkout", "-q", "main")

		r, exit := runCLI(t, bin, "--repo", dir, "merge", "config-source", "content-source")
		if r.Status != "precondition_unmet" || exit != 30 {
			t.Fatalf("status=%s exit=%d, want precondition_unmet/30 (no side's config exempts this finding): %+v", r.Status, exit, r)
		}
		assertRefusalNamesFinding(t, r, "widget.conf")
		if got := runGit(t, dir, "rev-parse", "main"); got != head {
			t.Fatalf("main advanced from %s to %s — the octopus landed despite the finding", head, got)
		}
	})
}

// Case G (the prospective load is silent about the scratch tree): the scratch
// worktree's git-tools.yaml differs from that worktree's own HEAD whenever the
// merge changes it, which is the arrangement under test rather than tampering.
// warnIfConfigTampered must therefore stay quiet for that load — otherwise
// every merge touching the file would warn about a temporary path the operator
// cannot inspect, training a real security warning into noise.
func TestProspectiveMergeScan_ProspectiveConfigLoadEmitsNoTamperWarning(t *testing.T) {
	bin := buildCLI(t)
	dir := signingRepo(t)
	t.Setenv("GIT_TOOLS_BETTERLEAKS_BIN", writeConfigAwareBetterleaksBinary(t,
		[2]string{configScanExemptMarker, configScanExemptRuleID}))

	commitFile(t, dir, "widget.conf", "widget_value = "+configScanExemptMarker+"\n", "add widget with a finding")
	runGit(t, dir, "branch", "feature")
	runGit(t, dir, "checkout", "-q", "feature")
	commitFile(t, dir, "git-tools.yaml", configScanAllowlistYAML(exempt(configScanExemptMarker, configScanExemptRuleID)), "exempt the finding")
	runGit(t, dir, "checkout", "-q", "main")

	r, exit, stderr := runCLICapturingStderr(t, bin, "--repo", dir, "merge", "feature")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	if strings.Contains(stderr, "git-tools.yaml is") {
		t.Fatalf("the prospective config load warned about the scratch tree's own git-tools.yaml: %q", stderr)
	}
}

// Case H (an unloadable prospective config is the operator's precondition):
// a source branch whose git-tools.yaml does not parse leaves the gate with no
// config it can honor, so the merge must refuse — but as a precondition
// naming git-tools.yaml, not as an internal fault advising the operator to
// file an issue about a scratch path this run has already deleted.
func TestProspectiveMergeScan_UnloadableProspectiveConfigRefusesAsAPrecondition(t *testing.T) {
	bin := buildCLI(t)
	dir := signingRepo(t)
	t.Setenv("GIT_TOOLS_BETTERLEAKS_BIN", writeConfigAwareBetterleaksBinary(t,
		[2]string{configScanExemptMarker, configScanExemptRuleID}))

	head := commitFile(t, dir, "widget.conf", "widget_value = clean\n", "add a clean widget")
	runGit(t, dir, "branch", "feature")
	runGit(t, dir, "checkout", "-q", "feature")
	commitFile(t, dir, "git-tools.yaml", "secret_scan_extra_allowlist: [unclosed\n", "break the config")
	runGit(t, dir, "checkout", "-q", "main")

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature")
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("status=%s exit=%d, want precondition_unmet/30 (a branch's own unparseable config is a precondition, not an internal fault): %+v", r.Status, exit, r)
	}
	if len(r.Errors) == 0 {
		t.Fatal("refusal carries no error")
	}
	message, _ := r.Errors[0]["message"].(string)
	if !strings.Contains(message, "git-tools.yaml") {
		t.Fatalf("refusal does not name the file to fix: %+v", r.Errors[0])
	}
	if strings.Contains(message, "git-tools-merge-scan-") {
		t.Fatalf("refusal names the deleted scratch worktree path: %+v", r.Errors[0])
	}
	if got := runGit(t, dir, "rev-parse", "main"); got != head {
		t.Fatalf("main advanced from %s to %s — merge landed with a config the gate could not load", head, got)
	}
}
