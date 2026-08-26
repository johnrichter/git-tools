// Tests for the content-guardrail gate merge, push, and rebase share:
// scanGate refuses each verb on a tracked executable file with binary
// content, leaves the repository exactly as it was, and lets the same file
// through once it loses its executable bit.
package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// nulByteExecutable is a short payload with a NUL byte inside the scanner's
// sniff window, small enough to stay under any size-based rule — only the
// executable-mode rule can flag it.
var nulByteExecutable = []byte("junk\x00binary")

// commitFileMode is commitFile with an explicit file mode, for planting a
// tracked fixture whose executable bit the content-guardrail gate cares
// about.
func commitFileMode(t *testing.T, dir, name string, content []byte, mode os.FileMode, message string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), content, mode); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", name)
	runGit(t, dir, "commit", "-q", "-m", message)
	return runGit(t, dir, "rev-parse", "HEAD")
}

// assertRefusalNamesFinding checks r's governing error against the three
// things a scanGate refusal must report: the offending path and the rule
// that fired (both in the error's context) and a remedy naming that same
// path (in the error's triage instruction). The scanner's own message text
// names neither path nor rule on its own, so the path/rule live in context.
func assertRefusalNamesFinding(t *testing.T, r wireResult, wantPath string) {
	t.Helper()
	if len(r.Errors) == 0 {
		t.Fatal("precondition_unmet result carries no errors for the planted finding")
	}
	context, _ := r.Errors[0]["context"].(map[string]any)
	if path, _ := context["path"].(string); path != wantPath {
		t.Fatalf("refusal does not name the offending path: %+v", r.Errors[0])
	}
	if rule, _ := context["rule"].(string); rule == "" {
		t.Fatalf("refusal does not name the rule that fired: %+v", r.Errors[0])
	}
	triage, _ := r.Errors[0]["triage"].(map[string]any)
	instruction, _ := triage["instruction"].(string)
	if !strings.Contains(instruction, wantPath) {
		t.Fatalf("remedy does not name the offending path: %+v", triage)
	}
}

// assertRefusalNamesOnlyFinding is assertRefusalNamesFinding for a refusal
// that must report wantPath's findings and nothing else. scanGate collapses
// every finding into a single governing diagnostic naming only the first
// path, so the aggregate count it carries in that diagnostic's "findings"
// context field is the only evidence in the record that no second path was
// flagged behind the one named: without the count, a refusal that also
// (wrongly) flagged exemptPath is indistinguishable from one that correctly
// ignored it. wantFindings is how many findings wantPath alone accounts for.
func assertRefusalNamesOnlyFinding(t *testing.T, r wireResult, wantPath, exemptPath string, wantFindings int) {
	t.Helper()
	assertRefusalNamesFinding(t, r, wantPath)
	context, _ := r.Errors[0]["context"].(map[string]any)
	count, ok := context["findings"].(float64)
	if !ok {
		t.Fatalf("refusal reports no aggregate findings count: %+v", r.Errors[0])
	}
	if int(count) != wantFindings {
		t.Fatalf("refusal aggregated %d findings, want %d (%s alone) — %s was flagged too, so its marker exemption did not hold: %+v", int(count), wantFindings, wantPath, exemptPath, r.Errors[0])
	}
	for _, d := range append(append([]map[string]any{}, r.Errors...), r.Caveats...) {
		context, _ := d["context"].(map[string]any)
		if path, _ := context["path"].(string); path == exemptPath {
			t.Fatalf("exempted path %s is named by a reported diagnostic: %+v", exemptPath, d)
		}
	}
}

func TestScanGate_MergeRefusesExecutableWithNulByte(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	head := commitFileMode(t, dir, "payload.sh", nulByteExecutable, 0o755, "add payload")
	runGit(t, dir, "branch", "feature")
	runGit(t, dir, "checkout", "-q", "feature")
	commitFile(t, dir, "feature.txt", "feature\n", "feature work")
	runGit(t, dir, "checkout", "-q", "main")

	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature")
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("status=%s exit=%d, want precondition_unmet/30: %+v", r.Status, exit, r)
	}
	assertRefusalNamesFinding(t, r, "payload.sh")
	if got := runGit(t, dir, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD moved from %s to %s — merge landed despite the guardrail finding", head, got)
	}
	if got := runGit(t, dir, "rev-parse", "main"); got != head {
		t.Fatalf("main advanced from %s to %s — merge landed despite the guardrail finding", head, got)
	}
}

func TestScanGate_MergeProceedsWhenFixtureIsNotExecutable(t *testing.T) {
	bin := buildCLI(t)
	dir := signingRepo(t)
	commitFileMode(t, dir, "payload.sh", nulByteExecutable, 0o644, "add payload")
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
}

func TestScanGate_PushRefusesExecutableWithNulByte(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	bare := newBareRemote(t, dir)
	before := runGit(t, bare, "rev-parse", "refs/heads/main")
	commitFileMode(t, dir, "payload.sh", nulByteExecutable, 0o755, "add payload")

	r, exit := runCLIIn(t, bin, dir, "push", "main")
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("status=%s exit=%d, want precondition_unmet/30: %+v", r.Status, exit, r)
	}
	assertRefusalNamesFinding(t, r, "payload.sh")
	if got := runGit(t, bare, "rev-parse", "refs/heads/main"); got != before {
		t.Fatalf("remote main moved from %s to %s — push published despite the guardrail finding", before, got)
	}
}

func TestScanGate_PushProceedsWhenFixtureIsNotExecutable(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	bare := newBareRemote(t, dir)
	tip := commitFileMode(t, dir, "payload.sh", nulByteExecutable, 0o644, "add payload")

	r, exit := runCLIIn(t, bin, dir, "push", "main")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	if got := runGit(t, bare, "rev-parse", "refs/heads/main"); got != tip {
		t.Fatalf("remote main = %s, want %s", got, tip)
	}
}

func TestScanGate_RebaseRefusesExecutableWithNulByte(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	runGit(t, dir, "branch", "feature")
	commitFile(t, dir, "main-only.txt", "main\n", "main advances")
	runGit(t, dir, "checkout", "-q", "feature")
	tip := commitFileMode(t, dir, "payload.sh", nulByteExecutable, 0o755, "add payload")

	r, exit := runCLI(t, bin, "--repo", dir, "rebase", "main")
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("status=%s exit=%d, want precondition_unmet/30: %+v", r.Status, exit, r)
	}
	assertRefusalNamesFinding(t, r, "payload.sh")
	if got := runGit(t, dir, "rev-parse", "feature"); got != tip {
		t.Fatalf("feature moved from %s to %s — rebase replayed despite the guardrail finding", tip, got)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "rebase-merge")); !os.IsNotExist(err) {
		t.Fatal("a rebase-merge state directory was left behind by a refused rebase")
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "rebase-apply")); !os.IsNotExist(err) {
		t.Fatal("a rebase-apply state directory was left behind by a refused rebase")
	}
}

func TestScanGate_RebaseProceedsWhenFixtureIsNotExecutable(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	runGit(t, dir, "branch", "feature")
	commitFile(t, dir, "main-only.txt", "main\n", "main advances")
	runGit(t, dir, "checkout", "-q", "feature")
	commitFileMode(t, dir, "payload.sh", nulByteExecutable, 0o644, "add payload")

	r, exit := runCLI(t, bin, "--repo", dir, "rebase", "main")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	mainHead := runGit(t, dir, "rev-parse", "main")
	featureParent := runGit(t, dir, "rev-parse", "HEAD^")
	if featureParent != mainHead {
		t.Fatalf("feature was not replayed onto main: parent=%s main=%s", featureParent, mainHead)
	}
}

// markerFrontmatter is a file body carrying the public tier's forbidden
// "privacy: internal" marker in its leading frontmatter block, exactly the
// shape a test/eval fixture legitimately needs as literal data rather than
// as a real sensitivity declaration.
const markerFrontmatter = "---\nprivacy: internal\n---\n\nfixture body\n"

// markerFrontmatterFindings is how many privacy findings one non-exempt
// markerFrontmatter file accounts for at the public tier: the forbidden
// "privacy: internal" marker itself (rule forbidden_marker) and the
// declares-privacy-but-not-privacy:public pair check (rule not_public_pair)
// both fire on the same frontmatter block. It is the per-file unit the
// scoping assertion counts in, so a second flagged file is visible as a
// multiple of it rather than as an opaque number.
const markerFrontmatterFindings = 2

// writeConfig writes a .git-tools.yaml into dir, the shape loadConfigFile
// auto-discovers from the invoking process's own working directory when no
// --config flag is passed.
func writeConfig(t *testing.T, dir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, ".git-tools.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// commitNestedFile is commitFile for a fixture whose path carries a
// directory component (e.g. the exempt directories these tests plant marker
// fixtures under), creating that directory first.
func commitNestedFile(t *testing.T, dir, name, content, message string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, filepath.Dir(name)), 0o755); err != nil {
		t.Fatal(err)
	}
	return commitFile(t, dir, name, content, message)
}

// TestScanGate_MarkerBlocksCleanMergeAndPush pins the default-deny baseline
// the exemption is measured against: with no privacy_marker_exempt
// configured, a file whose frontmatter carries a forbidden marker, already
// committed on the target branch, refuses a merge or push that touches
// nothing related to that file. That refusal is the correct default — it is
// also the bug this feature answers, which was that no configuration could
// ever release it.
func TestScanGate_MarkerBlocksCleanMergeAndPush(t *testing.T) {
	t.Run("merge", func(t *testing.T) {
		bin := buildCLI(t)
		dir := initRepo(t)
		head := commitNestedFile(t, dir, "fixtures/sample.md", markerFrontmatter, "add fixture sample")
		runGit(t, dir, "branch", "feature")
		runGit(t, dir, "checkout", "-q", "feature")
		commitFile(t, dir, "feature.txt", "feature\n", "feature work")
		runGit(t, dir, "checkout", "-q", "main")

		r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature")
		if r.Status != "precondition_unmet" || exit != 30 {
			t.Fatalf("status=%s exit=%d, want precondition_unmet/30: %+v", r.Status, exit, r)
		}
		assertRefusalNamesFinding(t, r, "fixtures/sample.md")
		if got := runGit(t, dir, "rev-parse", "HEAD"); got != head {
			t.Fatalf("HEAD moved from %s to %s — merge landed despite the guardrail finding", head, got)
		}
	})

	t.Run("push", func(t *testing.T) {
		bin := buildCLI(t)
		dir := initRepo(t)
		bare := newBareRemote(t, dir)
		before := runGit(t, bare, "rev-parse", "refs/heads/main")
		commitNestedFile(t, dir, "fixtures/sample.md", markerFrontmatter, "add fixture sample")

		r, exit := runCLIIn(t, bin, dir, "push", "main")
		if r.Status != "precondition_unmet" || exit != 30 {
			t.Fatalf("status=%s exit=%d, want precondition_unmet/30: %+v", r.Status, exit, r)
		}
		assertRefusalNamesFinding(t, r, "fixtures/sample.md")
		if got := runGit(t, bare, "rev-parse", "refs/heads/main"); got != before {
			t.Fatalf("remote main moved from %s to %s — push published despite the guardrail finding", before, got)
		}
	})
}

// TestScanGate_PrivacyMarkerExemptConfigAllowsMergeAndPush proves the fix: the
// identical marker-bearing fixture from the bug reproduction above no longer
// blocks merge or push once a .git-tools.yaml in the scanned repo names its
// path under privacy_marker_exempt.
func TestScanGate_PrivacyMarkerExemptConfigAllowsMergeAndPush(t *testing.T) {
	t.Run("merge", func(t *testing.T) {
		bin := buildCLI(t)
		dir := signingRepo(t)
		writeConfig(t, dir, "privacy_marker_exempt:\n  - fixtures\n")
		commitNestedFile(t, dir, "fixtures/sample.md", markerFrontmatter, "add fixture sample")
		runGit(t, dir, "branch", "feature")
		runGit(t, dir, "checkout", "-q", "feature")
		tip := commitFile(t, dir, "feature.txt", "feature\n", "feature work")
		runGit(t, dir, "checkout", "-q", "main")

		r, exit := runCLIIn(t, bin, dir, "merge", "feature")
		if r.Status != "success" || exit != 0 {
			t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
		}
		if got := runGit(t, dir, "rev-parse", "HEAD"); got != tip {
			t.Fatalf("main did not fast-forward to feature tip: got %s want %s", got, tip)
		}
	})

	t.Run("push", func(t *testing.T) {
		bin := buildCLI(t)
		dir := initRepo(t)
		writeConfig(t, dir, "privacy_marker_exempt:\n  - fixtures\n")
		bare := newBareRemote(t, dir)
		tip := commitNestedFile(t, dir, "fixtures/sample.md", markerFrontmatter, "add fixture sample")

		r, exit := runCLIIn(t, bin, dir, "push", "main")
		if r.Status != "success" || exit != 0 {
			t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
		}
		if got := runGit(t, bare, "rev-parse", "refs/heads/main"); got != tip {
			t.Fatalf("remote main = %s, want %s", got, tip)
		}
	})
}

// TestScanGate_PrivacyMarkerExemptConfigStaysScoped proves the exemption
// applies only to the named path: with the identical fixtures/ exemption from
// the previous test in force, a second marker-bearing file outside fixtures/
// still correctly refuses the same merge/push, in the same run — so the
// exemption cannot be mistaken for a blanket suppression of the marker check.
// The refusal's finding count has to come out at exactly one file's worth:
// checking only the path the refusal names would pass even with the
// exemption entirely inert, since the out-of-scope file is reported first
// either way.
func TestScanGate_PrivacyMarkerExemptConfigStaysScoped(t *testing.T) {
	t.Run("merge", func(t *testing.T) {
		bin := buildCLI(t)
		dir := initRepo(t)
		writeConfig(t, dir, "privacy_marker_exempt:\n  - fixtures\n")
		commitNestedFile(t, dir, "fixtures/sample.md", markerFrontmatter, "add fixture sample")
		head := commitNestedFile(t, dir, "docs/real.md", markerFrontmatter, "add real doc")
		runGit(t, dir, "branch", "feature")
		runGit(t, dir, "checkout", "-q", "feature")
		commitFile(t, dir, "feature.txt", "feature\n", "feature work")
		runGit(t, dir, "checkout", "-q", "main")

		r, exit := runCLIIn(t, bin, dir, "merge", "feature")
		if r.Status != "precondition_unmet" || exit != 30 {
			t.Fatalf("status=%s exit=%d, want precondition_unmet/30: %+v", r.Status, exit, r)
		}
		assertRefusalNamesOnlyFinding(t, r, "docs/real.md", "fixtures/sample.md", markerFrontmatterFindings)
		if got := runGit(t, dir, "rev-parse", "HEAD"); got != head {
			t.Fatalf("HEAD moved from %s to %s — merge landed despite the out-of-scope finding", head, got)
		}
	})

	t.Run("push", func(t *testing.T) {
		bin := buildCLI(t)
		dir := initRepo(t)
		writeConfig(t, dir, "privacy_marker_exempt:\n  - fixtures\n")
		bare := newBareRemote(t, dir)
		before := runGit(t, bare, "rev-parse", "refs/heads/main")
		commitNestedFile(t, dir, "fixtures/sample.md", markerFrontmatter, "add fixture sample")
		commitNestedFile(t, dir, "docs/real.md", markerFrontmatter, "add real doc")

		r, exit := runCLIIn(t, bin, dir, "push", "main")
		if r.Status != "precondition_unmet" || exit != 30 {
			t.Fatalf("status=%s exit=%d, want precondition_unmet/30: %+v", r.Status, exit, r)
		}
		assertRefusalNamesOnlyFinding(t, r, "docs/real.md", "fixtures/sample.md", markerFrontmatterFindings)
		if got := runGit(t, bare, "rev-parse", "refs/heads/main"); got != before {
			t.Fatalf("remote main moved from %s to %s — push published despite the out-of-scope finding", before, got)
		}
	})
}

// TestScanGate_PrivacyMarkerExemptConfigNotLoadedFromRepoFlagTarget documents
// a known, intentional-for-now gap: when merge --repo names a directory
// other than the invoking process's own working directory, the
// .git-tools.yaml auto-discovered by loadConfigFile still comes from the
// process's cwd, not from --repo's target. A config file placed inside the
// --repo directory is not picked up, so the marker exemption does not apply
// and the merge still refuses.
func TestScanGate_PrivacyMarkerExemptConfigNotLoadedFromRepoFlagTarget(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	writeConfig(t, dir, "privacy_marker_exempt:\n  - fixtures\n")
	head := commitNestedFile(t, dir, "fixtures/sample.md", markerFrontmatter, "add fixture sample")
	runGit(t, dir, "branch", "feature")
	runGit(t, dir, "checkout", "-q", "feature")
	commitFile(t, dir, "feature.txt", "feature\n", "feature work")
	runGit(t, dir, "checkout", "-q", "main")

	// No cmd.Dir override here: the process's cwd stays wherever `go test`
	// runs it, which is not dir, so dir's .git-tools.yaml is never read.
	r, exit := runCLI(t, bin, "--repo", dir, "merge", "feature")
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("status=%s exit=%d, want precondition_unmet/30 (the --repo target's .git-tools.yaml is not loaded from the process's own cwd): %+v", r.Status, exit, r)
	}
	assertRefusalNamesFinding(t, r, "fixtures/sample.md")
	if got := runGit(t, dir, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD moved from %s to %s — merge landed despite the guardrail finding", head, got)
	}
}

// TestScanGate_MalformedPrivacyMarkerExemptRefusesBeforeScanning proves the
// fix for the silent-disable bug: a malformed privacy_marker_exempt glob
// (an unclosed bracket, the reviewer's own reproduction) must fail the merge
// and push with a usage error naming the bad pattern, before scanGate ever
// runs a scan — not silently exempt the whole repository from the marker
// check, which is what fsx.ClassifyPath's fail-closed treatment of a
// malformed SkipRules-style pattern would otherwise do here, since
// MarkerExemptRules reads "matched" as "exempt" rather than "be careful".
func TestScanGate_MalformedPrivacyMarkerExemptRefusesBeforeScanning(t *testing.T) {
	t.Run("merge", func(t *testing.T) {
		bin := buildCLI(t)
		dir := initRepo(t)
		writeConfig(t, dir, "privacy_marker_exempt:\n  - \"fixtures[\"\n")
		head := commitNestedFile(t, dir, "docs/real.md", markerFrontmatter, "add real doc")
		runGit(t, dir, "branch", "feature")
		runGit(t, dir, "checkout", "-q", "feature")
		commitFile(t, dir, "feature.txt", "feature\n", "feature work")
		runGit(t, dir, "checkout", "-q", "main")

		r, exit := runCLIIn(t, bin, dir, "merge", "feature")
		if r.Status != "usage" || exit != 50 {
			t.Fatalf("status=%s exit=%d, want usage/50: %+v", r.Status, exit, r)
		}
		if len(r.Errors) == 0 || !strings.Contains(r.Errors[0]["message"].(string), "fixtures[") {
			t.Fatalf("usage error does not name the malformed pattern: %+v", r.Errors)
		}
		if got := runGit(t, dir, "rev-parse", "HEAD"); got != head {
			t.Fatalf("HEAD moved from %s to %s — merge attempted a scan despite the malformed config", head, got)
		}
	})

	t.Run("push", func(t *testing.T) {
		bin := buildCLI(t)
		dir := initRepo(t)
		writeConfig(t, dir, "privacy_marker_exempt:\n  - \"fixtures[\"\n")
		bare := newBareRemote(t, dir)
		before := runGit(t, bare, "rev-parse", "refs/heads/main")
		commitNestedFile(t, dir, "docs/real.md", markerFrontmatter, "add real doc")

		r, exit := runCLIIn(t, bin, dir, "push", "main")
		if r.Status != "usage" || exit != 50 {
			t.Fatalf("status=%s exit=%d, want usage/50: %+v", r.Status, exit, r)
		}
		if len(r.Errors) == 0 || !strings.Contains(r.Errors[0]["message"].(string), "fixtures[") {
			t.Fatalf("usage error does not name the malformed pattern: %+v", r.Errors)
		}
		if got := runGit(t, bare, "rev-parse", "refs/heads/main"); got != before {
			t.Fatalf("remote main moved from %s to %s — push attempted a scan despite the malformed config", before, got)
		}
	})
}

// TestScanGate_ValidPrivacyMarkerExemptStillWorksAlongsideValidation is a
// targeted no-regression check that the new malformed-pattern validation
// does not reject a well-formed privacy_marker_exempt entry: identical setup
// to TestScanGate_PrivacyMarkerExemptConfigAllowsMergeAndPush, run through
// the same code path the malformed-pattern test above exercises.
func TestScanGate_ValidPrivacyMarkerExemptStillWorksAlongsideValidation(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	writeConfig(t, dir, "privacy_marker_exempt:\n  - fixtures\n")
	bare := newBareRemote(t, dir)
	tip := commitNestedFile(t, dir, "fixtures/sample.md", markerFrontmatter, "add fixture sample")

	r, exit := runCLIIn(t, bin, dir, "push", "main")
	if r.Status != "success" || exit != 0 {
		t.Fatalf("status=%s exit=%d, want success/0: %+v", r.Status, exit, r)
	}
	if got := runGit(t, bare, "rev-parse", "refs/heads/main"); got != tip {
		t.Fatalf("remote main = %s, want %s", got, tip)
	}
}

// TestScanGate_AllThreeWriteVerbsCallTheSharedEntryPoint pins the "one
// shared scan entry point, not three copies" requirement at the source
// level: each write verb's own file calls scanGate exactly once, and none
// of them declares a copy of its own.
func TestScanGate_AllThreeWriteVerbsCallTheSharedEntryPoint(t *testing.T) {
	for _, name := range []string{"merge.go", "push.go", "rebase.go"} {
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		text := string(src)
		if got := strings.Count(text, "scanGate("); got != 1 {
			t.Errorf("%s calls scanGate %d times, want exactly 1", name, got)
		}
		if strings.Contains(text, "func scanGate(") {
			t.Errorf("%s declares its own scanGate instead of calling the shared one in scan.go", name)
		}
	}
	scanSrc, err := os.ReadFile("scan.go")
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(scanSrc), "func scanGate("); got != 1 {
		t.Fatalf("scan.go declares scanGate %d times, want exactly 1", got)
	}
}
