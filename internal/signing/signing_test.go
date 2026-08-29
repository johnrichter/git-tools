// Tests for the signing gate against scratch repositories: each refusal path's
// Code, Message and Advice, the two non-refusal report actions the gate reaches
// without a signing key, and the Refusal value's error and Context contracts.
// Paths that need a working signing key (resigned/would_resign) are exercised
// end to end by the merge verb's own suite.
package signing

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/johnrichter/claude-shared-tooling/go/clikit"
	"github.com/johnrichter/claude-shared-tooling/go/git"
)

// scratchRepo creates a throwaway repo with one unsigned commit on branch main
// and returns its path. Signing is disabled so the gate's own decisions, not
// the fixture, decide each test.
func scratchRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitCmd(t, dir, "init", "-q", "-b", "main")
	gitCmd(t, dir, "config", "user.name", "Test User")
	gitCmd(t, dir, "config", "user.email", "test@example.com")
	gitCmd(t, dir, "config", "commit.gpgsign", "false")
	commit(t, dir, "base.txt", "base\n", "base")
	return dir
}

func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func commit(t *testing.T, dir, name, content, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	gitCmd(t, dir, "add", name)
	gitCmd(t, dir, "commit", "-q", "-m", message)
}

func open(t *testing.T, dir string) *git.Repo {
	t.Helper()
	repo, err := git.Open(context.Background(), dir)
	if err != nil {
		t.Fatalf("open %s: %v", dir, err)
	}
	return repo
}

func TestGate_SourceNotBranch(t *testing.T) {
	repo := open(t, scratchRepo(t))
	_, refusal := Gate(context.Background(), repo, "main", []string{"ghost"}, false, NewProber(repo))
	if refusal == nil {
		t.Fatal("merging a non-branch was not refused")
	}
	if got := refusal.Code(); got != "precondition_unmet.git.merge_source_not_branch" {
		t.Errorf("code = %q", got)
	}
	if got, want := refusal.Message(), `"ghost" is not a local branch, so the signing gate cannot re-sign what the merge would land`; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
	if want := clikit.Manual("create a local branch at ghost and merge that instead"); !reflect.DeepEqual(refusal.Advice(), want) {
		t.Errorf("advice = %+v, want %+v", refusal.Advice(), want)
	}
}

func TestGate_NoForkPoint(t *testing.T) {
	dir := scratchRepo(t)
	gitCmd(t, dir, "switch", "-q", "--orphan", "beta")
	commit(t, dir, "beta.txt", "beta\n", "beta work")
	gitCmd(t, dir, "checkout", "-q", "main")

	repo := open(t, dir)
	_, refusal := Gate(context.Background(), repo, "main", []string{"beta"}, false, NewProber(repo))
	if refusal == nil {
		t.Fatal("merging unrelated history was not refused")
	}
	if got := refusal.Code(); got != "precondition_unmet.git.merge_no_fork_point" {
		t.Errorf("code = %q", got)
	}
	if got, want := refusal.Message(), "beta and main share no common ancestor, so there is no range for the signing gate to sign"; got != want {
		t.Errorf("message = %q, want %q", got, want)
	}
	if want := clikit.Manual("rebase the unrelated history onto the target branch first, then merge"); !reflect.DeepEqual(refusal.Advice(), want) {
		t.Errorf("advice = %+v, want %+v", refusal.Advice(), want)
	}
	// A refusal with nothing rewritten before it carries the source alone.
	if got := refusal.Context(); !reflect.DeepEqual(got, map[string]any{"source": "beta"}) {
		t.Errorf("context = %+v, want only the source", got)
	}
}

func TestGate_SigningKeyUnresolved(t *testing.T) {
	dir := scratchRepo(t)
	// A repository-local ssh key that does not exist resolves to no key at all,
	// and being local it overrides the host's git config.
	gitCmd(t, dir, "config", "gpg.format", "ssh")
	gitCmd(t, dir, "config", "user.signingkey", filepath.Join(t.TempDir(), "absent-key.pub"))
	gitCmd(t, dir, "checkout", "-q", "-b", "feature", "main")
	commit(t, dir, "feature.txt", "feature\n", "feature work") // unsigned
	gitCmd(t, dir, "checkout", "-q", "main")

	repo := open(t, dir)
	_, refusal := Gate(context.Background(), repo, "main", []string{"feature"}, false, NewProber(repo))
	if refusal == nil {
		t.Fatal("an unsigned source with no resolvable key was not refused")
	}
	if got := refusal.Code(); got != "precondition_unmet.git.signing_key_unresolved" {
		t.Errorf("code = %q", got)
	}
	if got := refusal.Message(); !strings.HasPrefix(got, "no key resolved for commit signing, so merging feature would land unsigned commits: ") {
		t.Errorf("message = %q", got)
	}
	if want := clikit.Manual("configure a signing key (gpg.format plus user.signingkey, or this environment's signing setup) and re-run; nothing was merged"); !reflect.DeepEqual(refusal.Advice(), want) {
		t.Errorf("advice = %+v, want %+v", refusal.Advice(), want)
	}
}

func TestGate_EmptyRangeIsSkippedNotRefused(t *testing.T) {
	dir := scratchRepo(t)
	gitCmd(t, dir, "branch", "feature") // points at main; no commits ahead

	repo := open(t, dir)
	gated, refusal := Gate(context.Background(), repo, "main", []string{"feature"}, false, NewProber(repo))
	if refusal != nil {
		t.Fatalf("a source already contained in the target was refused: %v", refusal)
	}
	if len(gated) != 1 || gated[0]["action"] != ActionEmptyRange {
		t.Fatalf("gate report = %+v, want one entry with action %q", gated, ActionEmptyRange)
	}
}

func TestRefusal_IsError(t *testing.T) {
	var err error = &Refusal{message: "gate refused"}
	if err.Error() != "gate refused" {
		t.Errorf("Error() = %q, want the raw message", err.Error())
	}
}

func TestRefusal_Context(t *testing.T) {
	bare := (&Refusal{source: "feature"}).Context()
	if !reflect.DeepEqual(bare, map[string]any{"source": "feature"}) {
		t.Errorf("context without rewrites = %+v, want only the source", bare)
	}
	rewrites := []map[string]any{{"source": "alpha", "backup_ref": "refs/backup/alpha"}}
	withRewrites := (&Refusal{source: "beta", rewritten: rewrites}).Context()
	if !reflect.DeepEqual(withRewrites, map[string]any{"source": "beta", "rewritten": rewrites}) {
		t.Errorf("context with rewrites = %+v, want source and rewritten", withRewrites)
	}
}
