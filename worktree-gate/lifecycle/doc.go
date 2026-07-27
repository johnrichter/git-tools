// Package lifecycle owns the worktree-isolation gate's write side: getting
// an isolated worktree in place before a gated write, and reclaiming it
// afterward.
//
// Ensure creates a repo-adjacent linked worktree off the repository's
// current HEAD the first time an agent-or-task id is seen, so the gate in
// package detect never has to deny a write for want of a place to make it.
// Complete folds that worktree's work back into its base branch and removes
// the checkout once a task finishes. Reap is the janitor for the case
// neither of those covers: a worktree nobody ever completed, either because
// the task that owned it was abandoned or because the calling process
// crashed mid-lifecycle. It only removes a worktree that is both idle past
// an age threshold and free of uncommitted changes, or one git no longer
// recognizes as a worktree at all (an orphaned directory) — it never
// removes a worktree carrying real, unrecovered work.
//
// Every mutating operation in this package serializes through one advisory
// file lock per repository (see lock.go), so a concurrent create, complete,
// and reap of the same repository's worktrees never interleave badly.
package lifecycle
