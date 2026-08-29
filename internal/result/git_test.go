package result

import (
	"errors"
	"strings"
	"testing"

	"github.com/johnrichter/claude-shared-tooling/go/git"
)

func TestConflictDiagnostic_StaleRef(t *testing.T) {
	err := &git.StaleRefError{Ref: "refs/heads/main", ExpectedOld: "aaa", ActualOld: "bbb"}
	diag, ok, buildErr := ConflictDiagnostic(err)
	if buildErr != nil {
		t.Fatalf("ConflictDiagnostic: %v", buildErr)
	}
	if !ok {
		t.Fatal("StaleRefError was not recognized as a conflict")
	}
	if diag.Code != "conflict.git.stale_ref" {
		t.Errorf("code = %q, want conflict.git.stale_ref", diag.Code)
	}
	if diag.Context["ref"] != "refs/heads/main" || diag.Context["expected_old"] != "aaa" || diag.Context["actual_old"] != "bbb" {
		t.Errorf("context missing stale-ref fields: %+v", diag.Context)
	}
}

func TestConflictDiagnostic_MergeConflict(t *testing.T) {
	err := &git.ConflictError{Op: "merge", Files: []string{"a.txt", "b.txt"}}
	diag, ok, buildErr := ConflictDiagnostic(err)
	if buildErr != nil {
		t.Fatalf("ConflictDiagnostic: %v", buildErr)
	}
	if !ok {
		t.Fatal("ConflictError was not recognized as a conflict")
	}
	if diag.Code != "conflict.git.merge_conflict" {
		t.Errorf("code = %q, want conflict.git.merge_conflict", diag.Code)
	}
}

func TestConflictDiagnostic_RebaseConflict(t *testing.T) {
	err := &git.ConflictError{Op: "rebase", Files: []string{"a.txt"}}
	diag, ok, _ := ConflictDiagnostic(err)
	if !ok || diag.Code != "conflict.git.rebase_conflict" {
		t.Errorf("code = %q ok=%v, want conflict.git.rebase_conflict/true", diag.Code, ok)
	}
}

func TestConflictDiagnostic_ManyConflictFilesStillBuilds(t *testing.T) {
	files := make([]string, 500)
	for i := range files {
		files[i] = "some/deeply/nested/path/that/is/reasonably/long/file.txt"
	}
	err := &git.ConflictError{Op: "merge", Files: files}
	diag, ok, buildErr := ConflictDiagnostic(err)
	if buildErr != nil {
		t.Fatalf("a conflict over many files must still build a diagnostic, not fail: %v", buildErr)
	}
	if !ok || diag.Code != "conflict.git.merge_conflict" {
		t.Fatalf("code = %q ok=%v, want conflict.git.merge_conflict/true", diag.Code, ok)
	}
}

func TestConflictDiagnostic_FilenameWithControlCharsStillBuilds(t *testing.T) {
	// A pathological filename (embedded newline/tab, as git itself can quote
	// or a hostile tree can contain) must not leave a raw control character
	// in the diagnostic message, which clikit.NewError rejects outright.
	err := &git.ConflictError{Op: "merge", Files: []string{"a\nb\tc.txt", "d\x00e.txt"}}
	diag, ok, buildErr := ConflictDiagnostic(err)
	if buildErr != nil {
		t.Fatalf("a conflict with control characters in a filename must still build: %v", buildErr)
	}
	if !ok || diag.Code != "conflict.git.merge_conflict" {
		t.Fatalf("code = %q ok=%v, want conflict.git.merge_conflict/true", diag.Code, ok)
	}
	for i, r := range diag.Message {
		if r < 0x20 || r == 0x7f {
			t.Fatalf("diag.Message retains a raw control character at rune %d: %q", i, diag.Message)
		}
	}
}

func TestConflictDiagnostic_MessageExactlyAtBoundaryStillBuilds(t *testing.T) {
	// git.ConflictError.Error() is "git: merge conflict in 1 file(s): " (35
	// chars) + Files[0]; pick Files[0] so the total lands exactly at 4096 and
	// one past it, to pin the off-by-one in the sanitize truncation.
	prefix := (&git.ConflictError{Op: "merge", Files: []string{""}}).Error()
	for _, total := range []int{4096, 4097, 4098} {
		pad := total - len(prefix)
		if pad < 1 {
			t.Fatalf("test setup: prefix %q already exceeds target length %d", prefix, total)
		}
		err := &git.ConflictError{Op: "merge", Files: []string{strings.Repeat("x", pad)}}
		diag, ok, buildErr := ConflictDiagnostic(err)
		if buildErr != nil {
			t.Fatalf("total=%d: conflict message must still build a diagnostic: %v", total, buildErr)
		}
		if !ok {
			t.Fatalf("total=%d: not recognized as a conflict", total)
		}
		if len(diag.Message) > 4096 {
			t.Fatalf("total=%d: diag.Message length %d exceeds clikit's 4096 line bound", total, len(diag.Message))
		}
	}
}

func TestConflictDiagnostic_UnrecognizedErrorIsNotOK(t *testing.T) {
	_, ok, buildErr := ConflictDiagnostic(errors.New("some other git failure"))
	if buildErr != nil {
		t.Fatalf("ConflictDiagnostic: %v", buildErr)
	}
	if ok {
		t.Fatal("a plain error was misclassified as a recognized conflict")
	}
}

func TestRewriteOutcomeData_OmitsEmptyOptionalFields(t *testing.T) {
	data := RewriteOutcomeData(&git.RewriteOutcome{Ref: "HEAD", OldHead: "aaa", BackupRef: "refs/backup/aaa"})
	if _, ok := data["new_head"]; ok {
		t.Error("new_head present despite empty NewHead")
	}
	if _, ok := data["push_cmd"]; ok {
		t.Error("push_cmd present despite empty PushCmd")
	}
	if data["ref"] != "HEAD" || data["old_head"] != "aaa" || data["backup_ref"] != "refs/backup/aaa" {
		t.Errorf("required fields missing/wrong: %+v", data)
	}
}

func TestRewriteOutcomeData_IncludesPopulatedOptionalFields(t *testing.T) {
	data := RewriteOutcomeData(&git.RewriteOutcome{
		Ref: "HEAD", OldHead: "aaa", NewHead: "bbb", BackupRef: "refs/backup/aaa",
		PushCmd: []string{"git", "push", "--force-with-lease", "origin", "HEAD"},
	})
	if data["new_head"] != "bbb" {
		t.Errorf("new_head = %v, want bbb", data["new_head"])
	}
	if _, ok := data["push_cmd"]; !ok {
		t.Error("push_cmd missing despite a populated PushCmd")
	}
}
