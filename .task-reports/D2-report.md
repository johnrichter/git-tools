# D2 — worktree remove cleanup-path fixes

Branch: `fix/worktree-remove-cleanup-path`, built from git-tools `main` tip `690947a3cea145cab2b72f39741e171a011b9cde` (not rebased).

## Authoritative source text (quoted verbatim)

### LED-146 (`workspace/.dat/ledger.md`)

> ## LED-146 — `worktree remove` strands the branch ref it checked out, and cannot resolve a landing target when the repo has no upstream
>
> - **Class:** open · **Raised by:** project-portfolio-reconciliation · workspace · 2026-08-15 · **Sensitivity:** personal
> - **Affects:** git-tools — `worktree remove` · **Impact 3 · Urgency 2 · Criticality 6**
>
> **What is wrong.** Two related gaps. First, `worktree remove` reports the checked-out branch in its result payload but never deletes the ref; combined with LED-145, which makes `branch delete` fail on every repo, any worktree-based task leaves a permanent branch ref behind — one session stranded four in a row. Second, in a repo whose remote is `[gone]`, `worktree remove` fails with `precondition_unmet.git.worktree_landing_unresolved`: its resolution order is `--landing-target`, else the branch upstream, else the local record of the remote default branch, and a gone remote satisfies none of them — but the error names the flag, not the cause, so the operator cannot tell that a missing upstream is why. Passing `--landing-target main` succeeds immediately once found by hand.
>
> **Proposed fix.** Offer `worktree remove` a flag to delete the branch when it is provably reachable from the landing target, or fix LED-145 so the follow-up delete works. Separately, when landing-target resolution fails, name which sources were tried and state explicitly that the repo has no upstream, or fall back to the local default branch when the remote record is absent.
>
> **Why it matters.** Branch-list noise is exactly what a fleet census re-litigates every sweep. And every worktree in a repo whose remote is gone hits the second gap, invisibly — it blocked this effort's own final cleanup step until the cause was worked out by hand.
>
> **Occurrences.** project-portfolio-reconciliation FB11 and FB12 (2026-08-15, workspace).

### fleet-03 FB5 (`marketplace/.dat/feedback-register.json`, `fleet-03-shared-templates` project)

> **id**: FB5 · **title**: git-tools can create a detached worktree but never remove one
>
> **feedback**: worktree add <path> <sha> creates a worktree with a detached HEAD, and it reports success. worktree remove then refuses that same worktree, because it cannot prove the work is reachable. The help text states that no flag overrides the refusal. The worktree is unreachable through the governed channel from that point.
>
> **proposed_solution**: Accept a detached HEAD when its commit is already reachable from the landing target. That proof is the same one the attached case uses. A second option is to refuse the detached form at worktree add time.
>
> **why_it_matters**: A session that creates a detached worktree cannot clean up after itself. The operator must finish the job by hand.

### fleet-03 FB8 (same register, same project)

> **id**: FB8 · **title**: The fleet-02 build left fifteen nested task worktrees registered
>
> **feedback**: Fleet-02 verification.json records fifteen nested task worktrees at `<repo>/.claude/worktrees/fleet-02-toolchain-pins/M1.P2.T<n>/`. Each one is untracked, not gitignored, and still registered in the parent repository's git worktree list. Every pin worktree except marketplace therefore reports a dirty git status, and a git add -A there would stage up to 476 files of a nested checkout. No committed file and no success criterion was affected.
>
> **proposed_solution**: Remove each per-task worktree when its task merges, inside the build loop, rather than leaving cleanup to the orchestrator at the end. A task worktree has no reader once its branch merges.
>
> **why_it_matters**: Fleet-03 needs eight clean worktrees under K6. Leftover registrations from a prior project make a dirty status the normal state, which hides a real one.

### LED-152 (`workspace/.dat/ledger.md`)

> ## LED-152 — The worktree gate denies `git worktree prune`, a metadata-only maintenance verb, ahead of the sanctioned resume sequence
>
> - **Class:** open · **Raised by:** harness-worktree-native · marketplace · 2026-08-18 · **Sensitivity:** public
> - **Affects:** governance-git — the worktree gate's read-safe allowlist · **Impact 2 · Urgency 2 · Criticality 4**
>
> **What is wrong.** Proving the resume criterion against the live enforcing gate: a bare `git worktree prune` run against the primary checkout, no other verbs on the line, is denied identically to a write verb, even though `prune` only edits `.git/worktrees` admin metadata and `git worktree list` — a pure read — is allowed unmodified. `git-tools` has no `prune` subcommand, so the resume step named in `build-with-team/SKILL.md` and this criterion's own acceptance text ("`git worktree prune` then `git worktree add ... --force` only if a stale registration survives the prune") cannot execute its first half as written under enforcement. The outcome still held: `git-tools worktree add <path> <branch> --force` alone, skipping prune, recreated a checkout removed out-of-band and recovered the full committed state — verified end to end — but every enforced resume now takes the `--force` branch, never the bare-add branch prune was meant to make the common case.
>
> **Proposed fix.** Either extend the worktree gate's read-safe allowlist to cover `git worktree prune` alongside `git worktree list`, or update `build-with-team/SKILL.md`'s resume step to call `git-tools worktree add ... --force` unconditionally, dropping the prune precondition the enforcing gate can never let it observe.
>
> **Why it matters.** The documented sequence and the enforced sequence diverge; a future reader auditing this criterion against the SKILL text alone would expect the bare add to succeed after a prune the gate silently prevents.
>
> **Occurrences.** harness-worktree-native FB10 (2026-08-18, marketplace).

## What was built, and why

### 1. LED-146 gap 1 — `--delete-branch` flag on `worktree remove`

`internal/worktreeclean.Options` gained a `DeleteBranch bool` field (standalone path only). `internal/worktreeclean.removeTarget` now, after a successful removal, deletes the target's checked-out branch via `repo.DeleteBranch(ctx, branch, entry.Head, false)` when `opts.DeleteBranch && entry.Branch != ""`. This reuses the existing reachability machinery rather than inventing a new one: `Cleanup`'s switch only reaches `removeTarget` once its own no-work-loss check has already proven `res.Unmerged == 0` for that branch (via `resolveLandingTarget` + `CountUnmerged`, the same functions `branch delete`'s own guard and `ReachableFrom` already share) — so the branch delete can never run against unreachable commits. The `Result` struct gained `DeletedBranch string`, surfaced on the CLI as `deleted_branch` in `cleanupData`. The flag name (`--delete-branch`) follows `merge`'s own `--cleanup` ("clean up after landing") naming convention, reading naturally alongside `worktree remove`'s existing `--landing-target`/`--dry-run` flags.

### 2. LED-146 gap 2 — name every landing-target source tried

New `landingUnresolvedMessage` composes the standalone path's `RefusalLandingUnresolved` message, naming every source `resolveLandingTarget` actually tried, in order (`--landing-target <value>` if given, `<branch>'s upstream`, `<remote>'s recorded default branch`), and states plainly `"this repository has no upstream configured"`. This applies only when `opts.MergedBranches == nil` (the standalone verb) — the `merge --cleanup` path's original, simpler message (`"cannot resolve a landing target for %s from local refs; pass --landing-target to name one"`) is deliberately left unchanged, since the ledger and the task scope this fix to the standalone verb only.

### 3. fleet-03 FB5 — remove a detached worktree

`subtreeState`'s target-entry case now always contributes a ref to check (previously it skipped a target with no branch, via the removed `RefusalDetachedHead` refusal, which unconditionally blocked any detached worktree from being removed). New `worktreeCommitRef(wt)` returns the worktree's branch, or its checked-out commit SHA when detached. This lets a detached target be judged by the exact same no-work-loss rule a branch is: its checked-out commit stands in for a branch name in `CountUnmerged`. Scope was deliberately kept narrow — only the target entry's own contribution changed; a nested (non-target) worktree's detached-HEAD handling in `subtreeState`'s `pathUnder` branch was left as-is, to avoid altering the `RefusalUnmergedWork`/`RefusalLiveSubWorktree` precedence for a hypothetical nested-and-detached sub-worktree, which is out of scope for FB5.

### 4. fleet-03 FB8 — confirmed, no separate fix built

Per instruction, no code was built for FB8. Honest finding, not just a rubber-stamp: FB8's actual described root cause — an orchestration/build-loop process gap (nobody called `worktree remove`/cleanup on per-task worktrees after each task's branch merged, so fifteen registrations survived a fleet build) — is a process gap in the *build loop*, not a defect in `worktree remove` itself. It does not literally share LED-146's root cause (branch-ref stranding after removal, and landing-target resolution failing on a gone remote). The ledger has no entry that explicitly ties FB8 to LED-146. The closer match by symptom is LED-101 ("a repo-wide file-walk check reads leftover nested worktrees as violations"), which is about a *different* mechanism (a post-merge gate walking into a stale nested worktree) than FB8's described symptom (dirty `git status` from unregistered nested checkouts). That said, this task's brief explicitly instructed "build nothing extra for FB8," which was followed — and LED-146's actual fixes above do at least remove one systemic reason a worktree might survive past its task's merge (a stranded branch ref no longer blocks cleanup, and the landing-target failure mode is now diagnosable), even though they do not address FB8's orchestration-loop cause directly.

### 5. LED-152 — admit `git worktree prune` to the gate's read-safe allowlist

`worktree-gate/detect/decide.go`'s `gitWorktreeReadSubcommands` map (consumed by `classifyGitSubSelect`, feeding `classifyGit`) now admits `"prune"` alongside the existing `"list"`. Confirmed read-safety by the same reasoning already applied to `list`: `git worktree prune` only drops a stale registration for a worktree directory that no longer exists, editing `.git/worktrees` admin metadata — never a tracked file inside any live worktree. This is a fully separate allowlist from the "SC15" sanctioned-CLI-provisioning verb machinery (`sc15VerbAllowed`/`sc15ReadVerb`), which correctly still denies `<git-tools binary> worktree prune`, since git-tools itself has no `prune` subcommand — that machinery was deliberately left untouched.

## Files touched

- `internal/worktreeclean/worktreeclean.go` — `Options.DeleteBranch`, `Result.DeletedBranch`, removed `RefusalDetachedHead`, `landingUnresolvedMessage`, `removeTarget` branch-delete, `subtreeState`/`worktreeCommitRef` for FB5.
- `internal/worktreeclean/worktreeclean_test.go` — removed the stale `RefusalDetachedHead` byte-identity/live-render cases; updated `RefusalLandingUnresolved`'s live-render `want` string to the new every-source-tried wording.
- `internal/cli/worktree.go` — `--delete-branch` flag, `Long`/`Example` text, `cleanupData`'s `deleted_branch`, removed the `RefusalDetachedHead` diagnostic-code mapping.
- `internal/cli/worktree_test.go` — removed `TestCleanupWorktree_DetachedHead_Refuses`; added `TestCleanupWorktree_DetachedHead_ReachableRemoves`, `TestCleanupWorktree_DetachedHead_UnreachableRefuses` (FB5 regression), `TestCleanupWorktree_DeleteBranch_ReachableDeletesAfterRemoval`, `TestCleanupWorktree_DeleteBranch_UnreachableNeverDeletesBranch` (LED-146 mandated regression — proves `--delete-branch` never deletes an unreachable branch), `TestCleanupWorktree_LandingUnresolved_NamesEveryTriedSourceAndNoUpstream`.
- `worktree-gate/detect/decide.go` — `gitWorktreeReadSubcommands` admits `"prune"`.
- `worktree-gate/detect/decide_test.go` — added `TestDecide_Bash_WorktreePruneInPrimaryCheckout_Allowed`.
- `worktree-gate/detect/sdet_git_verification_test.go` — `TestSDET_ClassifyGit_MigratedVerbsAndUnknownSubcommand`'s `{"worktree", "prune"}` case now expects `ClassRead` (was `ClassWrite`).
- `worktree-gate/detect/testdata/decide-bash-corpus.json` — the shared corpus case `git-worktree-prune-write-denied` (`want_deny: true`) renamed to `git-worktree-prune-read-allowed` (`want_deny: false`), consumed by `TestDecide_Bash_NamedPathAndSC15_Corpus`, `TestDecide_Bash_NamedPathOrdering_RedirectTargetPrecedesOperand`, and `TestDecide_Bash_EveryCorpusDenial_NamesARemedy` (the last one now simply skips this case, since it only checks denials).

Left deliberately untouched, and why: `internal/cli/branch.go` (the reachability pattern was reused via `worktreeclean`'s own exported helpers, not modified); `internal/cli/merge.go` (the `--cleanup` naming convention was read for reference; `merge --cleanup` never deletes branches, only worktrees, and stays that way); `worktree-gate/detect/verbs.json` (a generic non-git bash-prefix classifier, not the git-subcommand allowlist LED-152 targets); `worktree-gate/detect/decide_sc15_cleanup_test.go` and the `sc15Bin + " worktree prune"` case in `decide_test.go`'s `TestDecide_Bash_SC15ReadAllowance` (both test the separate SC15 CLI-provisioning allowlist, which correctly still denies `worktree prune` since git-tools has no such subcommand).

## Tests

Per-item tests, all added to `internal/cli/worktree_test.go` unless noted:

1. LED-146 gap 1: `TestCleanupWorktree_DeleteBranch_ReachableDeletesAfterRemoval` (positive) and `TestCleanupWorktree_DeleteBranch_UnreachableNeverDeletesBranch` (mandated regression: `--delete-branch` must never delete a branch not yet reachable from the landing target — asserts `Removed == false`, `DeletedBranch == ""`, and the branch ref still exists).
2. LED-146 gap 2: `TestCleanupWorktree_LandingUnresolved_NamesEveryTriedSourceAndNoUpstream` (asserts the refusal names `"feature's upstream"`, `"origin's recorded default branch"`, and `"no upstream configured"`).
3. FB5: `TestCleanupWorktree_DetachedHead_ReachableRemoves` (positive) and `TestCleanupWorktree_DetachedHead_UnreachableRefuses` (regression: a detached worktree ahead of the landing target must still refuse, and the worktree must stay on disk).
4. FB8: no test built, per instruction (no code fix).
5. LED-152: `TestDecide_Bash_WorktreePruneInPrimaryCheckout_Allowed` (`worktree-gate/detect/decide_test.go`), plus the corrected unit case in `sdet_git_verification_test.go` and the corrected corpus case in `decide-bash-corpus.json` (exercised by three existing corpus-driven test functions).

`worktreeclean_test.go`'s existing `TestRefusalStrings_ByteIdenticalToPreExtraction` / `TestRefusalStrings_LiveRenderMatchesTemplate` were updated (not newly authored) to drop the removed `RefusalDetachedHead` case and pin the new landing-unresolved wording, preserving their byte-identity-pinning intent for every refusal that didn't change.

### Commands run and outcomes

```
go build ./...                                # clean, no output
go vet ./...                                  # clean, no output
gofmt -l .                                    # clean, no output (nothing unformatted)
go test -timeout 30m -count=1 ./...           # all green, exit code 0
```

Full per-package result from the last run:

```
?   	github.com/johnrichter/git-tools/cmd/git-tools	[no test files]
ok  	github.com/johnrichter/git-tools/internal/cli	1101.816s
ok  	github.com/johnrichter/git-tools/internal/gitexec	106.376s
ok  	github.com/johnrichter/git-tools/internal/hooks	0.088s
ok  	github.com/johnrichter/git-tools/internal/result	0.005s
ok  	github.com/johnrichter/git-tools/internal/signing	17.506s
ok  	github.com/johnrichter/git-tools/internal/worktreeclean	35.516s
ok  	github.com/johnrichter/git-tools/worktree-gate/detect	6.403s
?   	github.com/johnrichter/git-tools/worktree-gate/detect/cmd/worktree-gate	[no test files]
ok  	github.com/johnrichter/git-tools/worktree-gate/fixtures	0.004s
ok  	github.com/johnrichter/git-tools/worktree-gate/lifecycle	147.098s
```

`internal/cli` took ~18.4 minutes (rebuilds the CLI per test, as expected); `worktree-gate/lifecycle` took ~2.5 minutes. Total wall time for the full suite was roughly 22-23 minutes.

## Commits

Two commits, split by package/concern rather than by ledger item, since LED-146's two gaps and FB5 are tightly coupled — same functions (`Cleanup`'s switch, `removeTarget`, `subtreeState`), same files, same test file — while LED-152 is a fully separate package (`worktree-gate/detect`) with no code overlap:

1. `626eb76` — "Let worktree remove delete its branch, name every landing-target source tried, and take back a detached worktree" (LED-146 gap 1, LED-146 gap 2, FB5). Files: `internal/worktreeclean/worktreeclean.go`, `internal/worktreeclean/worktreeclean_test.go`, `internal/cli/worktree.go`, `internal/cli/worktree_test.go`.
2. `46001a7` — "Admit `git worktree prune` to the worktree gate's read-safe allowlist" (LED-152). Files: `worktree-gate/detect/decide.go`, `worktree-gate/detect/decide_test.go`, `worktree-gate/detect/sdet_git_verification_test.go`, `worktree-gate/detect/testdata/decide-bash-corpus.json`.

Neither commit is signed, pushed, or merged. `HEAD` is `46001a7` on `fix/worktree-remove-cleanup-path`, two commits ahead of the `690947a` tip this task branched from.

## Open questions / risks for the orchestrator

1. **FB8 root-cause mismatch (finding, not a defect in this work).** As detailed above, FB8's own described cause (an orchestration/build-loop gap) is not literally LED-146's root cause, and the ledger never explicitly ties the two together. The instruction to build nothing extra for FB8 was followed regardless. If FB8 is meant to be formally closed on the strength of this task, the orchestrator should confirm that decision explicitly rather than infer it from LED-146's fix — the actual fix FB8 asked for (call cleanup inside the build loop when a task's branch merges) is a delivery-agent-team orchestration change, outside git-tools' own scope.
2. **`--delete-branch` is standalone-only by design.** `merge --cleanup` still only removes worktrees, never branches — this matches the task's explicit "standalone path only" framing and `merge`'s own existing behavior, but is worth flagging in case a later reviewer expects branch deletion to also be available from the merge path.
3. **Detached-worktree acceptance is scoped to the target entry only.** A nested (non-target) worktree that happens to be detached is not newly handled by this task — its behavior is unchanged from before. If a future ledger item asks for that case too, it is a separate, narrower follow-up in `subtreeState`.
