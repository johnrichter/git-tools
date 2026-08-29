# D2 — quality review

Branch `fix/worktree-remove-cleanup-path`, worktree
`/home/bits/Development/workspaces/psa-platform/git-tools/.claude/worktrees/d2-cleanup-path`.
Reviewed commits `626eb76`, `46001a7` against `.task-reports/D2-report.md` (implementer) and
`.task-reports/D2-test-verification.md` (test engineer, `1fc0643`), plus the real diff.

**Verdict: FIX-APPLIED (accept with fixes).** Four of the five items hold as claimed. LED-146
gap 2 shipped a refusal message that states two falsehoods on the `--landing-target` path, and
FB5's newly-reachable detached path resolved its landing target from an unrelated ref. Both are
fixed here, with regression tests; both were reachable from ordinary operator input and neither
was covered by the authored suite.

## What I verified independently (not taken from the prior reports)

| Claim | How I checked | Result |
|---|---|---|
| LED-152 differential execution | Built the gate binary from `git archive` of `690947a` and `46001a7` into `/home/bits/.cache/d2-review`, fed both identical PreToolUse payloads for a 40-case corpus of my own | Confirmed: 4 flips, all `prune` forms; 36 held |
| LED-152 admits nothing broader | Read `classifyGitSubSelect` + `firstOperand`; ran near-miss, chained, other-family cases live | Confirmed: exact lowercased map hit, no prefix/substring match |
| LED-146 gap 1 reachability | Read `Cleanup`'s switch ordering, `resolveLandingTarget`, `CountUnmerged`, and `git.Repo.DeleteBranch` (module cache, `go/git@v0.3.0`) | Confirmed sound, see below |
| LED-146 gap 2 message | Live probe of `Options{LandingTarget: "mian"}` against a real scratch repo | **Defect found** |
| FB5 detached handling | Read `subtreeState`/`worktreeCommitRef`; live probe of a detached target with no `--landing-target` | **Defect found** |
| FB8 no-shared-root-cause | Read `workspace/.dat/ledger.md` LED-146 (line 2571) and LED-152 (line 2661), and `marketplace/.dat/feedback-register.json` `projects[3]` (`fleet-03-shared-templates`) entries FB5 (`entries[4]`) and FB8 (`entries[7]`) | Confirmed, quotes verbatim |
| Full suite | Re-ran `go test -timeout 40m -count=1 ./...` with `TMPDIR` on a filesystem with free space | See Re-verification |

### LED-152 differential result (my own run, 40 cases)

Flipped (deny -> allow), all intended: `git worktree prune`, `... --expire=now`, `... -n`,
`git worktree PRUNE`.

Held identical on both revisions: `pruned`, `prunemore`, `prune-all`, `pru`, `--prune`,
`add prune`, bare `worktree`; `add`/`remove`/`move`/`lock`/`unlock`/`repair`; `git prune`,
`git prune --expire=now`, `git reflog prune`, `git reflog expire --all`, `git reflog show`,
`git gc --prune=now`, `git remote prune origin`, `git fetch --prune`;
`prune; rm -rf ...`, `prune && git add -A`, `prune | tee ...`, `prune > ...`,
`prune $(git add -A)`, `git add -A && ... prune`; `git -C <primary-checkout> worktree add`,
`git -C /tmp worktree add`, `git -C /tmp commit`; `git status`, `git log`, `worktree list`,
`git commit`, `git add`, `git reset --hard`, `git push`.

The test engineer's table is accurate. The `prune` widening cannot leak: `classifyGitSubSelect`
does `read[strings.ToLower(firstOperand(rest))]` — an exact map hit on the first non-flag token
(`worktree-gate/detect/decide.go:1028`), and the map is consulted only from the `"worktree"` arm
of `classifyGit`, so no other verb family inherits it.

No coverage was narrowed to reach green: the corpus case kept its command and flipped only
`want_deny`, and `TestDecide_Bash_EveryCorpusDenial_NamesARemedy` was not touched — it filters
`!c.WantDeny` and always did (`decide_test.go:172`).

## Findings

### Major (fixed here)

**M1 — `internal/worktreeclean/worktreeclean.go:195` `landingUnresolvedMessage` claimed sources
it never tried, and blamed a cause that may be false.**
`resolveLandingTarget` short-circuits on an explicit landing target
(`worktreeclean.go:266-269`: `if opts.LandingTarget != "" { ...; return }`) — the branch upstream
and the remote-default record are never reached. The shipped message named them anyway. Live
proof against a real repo, `Options{LandingTarget: "mian"}` (a typo, upstream irrelevant):

```
cannot resolve a landing target for feature: tried --landing-target mian, feature's upstream,
origin's recorded default branch, and none resolved -- this repository has no upstream
configured; pass --landing-target to name one
```

Three untruths in one line: it names two sources not tried; it asserts "no upstream configured"
in a repo that may have a perfectly good upstream; and it tells the operator to pass
`--landing-target` when they just did. LED-146 gap 2 exists precisely to stop this class of
misdirection ("name which sources were tried"), so shipping it inverted on the flag path is a
defect against the item's own acceptance, not a nitpick. The old, vaguer wording was at least
not false.

**M2 — `internal/worktreeclean/worktreeclean.go:270` a detached target resolved its landing
target from a bare `@{upstream}`.**
With `entry.Branch == ""`, `shortRef(entry.Branch)` is `""`, so the lookup was
`RevParseLocal(repo.Dir, "@{upstream}")` — git's shorthand for *the upstream of whatever branch
the primary checkout has out*, a ref with no relation to the worktree being removed. Before FB5
this was harmless: `RefusalDetachedHead` won `Cleanup`'s switch, so the resolved value was
discarded. FB5 removed that guard and made it load-bearing. Live proof (scratch repo, `main`
tracking `origin/main`, no `refs/remotes/origin/HEAD`, so only a bare `@{upstream}` could
resolve): the detached worktree removed cleanly, `kind=0 removed=true`.

Why it matters more for a detached target than an attached one: a detached worktree's checked-out
commit has no ref anywhere else. Removing the worktree drops its only pointer. Measuring that
commit against a silently-chosen, undocumented ref — the primary checkout could be sitting on any
feature branch — is the false-positive shape this review was asked to rule out. It also
contradicts the verb's own help text ("the landing target is `--landing-target` if given, else
*the branch's* upstream, else the local record of the remote's default branch") and it made M1's
"tried" list wrong in the other direction, omitting a source that really was tried.

Not a work-loss hole in the common case (reachability is still proven against a real ref, and
the reachable-detached path FB5 asked for is unaffected), which is why this is major and not
blocking.

### Minor (reported, not fixed — out of this task's acceptance)

- `worktreeclean.go:227-236` — when `RemoveWorktree` succeeds and `DeleteBranch` then fails,
  `removeTarget` returns `nil, err`, discarding `res.Removed == true`. The operator learns the
  branch delete failed but not that the worktree is already gone. Reachable only under a
  concurrent ref move (the CAS in `DeleteBranch` fails closed, which is correct); worth a
  follow-up that names both halves of the outcome.
- `internal/cli/worktree.go:208-210` — `deleted_branch` is asserted at the `Result` level but no
  test pins it in the rendered CLI data map. Small gap; `cleanupData` is trivial.
- `.task-reports/D2-report.md` is untracked in this worktree (only the test-verification report
  was committed, at `1fc0643`). Beyond provenance, it is a live tripwire for the very verb under
  review: an untracked file makes `worktree remove` refuse this worktree with `RefusalDirtyTree`.
  The orchestrator should commit it. I did not commit another role's report for them.

### Confirmed sound (no action)

- **LED-146 gap 1 safety.** `Cleanup`'s switch reaches `removeTarget` only when `landingOK` is
  true and `res.Unmerged == 0`, and `res.Unmerged` is `CountUnmerged(subtreeBranches,
  landing.sha)` over the target's own branch against the *resolved landing ref* — not "any ref".
  `DeleteBranch(ctx, branch, entry.Head, false)` is a compare-and-swap against the head captured
  in the same `WorktreeList` read the reachability proof used, and it writes a backup tag before
  `update-ref -d` (`go/git@v0.3.0/branch.go:40-64`). If the branch moved after the proof, the CAS
  refuses and the branch survives. Using the pre-removal head is the right choice: it binds the
  delete to the state that was proven.
- **Idiom.** `worktree-gate/lifecycle/complete.go:174-184` already does remove-then-delete with
  the identical CAS pattern and a `KeepBranch` opt-out, so `--delete-branch` follows an
  established in-repo convention rather than inventing one. Opt-in default (`false`) is right for
  a destructive addition.
- **FB5 cannot remove the wrong worktree.** Target identification is unchanged (resolved-path
  equality); `worktreeCommitRef` changes only *which ref's reachability is measured*, never which
  path `removeTarget` removes; `DeleteBranch` is gated on `entry.Branch != ""`, so a detached
  target never deletes a branch.
- **FB8.** The register entry (`fleet-03-shared-templates`, `entries[7]`, 2026-08-22) describes
  fifteen leftover per-task worktree registrations from a build loop that never called cleanup
  after each task merged, and proposes fixing the build loop. LED-146 is branch-ref stranding on
  removal plus gone-remote landing-target failure. No shared mechanism, and no ledger entry ties
  them. The implementer's and test engineer's claim holds against primary sources. FB8's real fix
  is a delivery-agent-team orchestration change, outside git-tools.
- **No leak, no hardcoded environment value.** Every added line across both commits and my fix
  was scanned for org/host/user/email/IP tokens: none.
- **`RefusalDetachedHead` removal is fully propagated.** No dangling reference in git-tools or in
  marketplace's plugins. Removing it from the `iota` block renumbers later `RefusalKind` values,
  which is safe here: the kind never crosses a process boundary as an integer — `cleanupRefusalCode`
  maps it to a stable string code.

## Fixes applied

One commit, `internal/worktreeclean/worktreeclean.go` + `internal/worktreeclean/worktreeclean_test.go`.

1. `landingUnresolvedMessage` now names only what was actually tried. With an explicit
   `--landing-target`, the message blames the named ref and nothing else:
   `cannot resolve a landing target for <label>: --landing-target <ref> does not resolve from
   local refs; name a ref this repository already has`. With no flag, the every-source-tried
   wording LED-146 gap 2 asked for is unchanged, so the implementer's own
   `TestCleanupWorktree_LandingUnresolved_NamesEveryTriedSourceAndNoUpstream` and the
   `TestRefusalStrings_LiveRenderMatchesTemplate/RefusalLandingUnresolved` byte-identity pin both
   still pass untouched.
2. `resolveLandingTarget` tries the branch upstream only when there is a branch. A detached
   target now falls straight to the remote's recorded default branch — the documented order — and
   refuses accurately when that is absent. FB5's reachable-detached removal is unaffected: every
   FB5 test passes an explicit `--landing-target`.
3. Two regression tests in `internal/worktreeclean/worktreeclean_test.go`:
   `TestLandingUnresolved_NamedTargetBlamesTheRefNotAMissingUpstream` and
   `TestDetachedTarget_NoLandingTargetIgnoresThePrimaryCheckoutsUpstream`. The second asserts its
   own fixture precondition (a bare `@{upstream}` *does* resolve there) so it cannot pass
   vacuously if a future fixture change stops configuring the upstream.

Placed in `internal/worktreeclean` rather than `internal/cli`: the refusal-wording pins already
live there, and that package runs in ~40s against real scratch repos versus ~18min for
`internal/cli`.

## Re-verification

```
gofmt -l .                                     clean
go build ./...                                 clean
go vet ./...                                   clean
go test -count=1 ./internal/worktreeclean/     ok  41.4s
go test -count=1 -run 'TestCleanupWorktree_(LandingUnresolved|DetachedHead|DeleteBranch)' \
        ./internal/cli/                        ok  23.5s (6/6 pass)
go test -timeout 40m -count=1 ./...            see below
```

Full suite, exit code 0, every package green on the first attempt — no flake, no rerun needed:

```
?   cmd/git-tools              [no test files]
ok  internal/cli               1106.928s
ok  internal/gitexec            106.630s
ok  internal/hooks                0.109s
ok  internal/result               0.004s
ok  internal/signing             17.473s
ok  internal/worktreeclean       41.590s
ok  worktree-gate/detect          6.443s
?   worktree-gate/detect/cmd/worktree-gate  [no test files]
ok  worktree-gate/fixtures        0.003s
ok  worktree-gate/lifecycle     148.338s
```

Note on the test engineer's reported flake: `/tmp` is still at 99% (364M free on a 31G tmpfs) at
review time, which corroborates their environmental diagnosis. I ran with
`TMPDIR=/home/bits/.cache/d2-review/tmp` (739G free) rather than re-rolling the dice, which
converts their two-rerun argument into a positive control.

## Test-suite assessment

Adequate for items 1, 3, and 5, thin on item 2.

- Items 1 and 3 each have a positive and an unreachable-work negative, and the negatives assert
  the right three things (refusal kind, ref/worktree survival, `DeletedBranch == ""`).
- Item 5 has a unit case, a live differential run, and the corpus case, with the near-miss
  `worktree lock` case retained.
- Item 2 had exactly one case — the no-flag, no-remote path. The `--landing-target`-given path is
  the other half of the same branch in the same function and was both untested and wrong. Closed
  here. The general gap the test engineer should carry forward: when a fix changes a *message
  composed from branching state*, test every branch of that composition, not just the branch the
  ledger's own reproduction happened to describe.
- The test engineer wrote no new files, judging existing coverage sufficient. That judgment was
  right about items 1, 3, and 5 and wrong about item 2; the live-binary runs reproduced the
  ledger's scenario faithfully but not the adjacent input the same code path accepts.

## Residual risk

- `worktreeCommitRef` returns `""` when a target entry has neither a branch nor a `HEAD` line —
  in practice only a *bare* repository's main worktree. `CountUnmerged` would then run
  `rev-list --count <sha>..`, which git reads as `<sha>..HEAD`, a degenerate comparison against
  the primary checkout. Not exploitable: the only entry that can produce it is the main worktree,
  and git refuses to remove a main worktree, so `RemoveWorktree` errors out. Left alone
  deliberately — a guard would need a new refusal kind, diagnostic code, and byte-identity entry
  for an unreachable case.
- On the merge path a detached target would now render `RefusalBranchNotMerged` with an empty
  branch name ("the worktree is on , which is not among..."). Unreachable through the CLI:
  `cleanupMergedWorktrees` skips `wt.Branch == ""` before calling `Cleanup`
  (`internal/cli/merge.go:330`). Cosmetic, and fixing it would require editing text the
  byte-identity test pins on purpose.
- `git worktree prune` is now allowed against a primary checkout, as LED-152 asked. It is
  metadata-only, but it is not a pure read: it can drop a registration for a worktree whose
  directory is temporarily unavailable (an unmounted volume, say), which the operator then has to
  re-add with `--force`. Accepted as the ledger's own chosen remedy.

## Plan feedback

- LED-152's remedy is now implemented in the gate. The other half of the ledger entry —
  `build-with-team/SKILL.md`'s resume step — is unchanged and now correct as written; no edit
  needed, but whoever closes LED-152 should record that the gate arm was taken, not the SKILL arm.
- FB8 should not be closed on D2's strength. Confirmed independently: its cause is a
  delivery-agent-team build-loop gap (call cleanup when a task's branch merges), not anything in
  `worktree remove`. It needs its own task in the orchestration layer. The implementer flagged
  this correctly; it deserves an explicit orchestrator decision rather than an inherited close.
- Process, for the whole plan: the two defects found here are both *newly reachable* code rather
  than newly written code — a guard was removed (FB5) and made a pre-existing, dormant fallback
  load-bearing. Neither the implementer's nor the test engineer's method looks at that class. When
  a task deletes a refusal or an early return, the diff review should ask what downstream code
  now runs for the first time, and the suite should cover it.
