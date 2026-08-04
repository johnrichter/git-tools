// Package detect implements the worktree-isolation PreToolUse gate: it
// decides, for one Write, Edit, or Bash tool call, whether the call would
// modify a file inside a git repository from outside a linked worktree.
//
// Write and Edit carry an explicit file path, so repo and worktree
// membership resolve deterministically from the filesystem alone. Bash
// carries only a command string and a working directory, so whether it
// modifies a repo file is undecidable in general; ClassifyBash resolves
// that with a conservative, data-driven over-approximation biased toward
// treating an unrecognized command as a possible write.
//
// A call this package cannot resolve confidently is denied (fail closed):
// an unblocked write outside a worktree can destroy committed work with no
// way to reconstruct it, so the safe default is to refuse rather than
// guess. A missing or corrupt embedded classifier artifact denies too,
// since it could be masking a real write. The one exception is a call
// already resolved independently of the artifact (e.g. confirmed inside a
// worktree): there the defect is only surfaced as a loud diagnostic
// (Decision.Degraded), never as a reason to change the verdict.
package detect
