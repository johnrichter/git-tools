# D2 — test verification

Branch `fix/worktree-remove-cleanup-path`, commits `626eb76`, `46001a7`, worktree
`/home/bits/Development/workspaces/psa-platform/git-tools/.claude/worktrees/d2-cleanup-path`.
Verified against `.task-reports/D2-report.md` and the real diff
(`git diff 690947a..46001a7`), not against the report's prose alone.

## Deliverable type

Code (Go CLI + fail-closed classifier). Verification form: full suite run,
live adversarial exercise of the binary against real scratch git repos, and a
live cross-revision differential run of the classifier binary built at both
the parent commit and the branch tip.

## What was tested

1. Full existing + already-authored suite: `go test -timeout 30m -count=1 ./...`.
2. Live binary exercise (`go build ./cmd/git-tools`, then real `exec.Command`
   calls against throwaway git repos under `/tmp`, run from inside small Go
   harness programs via `go run` — not through the Bash tool's own git
   commands, since this machine's own worktree/branch-protection governance
   hooks intercept raw `git`/`cd&&git` text on the Bash tool and refuse to
   operate on an ad hoc scratch repo; shelling out from inside a Go process is
   the equivalent of what the existing unit tests already do and is not
   subject to that interception):
   - LED-146 gap 1, both directions (unreachable branch refused +
     survives; reachable branch deleted after removal).
   - LED-146 gap 2 (gone/absent-upstream error message, real repo with no
     remote at all).
   - fleet-03 FB5, both directions (reachable detached worktree removed;
     unreachable detached worktree refused and left on disk).
3. Live cross-revision differential run for LED-152: built the
   `worktree-gate` hook binary from `git archive` snapshots of both the
   parent commit (`690947a`) and the branch tip (`46001a7`), fed both binaries
   the same PreToolUse JSON payloads for a hand-picked command set, cwd =
   this repo's real primary checkout (a directory the enforcing classifier
   itself treats as "outside a worktree").
4. FB8 independent source check: read `workspace/.dat/ledger.md`'s LED-146
   and LED-101 entries and `marketplace/.dat/feedback-register.json`'s FB8
   entry directly (not the report's quotes of them).

## Acceptance — item by item

**1. LED-146 gap 1 (`--delete-branch`).** PASS.
Diff (`internal/worktreeclean/worktreeclean.go`, `removeTarget`) matches the
report: branch delete only runs after `Cleanup`'s existing no-work-loss
switch already reached `removeTarget`, using the same `entry.Branch`/`entry.Head`
values proven reachable; no second reachability check is invented, none is
needed.
Live proof, unreachable branch (real repo, real unmerged commit on the
worktree's branch):
```
$ git-tools worktree remove <wt> --landing-target main --delete-branch
exit=30, precondition_unmet.git.worktree_unmerged_work,
"1 commit(s) on feature1 are not reachable from main and would be lost"
$ git branch --list feature1   # still present: "+ feature1"
$ git worktree list            # wt1 still registered
```
Live proof, reachable branch:
```
$ git-tools worktree remove <wt> --landing-target main --delete-branch
exit=0, deleted_branch:"feature2", removed:true
$ git branch --list feature2   # empty: branch is gone
```
Existing regression tests (`internal/cli/worktree_test.go`
`TestCleanupWorktree_DeleteBranch_UnreachableNeverDeletesBranch`,
`...ReachableDeletesAfterRemoval`) assert the same two outcomes at the unit
level; the live run above proves the same behavior end to end through the
built binary, not just against the package's exported Go API.

**2. LED-146 gap 2 (landing-unresolved message).** PASS.
Live proof, real repo with no remote configured at all, no `--landing-target`,
branch with no upstream:
```
message: "cannot resolve a landing target for feature: tried feature's
upstream, origin's recorded default branch, and none resolved -- this
repository has no upstream configured; pass --landing-target to name one"
```
Names both sources actually tried (branch's own upstream, remote's recorded
default) and states plainly there is no upstream, exactly as claimed. The
`merge --cleanup` path's separate, unchanged message
(`internal/worktreeclean/worktreeclean.go`, the `opts.MergedBranches != nil`
branch of the same switch) was confirmed untouched by inspection of the diff.

**3. fleet-03 FB5 (detached worktree removal).** PASS.
Live proof, reachable detached worktree (`git-tools worktree add <path>
<sha>`, no `--branch`, confirmed detached via `git symbolic-ref -q HEAD`
returning empty):
```
$ git-tools worktree remove <wt> --landing-target main
exit=0, removed:true
$ stat <wt>   # no such file or directory
```
Live proof, detached worktree ahead of the landing target (committed on top
of the detached checkout before removal):
```
$ git-tools worktree remove <wt> --landing-target main
exit=30, precondition_unmet.git.worktree_unmerged_work,
"1 commit(s) on <sha> are not reachable from main and would be lost"
$ stat <wt>   # still present
```
`worktreeCommitRef` (diff, `worktreeclean.go`) substitutes the checked-out
commit SHA for a branch name in exactly the target-entry contribution to
`subtreeState`, leaving the nested (non-target) detached case unmodified —
matches the report's stated scope limitation, confirmed by reading the diff's
`subtreeState` hunk directly (only the `p == targetResolved` case changed).

**4. fleet-03 FB8 (no code change; root-cause finding).** PASS — the claim is
honest, not fabricated. Independently read (not merely re-quoted from the
report):
- `workspace/.dat/ledger.md` LED-146 (lines 2571–2582): stranded branch ref
  after removal + landing-target resolution failing on a gone remote.
- `workspace/.dat/ledger.md` LED-101 (lines 1822–1836): a repo-wide file-walk
  gate misreading a leftover nested worktree as a violation, because
  per-task worktree cleanup wasn't sequenced ahead of the post-merge gate.
- `marketplace/.dat/feedback-register.json` FB8 (fleet-03 entry, dated
  2026-08-22): fifteen leftover nested per-task worktree registrations from
  a build loop that never called cleanup after each task's branch merged.
Own conclusion: FB8's own described cause (an orchestration/build-loop gap —
nobody removed a per-task worktree once its branch merged) shares no
mechanism with LED-146 (branch-ref stranding on removal, and a gone-remote
landing-target failure). No ledger entry ties the two. LED-101 is the closer
symptom match (a gate walking into a stale nested worktree) but is also a
distinct mechanism from FB8's own stated symptom (dirty `git status` from
unregistered checkouts, not a gate misfire). The report's "no shared root
cause, no code fix built, per instruction" framing holds up against the
primary sources.

**5. LED-152 (`git worktree prune` on the read-safe allowlist).** PASS,
including differential-execution evidence beyond the green suite.
`gitWorktreeReadSubcommands` changed from `{"list": true}` to `{"list": true,
"prune": true}`; lookup in `classifyGitSubSelect` is an exact,
case-normalized map hit on `firstOperand(rest)` — no substring or prefix
matching, so the widening cannot leak into an unrelated subcommand by
construction. Confirmed by reading the function directly.

Differential run: built `worktree-gate` from `git archive` snapshots of
`690947a` (parent) and `46001a7` (tip), fed both binaries identical
PreToolUse JSON payloads (cwd = this repo's primary checkout, which the gate
itself treats as "outside a worktree" for every write verb) for:

| command | parent verdict | tip verdict | same? |
|---|---|---|---|
| `git worktree prune` | deny | **allow** | no — intended flip |
| `git worktree prune --expire=now` | deny | **allow** | no — intended flip |
| `git worktree prune -n` | deny | **allow** | no — intended flip |
| `git worktree prune --verbose` | deny | **allow** | no — intended flip |
| `git worktree PRUNE` (case) | deny | **allow** | no — intended, symmetric with existing case-insensitive `list` handling |
| `git worktree list` | allow | allow | yes |
| `git worktree add ../x main` | deny | deny | yes |
| `git worktree remove ../x` | deny | deny | yes |
| `git worktree lock ../x` | deny | deny | yes |
| `git worktree unlock ../x` | deny | deny | yes |
| `git worktree move ../x ../y` | deny | deny | yes |
| `git worktree repair` | deny | deny | yes |
| `git worktree` (bare) | deny | deny | yes |
| `git worktree pruned` (near-miss) | deny | deny | yes |
| `git worktree prunemore` (near-miss) | deny | deny | yes |
| `git worktree prune; rm -rf /tmp/x` (chained, write elsewhere on the line) | deny | deny | yes |
| `git worktree prune && git add -A` (chained) | deny | deny | yes |
| `git status` | allow | allow | yes |
| `git log` | allow | allow | yes |

Only `prune` itself (and its case variants and flag combinations) flipped,
exactly as intended; every write verb, every near-miss subcommand name, and
every chained command that carries a write elsewhere on the same line kept
its old verdict on both revisions. This is the required set: commands that
must stay outside the newly-widened rule, proven unchanged on both the parent
and the tip, live, not just via the pre-existing unit table.

Also confirmed unchanged by reading the code: `sc15ReadVerb` (the separate
SC15 CLI-provisioning allowance) matches only `worktree list`, never
`worktree prune` — `<git-tools> worktree prune` still denies, since
git-tools has no such subcommand, exactly as the report claims. `verbs.json`
(the generic non-git bash-prefix classifier) is untouched by the diff.

## Full suite result

`go test -timeout 30m -count=1 ./...`, first run: **one flaky failure**, all
else pass.

```
FAIL	internal/cli	1082.771s (one subtest tree failed)
ok  	internal/gitexec        106.360s
ok  	internal/hooks            0.090s
ok  	internal/result           0.007s
ok  	internal/signing         17.437s
ok  	internal/worktreeclean   35.398s
ok  	worktree-gate/detect       6.434s
ok  	worktree-gate/fixtures     0.003s
ok  	worktree-gate/lifecycle  147.915s
```
Wall time: 18m03s (`time` around the whole run).

Failure detail: `TestOtherVerbs_DataMaps_DoNotCarryMergeDataKeys` and its
subtests failed with `no space left on device` and
`failed to write new configuration file .../.git/config.lock`. `df -h /tmp`
at the time showed `/tmp` at 99% capacity (364M free on a 31G tmpfs), driven
by large pre-existing unrelated artifacts (`mise_data_test`, `ruby-build.*`,
`python-build.*`, several GB each) — not by this task's own test artifacts.
This is host disk pressure, not a defect in the change.

Retest to confirm flake vs. real defect: re-ran the single failing test
(`-run TestOtherVerbs_DataMaps_DoNotCarryMergeDataKeys ./internal/cli/...`)
— passed in 32.3s. Then re-ran the entire `internal/cli` package again in
full (`go test -timeout 20m -count=1 ./internal/cli/...`) — passed clean in
1100.5s (18m20s wall). Confirmed flake, not a reproducible failure; disk
pressure on `/tmp` is a pre-existing host condition unrelated to this change,
present before this task started and not caused by it.

Total: two full-package reruns plus the original full-suite run, 0 real
failures found in the change under test across all three passes of
`internal/cli`, and one clean pass of every other package.

## Coverage

Existing test coverage (pre-authored by the implementer, read and confirmed
adequate rather than duplicated) already exercises the adversarial shape
this task asked for: unreachable-branch/unreachable-detached regression
cases exist for every mutating item (1, 3), a positive and a negative case
for the message-composition item (2), and the differential concern for item
5 is covered by an existing unit case (`sdet_git_verification_test.go`,
`"worktree unrecognized subcommand is write"`, `{"worktree","lock"}` stays
`ClassWrite`) plus this session's live cross-revision run above. No
additional test files were written to the repo — the existing suite plus
the live binary/differential runs already meet the acceptance bar; adding
parallel Go tests that would only re-assert what the live runs above already
proved would not add information.

Gaps: no coverage (live or unit) for `merge --cleanup`'s own DeleteBranch
behavior — by design, since `Options.DeleteBranch` is standalone-only and
`merge --cleanup` never sets it; confirmed by reading `internal/cli/merge.go`
is untouched in the diff.

## Failures

One flake, detailed above (`TestOtherVerbs_DataMaps_DoNotCarryMergeDataKeys`,
`internal/cli`, `no space left on device` / `.git/config.lock` write
failure) — environmental (`/tmp` at 99%), not reproducible on retest, not
attributable to the D2 change. No other failures found across three full or
targeted runs.

## CI/e2e

No CI/e2e harness beyond `go test ./...` is defined for this repo per
`test_strategy`; none run beyond what is reported above.

## Verdict

**PASS.** All five items' acceptance criteria hold under live, adversarial
proof against a real binary and real scratch repositories, not just the
green suite. The one full-suite failure was reproduced as a flake (disk
pressure, not code) and cleared on two independent reruns of the affected
package. FB8's "no shared root cause with LED-146" claim is independently
confirmed against primary sources, not just accepted from the report. LED-152's
narrowing (widening, in this direction) was proven safe via live differential
execution on both the parent commit and the branch tip, per this plan's own
differential-execution rule, not solely via the pre-existing green suite.
