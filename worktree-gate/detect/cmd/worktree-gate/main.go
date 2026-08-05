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
	"flag"
	"os"

	"github.com/johnrichter/git-tools/worktree-gate/detect"
)

func main() {
	provisionedBin := flag.String("provisioned-bin", "", "absolute path of the plugin-provisioned CLI whose landing verbs SC15 allows")
	provisionedDigest := flag.String("provisioned-digest", "", "expected sha256 of the binary at -provisioned-bin (the host's per-(os,arch) row)")
	flag.Parse()

	os.Exit(detect.RunGate(os.Stdin, os.Stdout, os.Stderr, os.Lstat, os.ReadFile, os.Getenv, *provisionedBin, *provisionedDigest))
}
