// End-to-end proof for the mandatory credential scan (scan.go's
// errBetterleaksUnconfigured) and secret_scan_categorized_severity's
// warn/block posture: every case here drives the built binary against a
// real, disposable git repository, the same way scan_gate_test.go and
// merge_scan_test.go do.
package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMandatoryCredentialScan_UnconfiguredRefusesEveryEntryPoint proves the
// env-var opt-out is gone: with GIT_TOOLS_BETTERLEAKS_BIN unset, scan
// secrets, scan all, and merge each refuse with a precondition_unmet naming
// the credential scanner as unconfigured -- none silently proceeds as if
// nothing were wrong.
func TestMandatoryCredentialScan_UnconfiguredRefusesEveryEntryPoint(t *testing.T) {
	bin := buildCLI(t)

	t.Run("scan_secrets", func(t *testing.T) {
		dir := initRepo(t)
		t.Setenv(betterleaksBinEnvVar, "")
		r, exit := runCLIIn(t, bin, dir, "scan", "secrets")
		assertCredentialScannerUnconfigured(t, r, exit)
	})

	t.Run("scan_all", func(t *testing.T) {
		dir := initRepo(t)
		t.Setenv(betterleaksBinEnvVar, "")
		r, exit := runCLIIn(t, bin, dir, "scan", "all")
		assertCredentialScannerUnconfigured(t, r, exit)
	})

	t.Run("merge", func(t *testing.T) {
		dir := initRepo(t)
		t.Setenv(betterleaksBinEnvVar, "")
		runGit(t, dir, "branch", "feature")
		runGit(t, dir, "checkout", "-q", "feature")
		commitFile(t, dir, "feature.txt", "feature\n", "feature work")
		runGit(t, dir, "checkout", "-q", "main")

		r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature")
		assertCredentialScannerUnconfigured(t, r, exit)
		if got := runGit(t, dir, "rev-parse", "HEAD"); got != runGit(t, dir, "rev-parse", "feature^") {
			t.Fatalf("HEAD moved despite the refusal: %s", got)
		}
	})
}

// TestMandatoryCredentialScan_NonexistentPathRefusesTheSameWay proves a
// configured-but-nonexistent binary path fails identically to an unset one,
// not as an internal error.
func TestMandatoryCredentialScan_NonexistentPathRefusesTheSameWay(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	t.Setenv(betterleaksBinEnvVar, "/nonexistent/path/to/betterleaks")

	r, exit := runCLIIn(t, bin, dir, "scan", "secrets")
	assertCredentialScannerUnconfigured(t, r, exit)
}

// assertCredentialScannerUnconfigured checks r/exit against the refusal
// scan.go's credentialScannerUnconfiguredDiagnostic builds: precondition_unmet
// (exit 30), naming the credential scanner as the real cause and stating
// nothing downstream ran, never a vague internal error.
func assertCredentialScannerUnconfigured(t *testing.T, r wireResult, exit int) {
	t.Helper()
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("status=%s exit=%d, want precondition_unmet/30: %+v", r.Status, exit, r)
	}
	if len(r.Errors) == 0 {
		t.Fatal("precondition_unmet result carries no errors")
	}
	message, _ := r.Errors[0]["message"].(string)
	if !strings.Contains(message, "credential scanner is not configured") {
		t.Fatalf("message = %q, does not plainly name the credential scanner as unconfigured", message)
	}
}

// TestMandatoryCredentialScan_ConfiguredCleanAddsNoFriction proves Part 1
// adds no new friction once a working betterleaks binary is actually
// available: with a clean stub configured and nothing to find, scan
// secrets, scan all, and merge each succeed exactly as before.
func TestMandatoryCredentialScan_ConfiguredCleanAddsNoFriction(t *testing.T) {
	bin := buildCLI(t)
	// betterleaksBinEnvVar already points at TestMain's own clean stub; each
	// subtest below only needs to avoid overriding it.

	t.Run("scan_secrets", func(t *testing.T) {
		dir := initRepo(t)
		r, exit := runCLIIn(t, bin, dir, "scan", "secrets")
		if r.Status != "success" || exit != 0 {
			t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
		}
	})

	t.Run("scan_all", func(t *testing.T) {
		dir := initRepo(t)
		r, exit := runCLIIn(t, bin, dir, "scan", "all")
		if r.Status != "success" || exit != 0 {
			t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
		}
	})

	t.Run("merge", func(t *testing.T) {
		dir := signingRepo(t)
		runGit(t, dir, "branch", "feature")
		runGit(t, dir, "checkout", "-q", "feature")
		tip := commitFile(t, dir, "feature.txt", "feature\n", "feature work")
		runGit(t, dir, "checkout", "-q", "main")

		r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature")
		if r.Status != "success" || exit != 0 {
			t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
		}
		if got := runGit(t, dir, "rev-parse", "HEAD"); got != tip {
			t.Fatalf("main did not fast-forward to feature tip: got %s want %s", got, tip)
		}
	})
}

// categorizedFindingRepo builds a signed-commit repo whose target already
// carries fixtureBetterleaksValue in widget.conf, plus a feature branch ready
// to merge, and points betterleaksBinEnvVar at a stub reporting that value as
// a "credentials"-categorized finding -- the shared setup every warn/block
// posture case below needs, differing only in git-tools.yaml.
func categorizedFindingRepo(t *testing.T) string {
	t.Helper()
	dir := signingRepo(t)
	t.Setenv(betterleaksBinEnvVar, writeFixtureBetterleaksBinary(t,
		fixtureBetterleaksReport(fixtureBetterleaksRuleID, fixtureBetterleaksValue, "widget.conf")))
	commitFile(t, dir, "widget.conf", "widget_value = "+fixtureBetterleaksValue+"\n", "add widget config")
	runGit(t, dir, "branch", "feature")
	runGit(t, dir, "checkout", "-q", "feature")
	commitFile(t, dir, "feature.txt", "feature\n", "feature work")
	runGit(t, dir, "checkout", "-q", "main")
	return dir
}

// TestSecretScanCategorizedSeverity_DefaultWarnLandsMergeAsCaveat proves the
// warn-only default: with no secret_scan_categorized_severity key at all, a
// categorized finding lands the merge as a caveat (exit 10), not a hard
// block (exit 30), and the caveat names the finding.
func TestSecretScanCategorizedSeverity_DefaultWarnLandsMergeAsCaveat(t *testing.T) {
	bin := buildCLI(t)
	dir := categorizedFindingRepo(t)
	head := runGit(t, dir, "rev-parse", "HEAD")

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature")
	if r.Status != "caveats" || exit != 10 {
		t.Fatalf("status=%s exit=%d, want caveats/10 (a categorized finding must warn, not block, with no severity configured): %+v", r.Status, exit, r)
	}
	if len(r.Caveats) == 0 {
		t.Fatal("caveats result carries no caveats for the planted finding")
	}
	context, _ := r.Caveats[0]["context"].(map[string]any)
	if path, _ := context["path"].(string); path != "widget.conf" {
		t.Fatalf("caveat does not name the offending path: %+v", r.Caveats[0])
	}
	if got := runGit(t, dir, "rev-parse", "HEAD"); got == head {
		t.Fatalf("HEAD stayed at %s — merge did not land despite the warn-only posture", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "feature.txt")); err != nil {
		t.Fatalf("feature's own commit did not land: %v", err)
	}
}

// TestSecretScanCategorizedSeverity_ExplicitBlockRefusesMerge proves the
// opt-in: the identical categorized finding, with
// secret_scan_categorized_severity: block configured, now refuses the merge
// with precondition_unmet instead of landing as a caveat.
func TestSecretScanCategorizedSeverity_ExplicitBlockRefusesMerge(t *testing.T) {
	bin := buildCLI(t)
	dir := categorizedFindingRepo(t)
	// Committed, not merely written: merge's own gate reads git-tools.yaml
	// from the prospective, trial-merged tree (see prospectiveMergeScanDir),
	// which never carries dir's uncommitted content.
	commitConfig(t, dir, "secret_scan_categorized_severity: block\n", "opt into hard-block for categorized findings")
	head := runGit(t, dir, "rev-parse", "HEAD")

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature")
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("status=%s exit=%d, want precondition_unmet/30: %+v", r.Status, exit, r)
	}
	assertRefusalNamesFinding(t, r, "widget.conf")
	if got := runGit(t, dir, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD moved from %s to %s — merge landed despite the block posture", head, got)
	}
}

// TestSecretScanCategorizedSeverity_DefaultWarnLandsPushAsCaveat is the merge
// case above for the second, separately wired caveat-threading path: merge
// folds scanGate's caveats in at its own terminal result, while push, tag
// create, and rebase route theirs through pushRef/finishResult instead. The
// defect this branch fixed (scanGate discarding its caveats) was invisible
// end to end until a test asserted the caveat on a command's own result, so
// each distinct threading path needs one such assertion rather than trusting
// the shared helper.
func TestSecretScanCategorizedSeverity_DefaultWarnLandsPushAsCaveat(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	t.Setenv(betterleaksBinEnvVar, writeFixtureBetterleaksBinary(t,
		fixtureBetterleaksReport(fixtureBetterleaksRuleID, fixtureBetterleaksValue, "widget.conf")))
	bare := newBareRemote(t, dir)
	before := runGit(t, bare, "rev-parse", "refs/heads/main")
	tip := commitFile(t, dir, "widget.conf", "widget_value = "+fixtureBetterleaksValue+"\n", "add widget config")

	r, exit := runCLIIn(t, bin, dir, "push", "main")
	if r.Status != "caveats" || exit != 10 {
		t.Fatalf("status=%s exit=%d, want caveats/10 (a categorized finding must warn, not block, and the caveat must reach push's own result): %+v", r.Status, exit, r)
	}
	var sawFinding bool
	for _, c := range r.Caveats {
		if code, _ := c["code"].(string); code != "caveats.githooks.categorized_secret_detected" {
			continue
		}
		context, _ := c["context"].(map[string]any)
		if path, _ := context["path"].(string); path == "widget.conf" {
			sawFinding = true
		}
	}
	if !sawFinding {
		t.Fatalf("push's own result does not carry the gate's warn-only caveat naming widget.conf: %+v", r.Caveats)
	}
	if got := runGit(t, bare, "rev-parse", "refs/heads/main"); got != tip {
		t.Fatalf("remote main is at %s, want the pushed tip %s (was %s) — the warn-only posture must still publish", got, tip, before)
	}
}

// TestSecretScanCategorizedSeverity_UncategorizedFindingAlwaysBlocks proves
// the posture only ever governs betterleaks-sourced, categorized findings: a
// plain ScanSecrets finding (no Category) still hard-blocks the merge even
// with secret_scan_categorized_severity: warn configured explicitly.
func TestSecretScanCategorizedSeverity_UncategorizedFindingAlwaysBlocks(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	// Committed, not merely written: merge's gate reads git-tools.yaml from the
	// prospective, trial-merged tree, so an untracked "warn" here would never
	// reach it — the case would then only re-prove the default rather than the
	// explicit setting it claims to test.
	commitConfig(t, dir, "secret_scan_categorized_severity: warn\n", "configure the warn-only posture explicitly")
	head := commitNestedFile(t, dir, "config/prod.env", secretFixtureSecret, "add prod config")
	runGit(t, dir, "branch", "feature")
	runGit(t, dir, "checkout", "-q", "feature")
	commitFile(t, dir, "feature.txt", "feature\n", "feature work")
	runGit(t, dir, "checkout", "-q", "main")

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature")
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("status=%s exit=%d, want precondition_unmet/30 (an uncategorized finding must block regardless of the warn posture): %+v", r.Status, exit, r)
	}
	assertRefusalNamesFinding(t, r, "config/prod.env")
	if got := runGit(t, dir, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD moved from %s to %s — merge landed despite the uncategorized finding", head, got)
	}
}

// TestSecretScanCategorizedSeverity_InvalidValueRefusesBeforeScanning proves
// an unrecognized secret_scan_categorized_severity value fails cleanly with a
// usage error, before any scan runs — mirroring how an invalid --privacy-tier
// or a malformed secret_scan_exempt entry already refuse.
func TestSecretScanCategorizedSeverity_InvalidValueRefusesBeforeScanning(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	// Committed, not merely written: merge's own gate reads git-tools.yaml
	// from the prospective, trial-merged tree (see prospectiveMergeScanDir),
	// which never carries dir's uncommitted content — an untracked invalid
	// value here would never reach scanGate's validation at all.
	commitConfig(t, dir, "secret_scan_categorized_severity: nonsense\n", "set an invalid secret_scan_categorized_severity")
	head := commitNestedFile(t, dir, "docs/real.md", "hello\n", "add doc")
	runGit(t, dir, "branch", "feature")
	runGit(t, dir, "checkout", "-q", "feature")
	commitFile(t, dir, "feature.txt", "feature\n", "feature work")
	runGit(t, dir, "checkout", "-q", "main")

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature")
	if r.Status != "usage" || exit != 50 {
		t.Fatalf("status=%s exit=%d, want usage/50: %+v", r.Status, exit, r)
	}
	if len(r.Errors) == 0 || !strings.Contains(r.Errors[0]["message"].(string), "nonsense") {
		t.Fatalf("usage error does not name the invalid value: %+v", r.Errors)
	}
	if got := runGit(t, dir, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD moved from %s to %s — merge attempted a scan despite the invalid config", head, got)
	}
}
