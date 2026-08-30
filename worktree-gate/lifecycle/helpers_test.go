package lifecycle

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// signableScratchRepo is newScratchRepo plus a working ssh commit-signing
// setup, so commits made after it returns are signed and verify. It mirrors
// internal/cli's own signingRepo fixture: an ed25519 key pair generated
// under the test's tempdir, never leaving it, with an allowed-signers file
// so git reports the resulting signatures as verifying (%G? = G) rather than
// merely present.
func signableScratchRepo(t *testing.T) string {
	t.Helper()
	dir := newScratchRepo(t)
	configureSigningKeyT(t, dir)
	runGitT(t, dir, "config", "commit.gpgsign", "true")
	return dir
}

// configureSigningKeyT gives dir a resolvable ssh signing key without turning
// commit.gpgsign on, mirroring internal/cli's configureSigningKey. A fixture
// whose own commits should be signed uses signableScratchRepo; one whose
// commits must stay unsigned -- the state that makes Complete's signing gate
// re-sign the source branch before landing it -- layers this over
// newScratchRepo instead.
func configureSigningKeyT(t *testing.T, dir string) {
	t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Fatalf("ssh-keygen is required to sign this fixture's commits: %v", err)
	}
	keyDir := t.TempDir()
	key := filepath.Join(keyDir, "signing-key")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "lifecycle signing fixture", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	pub, err := os.ReadFile(key + ".pub")
	if err != nil {
		t.Fatal(err)
	}
	allowed := filepath.Join(keyDir, "allowed_signers")
	if err := os.WriteFile(allowed, []byte(`test@example.com namespaces="git" `+string(pub)), 0o600); err != nil {
		t.Fatal(err)
	}
	runGitT(t, dir, "config", "gpg.format", "ssh")
	runGitT(t, dir, "config", "user.signingkey", key+".pub")
	runGitT(t, dir, "config", "gpg.ssh.allowedSignersFile", allowed)
}

// breakSigningKeyT points user.signingkey at a key that does not exist, so
// dir's repository can no longer produce a new signature. Being
// repository-local it overrides whatever the host's git config supplies,
// giving a test a deterministic "no key resolves" repository regardless of
// the host's own signing setup.
func breakSigningKeyT(t *testing.T, dir string) {
	t.Helper()
	runGitT(t, dir, "config", "gpg.format", "ssh")
	runGitT(t, dir, "config", "user.signingkey", filepath.Join(t.TempDir(), "absent-key.pub"))
}

// newScratchRepo creates a throwaway git repository inside its own
// directory under t.TempDir(), so WorktreesDir's sibling (.worktrees)
// lands next to it but still fully inside the test's isolated temp tree.
func newScratchRepo(t *testing.T) string {
	t.Helper()
	// Isolate from the host's global/system git config, most importantly
	// core.hooksPath -- without this, every commit below runs whatever hook
	// the host has configured globally.
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
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
