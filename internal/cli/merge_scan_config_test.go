// Adversarial end-to-end proof that merge's content-guardrail gate resolves
// its own git-tools.yaml from the same prospective, trial-merged tree
// merge_scan_test.go already proves the gate scans for content — not from
// the target's pre-merge checkout. Every case here drives the built binary
// against a real, disposable git repository.
package cli_test

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// configScanFixedMarker and configScanFixedRuleID name the finding case 1's
// source branch resolves by editing the flagged file itself, leaving no
// trace of the finding in the prospective tree.
const (
	configScanFixedMarker  = "widget-marker-fixed-483"
	configScanFixedRuleID  = "fixture-widget-fixed-rule"
	configScanExemptMarker = "widget-marker-exempt-756"
	configScanExemptRuleID = "fixture-widget-exempt-rule"
)

// configScanAllowlistYAML renders a git-tools.yaml carrying exactly one
// secret_scan_extra_allowlist entry naming marker/ruleID, or no key at all
// (an empty marker) — the two config bodies these cases toggle between.
func configScanAllowlistYAML(marker, ruleID string) string {
	if marker == "" {
		return "repo: .\n"
	}
	return fmt.Sprintf("secret_scan_extra_allowlist:\n  - rule_id: %s\n    value: %s\n", ruleID, marker)
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
	tip := commitFile(t, dir, "git-tools.yaml", configScanAllowlistYAML(configScanExemptMarker, configScanExemptRuleID), "exempt widget-b's finding")
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
	tip := commitFile(t, dir, "git-tools.yaml", configScanAllowlistYAML(configScanExemptMarker, configScanExemptRuleID), "exempt the finding")
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
	if err := os.WriteFile(externalConfig, []byte(configScanAllowlistYAML("", "")), 0o644); err != nil {
		t.Fatal(err)
	}

	head := commitFile(t, dir, "widget.conf", "widget_value = "+configScanExemptMarker+"\n", "add widget with a finding")
	runGit(t, dir, "branch", "feature")
	runGit(t, dir, "checkout", "-q", "feature")
	commitFile(t, dir, "git-tools.yaml", configScanAllowlistYAML(configScanExemptMarker, configScanExemptRuleID), "exempt the finding (ignored: --config overrides this)")
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
	head := commitFile(t, dir, "git-tools.yaml", configScanAllowlistYAML(configScanExemptMarker, configScanExemptRuleID), "exempt the finding")
	runGit(t, dir, "branch", "feature")
	runGit(t, dir, "checkout", "-q", "feature")
	commitFile(t, dir, "git-tools.yaml", configScanAllowlistYAML("", ""), "narrow: drop the exemption")
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
