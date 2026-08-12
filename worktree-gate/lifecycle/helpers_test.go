package lifecycle

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newScratchRepo creates a throwaway git repository inside its own
// directory under t.TempDir(), so WorktreesDir's sibling (.worktrees)
// lands next to it but still fully inside the test's isolated temp tree.
func newScratchRepo(t *testing.T) string {
	t.Helper()
	base := t.TempDir()
	dir := filepath.Join(base, "repo")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("mkdir repo dir: %v", err)
	}
	runGitT(t, dir, "init", "-q", "-b", "main")
	runGitT(t, dir, "config", "user.name", "Test User")
	runGitT(t, dir, "config", "user.email", "test@example.com")
	runGitT(t, dir, "config", "commit.gpgsign", "false")
	writeFileT(t, dir, "README.md", "seed\n")
	runGitT(t, dir, "add", "-A")
	runGitT(t, dir, "commit", "-q", "-m", "seed")
	return dir
}

func runGitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s (in %s): %v\n%s", strings.Join(args, " "), dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeFileT(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// commitFileT writes name in dir and commits it there, for a test that
// needs a real commit (something Complete's merge step can land) rather
// than an untracked or modified working-tree change (something its
// worktreeclean-backed dirty check refuses on).
func commitFileT(t *testing.T, dir, name, content string) {
	t.Helper()
	writeFileT(t, dir, name, content)
	runGitT(t, dir, "add", "-A")
	runGitT(t, dir, "commit", "-q", "-m", "commit "+name)
}
