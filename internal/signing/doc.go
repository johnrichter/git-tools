// Package signing is the merge landing channel's signing gate, and it covers
// every commit a merge would produce: the incoming commits each source
// carries, and the merge commit itself when the merge mints one. Gate
// re-signs, or refuses, every source's incoming commits before the merge
// runs; WillMintCommit and the shared Prober let the caller settle, up front,
// whether the merge will mint a commit and whether git can sign it. Used
// together — as the merge verb uses them — the two halves mean a merge never
// lands an unsigned commit, incoming or minted, and commit.gpgsign is no
// longer the dependency: the verb proves signing is available and asks for it
// explicitly.
//
// Inputs: an open git.Repo, the branch being merged into, the source branches,
// and whether the merge is a dry run. Outputs: a per-source signing_gate
// report (each entry naming the source and the Action taken), or a *Refusal
// that stops the merge.
//
// Invariants:
//   - The gate never moves a ref on a dry run, and never lands a source it
//     could not sign.
//   - Sources are gated independently and in order; a late refusal reports the
//     rewrites that already landed on earlier sources rather than unwinding
//     them.
//   - Refusal messages are raw (unsanitized) at this package boundary; the
//     caller that emits a Refusal as a diagnostic sanitizes it there, exactly
//     once.
//   - This package depends only on gitexec and the shared git/clikit
//     libraries — never on CLI or worktree-cleanup code — so both a clikit
//     consumer and a plain-error consumer can call it.
package signing
