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
