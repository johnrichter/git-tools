# D3 merge-correctness — independent test-engineer verification

Commit under test: 5c9c528 (chore/d3-merge-correctness). Verified against a
fresh worktree checkout, no changes made to source under test.

## Verdict: PASS

All three claimed changes are real and match their description. `go build`,
`go vet`, and the full `go test ./... -count=1 -timeout 30m` all pass, fresh,
independently re-run. `commits_landed` is cross-checked against an
independently-computed `git rev-list --count` on a topology not in the
shipped suite. The LED-109 regression test genuinely uses a non-fast-forward
merge with global signing off, and genuinely asserts the merge commit's
signature. `published` is covered for both merged-only and merged-and-pushed
cases with correct values. One residual gap, already disclosed by the
engineer's own report, is confirmed genuinely hard to close and does not
block PASS (see check 5).

## Check 1 — diff matches the three claims

`git diff main...HEAD --stat`:
```
.task-reports/d3-merge-correctness-report.md | 188 +++++++
internal/cli/merge.go                        | 130 ++++-
internal/cli/merge_internal_test.go          |  98 +++++
internal/cli/merge_test.go                   | 148 ++++++
```

Read `internal/cli/merge.go` diff in full and `merge_test.go`/
`merge_internal_test.go` in full. Confirmed:
- `oldHead` captured via `resolveCommit` immediately before `repo.Merge`;
  `commitsLanded` runs `git rev-list --count oldHead..newHead` and is stored
  at `data["commits_landed"]` only on a real (non-dry-run, non-gate_negative)
  merge. Matches claim 1.
- `headSigState` reads `%G?` for `result.NewHead` when `sign == true`
  (i.e. the merge minted a commit); a state other than `G`/`U` produces
  `caveats.git.merge_commit_unsigned` at exit 10 without unwinding the
  merge. `TestMerge_LED109_GlobalSigningOff_RealNonFastForward_VerifiesSigned`
  is new in `merge_test.go`. Matches claim 2.
- New `--push` bool flag; `publishTarget` pushes the target ref to
  `cfg.Remote` only when local/remote tips differ; `data["published"]` is
  always present, `true` only when `--push` was given and a real merge
  landed. Matches claim 3.
- `internal/result/git.go` is untouched (confirmed: `git diff main...HEAD`
  touches only the four files listed above).

`gofmt -l internal/cli/merge.go internal/cli/merge_test.go
internal/cli/merge_internal_test.go` — no output, clean.

## Check 2 — build and vet, fresh

```
$ go build ./...
(exit 0, no output)
$ go vet ./...
(exit 0, no output)
```

## Check 3 — full suite, fresh, real run

Ran `go test ./... -count=1 -timeout 30m` from a clean state (own run, not
reusing the engineer's numbers). Wall time: `internal/cli` genuinely ran
1181.7s (~19.7 min), consistent with the report's stated ~19.6 min and with
Track D9's known pre-existing condition. Full output:

```
?   github.com/johnrichter/git-tools/cmd/git-tools    [no test files]
ok  github.com/johnrichter/git-tools/internal/cli           1181.714s
ok  github.com/johnrichter/git-tools/internal/gitexec       106.114s
ok  github.com/johnrichter/git-tools/internal/hooks         0.087s
ok  github.com/johnrichter/git-tools/internal/result        0.005s
ok  github.com/johnrichter/git-tools/internal/signing       17.442s
ok  github.com/johnrichter/git-tools/internal/worktreeclean  41.375s
ok  github.com/johnrichter/git-tools/worktree-gate/detect    6.448s
?   github.com/johnrichter/git-tools/worktree-gate/detect/cmd/worktree-gate [no test files]
ok  github.com/johnrichter/git-tools/worktree-gate/fixtures  0.004s
ok  github.com/johnrichter/git-tools/worktree-gate/lifecycle 147.039s
EXIT=0
```

Exit code 0, every package `ok`, no skips, no `panic: test timed out`.
Matches the claimed behavior exactly (10-min default timeout would false-fail
`internal/cli` only; 30-min timeout shows the true, passing result).

## Check 4 — `commits_landed`, independently exercised

Ran the two shipped tests targeted:
- `TestMerge_CommitsLanded_FastForwardReportsCarriedCommits` — PASS (12.14s)
- `TestMerge_CommitsLanded_OctopusReportsSourcesPlusMergeCommit` — PASS (16.86s)
- `TestCommitsLanded_CountsExactlyTheNewRange` (white-box) — PASS (11.07s)

Then authored and ran an independent adversarial test not in the shipped
suite, covering a topology none of the above use — a three-way octopus with
unequal per-source commit counts (alpha=1, beta=2, gamma=3), asserting the
reported `commits_landed` against a value computed via `git rev-list --count
oldHead..newHead` run directly by the test, not by calling the CLI's own
helper:

```go
func TestQA_CommitsLanded_CrossCheckedAgainstRevList_ThreeWayUnequal(t *testing.T) {
    // alpha: 1 commit, beta: 2, gamma: 3 -> octopus merge into main
    // independent := git rev-list --count oldHead..newHead (run directly)
    // asserts reported == independent, and reported == 7 (6 carried + 1 merge commit)
}
```
Result: PASS (22.72s). `commits_landed` reported `7`; independently-computed
`git rev-list --count` also `7`. This test was written to a temp scratch
file (`internal/cli/qa_scratch_test.go`), run, and then deleted — it is not
part of the deliverable and is not committed; only this report documents it.

## Check 5 — LED-109 regression test and post-merge signature check

`TestMerge_LED109_GlobalSigningOff_RealNonFastForward_VerifiesSigned`
(`internal/cli/merge_test.go`), read in full:

- Global signing off genuinely: `runGit(t, dir, "config", "--unset",
  "commit.gpgsign")` — unset, not merely `false`. `signingRepo` sets
  `commit.gpgsign=true` first, so this test explicitly reverses that ambient
  default before merging, and it uses a real ssh signing key
  (`gpg.format=ssh`, `user.signingkey`, `gpg.ssh.allowedSignersFile`), so a
  signed commit is possible but not automatic via the global switch.
- Genuine non-fast-forward: `signedBranch(feature)` branches off `main` and
  commits once, then `commitFile(dir, "main.txt", ...)` advances `main`
  independently before the merge runs. Both sides moved past their common
  ancestor, so `git merge` cannot fast-forward. Confirmed structurally
  distinct from the pre-existing `TestMerge_ForcedMergeCommit_
  IsSignedThoughGpgsignUnset` (SC-B1), which only forces a merge commit via
  `--fast-forward=never` on an otherwise fast-forwardable source — read both
  side by side, confirmed they are different topologies.
- Assertion strength: the test runs `git rev-list --parents -1 newHead` and
  requires 3 whitespace-separated fields (commit + 2 parents) — a real
  two-parent merge commit, not a fast-forward pass-through. It then runs
  `git verify-commit newHead` (via the shared `runGit` helper, which calls
  `t.Fatalf` on any non-zero exit — confirmed by reading
  `internal/cli/integration_test.go`), and separately checks `%G?` is `G` or
  `U`. This is a real cryptographic-signature check, not a metadata check
  (e.g. not merely checking a `-S` flag was passed).
- Ran directly: PASS (10.94s).

Post-merge signature-verification step (`headSigState` in `merge.go`), and
its negative branch:
- `TestHeadSigState_UnsignedCommit_ReportsN` (white-box,
  `merge_internal_test.go`) creates a genuinely unsigned commit
  (`commit.gpgsign=false`, no signing config at all) and asserts
  `headSigState` returns `"N"` — confirmed this is exactly the condition
  `merge.go`'s RunE branches on (`state != "G" && state != "U"`) to emit
  `caveats.git.merge_commit_unsigned`. Ran directly: PASS (2.80s).
- Independently traced the code path (`internal/cli/merge.go` lines ~223-240):
  `sign := willMint && !dryRun`; when `sign` is true, the merge already
  probed that signing is *possible* before running `git merge -S`, and a
  merge that cannot sign is refused earlier at
  `precondition_unmet.git.signing_key_unresolved` before any commit lands.
  This means a full CLI-level negative case (a merge that reaches the
  post-merge check with `sign == true` but an actually-unsigned tip) requires
  `git merge -S` to exit 0 while silently not signing — confirmed by direct
  experimentation with a broken `gpg.ssh.allowedSignersFile` mid-run that
  every failure mode reachable through the CLI's own preconditions either
  refuses the merge before it lands (exit 30) or still produces a `%G?` = `U`
  (unverifiable but not "unsigned" by this codebase's own convention) tip.
  This confirms the engineer's own disclosed gap honestly: the negative
  branch is proven only at the unit level (`headSigState` on a fabricated
  unsigned commit), not end-to-end through a real broken merge. This is a
  disclosed, not hidden, limitation and does not by itself sink the PASS
  verdict — but it is the one gap in this task's coverage worth flagging
  forward.

## Check 6 — `published`, both cases

Ran the three shipped tests targeted:
- `TestMerge_Publish_WithoutPushFlag_ReportsPublishedFalse` — PASS (6.62s).
  Asserts `published == false` and independently re-checks the bare remote's
  `refs/heads/main` is unchanged (`before == after`).
- `TestMerge_Publish_WithPushFlag_MergesAndPublishes` — PASS (11.69s).
  Asserts `published == true` and independently re-checks the bare remote's
  `refs/heads/main` now equals the merged tip (not just "changed" — equals
  the specific expected SHA).
- `TestMerge_Publish_DryRunNeverPublishes` — PASS (6.48s). `--push --dry-run`
  together: `published == false`, remote untouched. Correctly exercises that
  `--push` has no effect when nothing lands.

Both semantic cases (merged-only, merged-and-published) are covered with a
real bare-remote fixture (`newBareRemote`, pre-existing helper reused, read
to confirm it creates a real bare git remote, not a mock) and correct
resulting values in each case.

## Raw evidence log

Targeted run of every new/changed test (own re-run, not the engineer's
numbers):
```
=== RUN   TestHeadSigState_UnsignedCommit_ReportsN
--- PASS: TestHeadSigState_UnsignedCommit_ReportsN (2.80s)
=== RUN   TestCommitsLanded_CountsExactlyTheNewRange
--- PASS: TestCommitsLanded_CountsExactlyTheNewRange (11.07s)
=== RUN   TestMerge_ForcedMergeCommit_IsSignedThoughGpgsignUnset
--- PASS: TestMerge_ForcedMergeCommit_IsSignedThoughGpgsignUnset (8.13s)
=== RUN   TestMerge_CommitsLanded_OctopusReportsSourcesPlusMergeCommit
--- PASS: TestMerge_CommitsLanded_OctopusReportsSourcesPlusMergeCommit (16.86s)
=== RUN   TestMerge_CommitsLanded_FastForwardReportsCarriedCommits
--- PASS: TestMerge_CommitsLanded_FastForwardReportsCarriedCommits (12.14s)
=== RUN   TestMerge_LED109_GlobalSigningOff_RealNonFastForward_VerifiesSigned
--- PASS: TestMerge_LED109_GlobalSigningOff_RealNonFastForward_VerifiesSigned (10.94s)
=== RUN   TestMerge_Publish_WithoutPushFlag_ReportsPublishedFalse
--- PASS: TestMerge_Publish_WithoutPushFlag_ReportsPublishedFalse (6.62s)
=== RUN   TestMerge_Publish_WithPushFlag_MergesAndPublishes
--- PASS: TestMerge_Publish_WithPushFlag_MergesAndPublishes (11.69s)
=== RUN   TestMerge_Publish_DryRunNeverPublishes
--- PASS: TestMerge_Publish_DryRunNeverPublishes (6.48s)
PASS
ok  github.com/johnrichter/git-tools/internal/cli  86.746s
```

Independent adversarial cross-check (written, run, then deleted — not part
of the deliverable):
```
=== RUN   TestQA_CommitsLanded_CrossCheckedAgainstRevList_ThreeWayUnequal
--- PASS: TestQA_CommitsLanded_CrossCheckedAgainstRevList_ThreeWayUnequal (22.72s)
PASS
ok  github.com/johnrichter/git-tools/internal/cli  22.729s
```

Full suite (fresh, own run):
```
?   github.com/johnrichter/git-tools/cmd/git-tools    [no test files]
ok  github.com/johnrichter/git-tools/internal/cli           1181.714s
ok  github.com/johnrichter/git-tools/internal/gitexec       106.114s
ok  github.com/johnrichter/git-tools/internal/hooks         0.087s
ok  github.com/johnrichter/git-tools/internal/result        0.005s
ok  github.com/johnrichter/git-tools/internal/signing       17.442s
ok  github.com/johnrichter/git-tools/internal/worktreeclean  41.375s
ok  github.com/johnrichter/git-tools/worktree-gate/detect    6.448s
?   github.com/johnrichter/git-tools/worktree-gate/detect/cmd/worktree-gate [no test files]
ok  github.com/johnrichter/git-tools/worktree-gate/fixtures  0.004s
ok  github.com/johnrichter/git-tools/worktree-gate/lifecycle 147.039s
EXIT=0
```

`go build ./...` and `go vet ./...`: clean, exit 0, no output.

## Coverage note

No coverage percentage was computed (not requested by the task; the
package's existing test style is black-box CLI-driving plus targeted
white-box unit tests, not line-coverage gated). Notable gap: the
post-merge-unsigned caveat path (`caveats.git.merge_commit_unsigned`) has no
full end-to-end CLI-level negative test — confirmed genuinely infeasible to
construct honestly given the pre-merge signing probe's own preconditions
(see Check 5). This is disclosed by the engineer and independently confirmed
here, not a hidden gap.
