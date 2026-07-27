// Command git-tools composes the shared git, githooks, fsx, sysops and
// clikit libraries into one CLI: signing/rewriting operations, worktree and
// branch management, merge/rebase, content-guardrail scans, and installable
// git hooks that invoke those same scans.
package main

import (
	"os"

	"github.com/johnrichter/git-tools/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
