// Command worktree-gate is the PreToolUse hook binary: it reads a hook
// payload from stdin and denies a repo-modifying Write, Edit, or Bash call
// made outside a git worktree.
//
// Enforcement is on by default; the rollout package's flag
// (GIT_TOOLS_WORKTREE_GATE_ENFORCE=0) is the one opt-out. Opted out, this
// binary only observes what it would have denied.
package main

import (
	"os"

	"github.com/johnrichter/git-tools/worktree-gate/rollout"
)

func main() {
	status := rollout.Resolve(os.Getenv)
	os.Exit(rollout.Run(status, os.Stdin, os.Stdout, os.Stderr, os.Lstat, os.ReadFile, os.Getenv))
}
