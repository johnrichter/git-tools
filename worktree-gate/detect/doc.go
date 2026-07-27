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
// guess. The one exception is the package's own classifier data: a missing
// or corrupt embedded artifact is a packaging defect, not a signal, and
// fails open with a loud diagnostic instead of denying on it.
package detect
