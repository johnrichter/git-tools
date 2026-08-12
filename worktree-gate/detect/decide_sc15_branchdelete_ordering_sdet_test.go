package detect

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSDET_SC15_BranchDelete_GateSanctionsButGuardStillRefuses builds the
// real git-tools binary shipped by this repo, re-hashes it exactly as
// sc15Identity does, and confirms two things about that one binary:
//
//  1. Decide() SANCTIONS `branch delete` run by that binary's absolute
//     provisioned path from a primary checkout.
//  2. Actually running that binary's `branch delete` against an unmerged
//     branch still REFUSES (precondition_unmet, exit 30) and leaves the
//     ref unmoved -- the gate's sanction opens no path around the CLI's
//     own no-work-loss guard, which runs unconditionally regardless of who
//     is permitted to invoke it.
func TestSDET_SC15_BranchDelete_GateSanctionsButGuardStillRefuses(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "git-tools")
	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	build := exec.Command("go", "build", "-o", bin, "./cmd/git-tools")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build git-tools: %v\n%s", err, out)
	}

	binBytes, err := os.ReadFile(bin)
	if err != nil {
		t.Fatalf("read built binary: %v", err)
	}
	sum := sha256.Sum256(binBytes)
	digest := hex.EncodeToString(sum[:])

	// Scratch repo with an unmerged branch: main has a commit feature
	// doesn't, so deleting feature from main must be refused.
	dir := t.TempDir()
	runGitCmd(t, dir, "init", "-q", "-b", "main")
	runGitCmd(t, dir, "config", "user.name", "Test User")
	runGitCmd(t, dir, "config", "user.email", "test@example.com")
	runGitCmd(t, dir, "config", "commit.gpgsign", "false")
	writeAndCommit(t, dir, "base.txt", "base\n", "base")
	runGitCmd(t, dir, "branch", "feature")
	runGitCmd(t, dir, "checkout", "-q", "feature")
	writeAndCommit(t, dir, "feature.txt", "feature\n", "feature-only")
	runGitCmd(t, dir, "checkout", "-q", "main")
	featureHead := strings.TrimSpace(runGitCmd(t, dir, "rev-parse", "feature"))

	// The gate side: Decide() must sanction this exact call from a
	// primary checkout at dir.
	fs := newFakeFS().dir(dir+"/.git").file(bin, string(binBytes))
	v := testVerbs(t)
	cmd := bin + " branch delete feature " + featureHead
	d := Decide(fs.lstat, fs.readFile, v, nil, TrackingDocs{}, nil, Input{
		ToolName: "Bash", CWD: dir, Command: cmd,
		ProvisionedBinPath: bin, ProvisionedBinDigest: digest,
	})
	if d.Deny {
		t.Fatalf("gate must SANCTION %q from a primary checkout, got deny: %s", cmd, d.Reason)
	}

	// The guard side: actually running the sanctioned binary against the
	// unmerged branch, with an explicit landing target so the refusal
	// under test is the unmerged-commits guard and not an unresolved
	// landing target, must still refuse and leave the ref unmoved.
	run := exec.Command(bin, "branch", "delete", "feature", featureHead, "--landing-target", "main")
	run.Dir = dir
	out, _ := run.Output()
	exit := run.ProcessState.ExitCode()
	var r struct {
		Status string `json:"status"`
		Errors []struct {
			Code string `json:"code"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatalf("branch delete output is not valid JSON: %v\nraw: %s", err, out)
	}
	wantCode := "precondition_unmet.git.branch_unmerged_work"
	if exit != 30 || len(r.Errors) == 0 || r.Errors[0].Code != wantCode {
		t.Fatalf("branch delete of an unmerged branch must refuse with %s (exit 30); got exit=%d raw=%s", wantCode, exit, out)
	}
	if got := strings.TrimSpace(runGitCmd(t, dir, "rev-parse", "feature")); got != featureHead {
		t.Fatalf("guard refusal must leave feature's ref unmoved: before=%s after=%s", featureHead, got)
	}
}

func runGitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

func writeAndCommit(t *testing.T, dir, name, content, message string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitCmd(t, dir, "add", name)
	runGitCmd(t, dir, "commit", "-q", "-m", message)
}
