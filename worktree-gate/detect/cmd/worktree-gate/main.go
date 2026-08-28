// Command worktree-gate is the PreToolUse hook binary: it reads a hook
// payload from stdin and denies a repo-modifying Write, Edit, or Bash call
// made outside a git worktree. Enforcement is unconditional -- there is no
// environment opt-out.
//
// SC15's landing-verb allowance is the one carve-out, and its inputs arrive
// as ARGV, never from the environment: -provisioned-bin is the absolute path
// of the plugin-provisioned CLI, and -provisioned-digest is the expected
// sha256 of the binary at that path (the invoking wrapper selects the host's
// per-(os,arch) row and passes it in). With either flag absent the allowance
// is disabled and the call is judged on its own merits.
package main

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"time"

	"github.com/johnrichter/git-tools/internal/gitexec"
	"github.com/johnrichter/git-tools/worktree-gate/detect"
)

// gitIgnoreTimeout bounds the git work behind one SC23 lookup. A hook that
// never returns is worse than a slow deny: the invoking client eventually
// abandons the hook and loses its verdict entirely, so the bound turns a
// wedged git into an error, which gitignoreExempt reads as "not covered" and
// the gate resolves as its ordinary deny.
const gitIgnoreTimeout = 5 * time.Second

// gitIgnored adapts gitexec.IsIgnoredByCommittedGitignore (SC23) to
// detect.GitIgnoredFunc's absolute-path contract, the one place this binary
// shells out to git at all -- worktree-gate/detect itself stays hermetic.
func gitIgnored(repoRoot, absPath string) (bool, error) {
	rel, err := filepath.Rel(repoRoot, absPath)
	if err != nil {
		return false, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), gitIgnoreTimeout)
	defer cancel()
	return gitexec.IsIgnoredByCommittedGitignore(ctx, repoRoot, rel)
}

func main() {
	provisionedBin := flag.String("provisioned-bin", "", "absolute path of the plugin-provisioned CLI whose landing verbs SC15 allows")
	provisionedDigest := flag.String("provisioned-digest", "", "expected sha256 of the binary at -provisioned-bin (the host's per-(os,arch) row)")
	flag.Parse()

	os.Exit(detect.RunGate(os.Stdin, os.Stdout, os.Stderr, os.Lstat, os.ReadFile, gitIgnored, os.Getenv, *provisionedBin, *provisionedDigest))
}
