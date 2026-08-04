// Command worktree-gate is the PreToolUse hook binary: it reads a hook
// payload from stdin and denies a repo-modifying Write, Edit, or Bash call
// made outside a git worktree. Enforcement is unconditional -- there is no
// environment opt-out.
package main

import (
	"os"

	"github.com/johnrichter/git-tools/worktree-gate/detect"
)

func main() {
	os.Exit(detect.Run(os.Stdin, os.Stdout, os.Stderr, os.Lstat, os.ReadFile, os.Getenv))
}
