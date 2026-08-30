# D3 merge-correctness — quality review

Reviewed: commit `5c9c528` (implementation) plus `72c78e8` (test verification), on
branch `chore/d3-merge-correctness`. Reviewed the full `git diff main...HEAD`, the
surrounding `merge.go`/`push.go`/`signing.go` code, the two test files, the shared
test fixtures, `.github/workflows/ci.yml`, and `go/git@v0.4.0`'s `Merge`.

## Verdict: ACCEPT WITH FIXES (FIX-APPLIED)

Both gaps the test-engineer flagged are closed with **permanent** tests, including
the end-to-end negative the implementer and the test-engineer both judged
infeasible — it is feasible, and it now exists. Four further findings, all minor,
were fixed in place. `go build ./...`, `go vet ./...`, `gofmt -l` and the full
`go test ./... -count=1 -timeout 30m` are green on the resulting tree (evidence
below). Nothing blocking remains.

The three shipped changes themselves are correct: `commits_landed` is computed
from a pre-merge tip captured after the gate and before `repo.Merge`, so it counts
exactly this merge's range; the post-merge signature check reads git's own `%G?`
with the same `G`/`U` vocabulary `internal/signing/signing.go:311` already uses;
`published` reports a real push through a non-forced refspec, and a publish
failure caveats without unwinding the merge, matching the `--cleanup` idiom below
it. Field names (`commits_landed`, `published`) match the result data's existing
snake_case convention (`new_head`, `dry_run`, `would_merge`, `cleaned_worktrees`),
and both fields plus `--push` are documented in `merge --help`'s `Long` text.

## Gap 1 — `commits_landed` on a three-way unequal octopus: closed

`internal/cli/merge_test.go` —
`TestMerge_CommitsLanded_ThreeWayUnequalOctopusMatchesRevList`.

- Sources contribute 1, 2 and 3 commits (`alpha`, `beta`, `gamma`), so no
  per-source count, source sum, largest-source count, or parent count coincides
  with the right answer.
- Captures the pre-merge tip itself, then asserts the reported value against a
  `git rev-list --count <oldHead>..<newHead>` the test runs directly — the
  assertion does not route through `merge.go`'s own helper — **and** against the
  literal 7 (6 carried + 1 minted).
- Also asserts the landed head is a genuine multi-parent octopus commit.

**Correction to the test-engineer's throwaway probe, worth recording:** a
three-source octopus into an *unmoved* target lands a **three**-parent commit, not
four — git fast-forwards the target onto the first source and then mints one
commit over the three source tips. The count (7) is unaffected; a shape assertion
written on the four-parent assumption fails. The permanent test pins the true
shape with that reasoning in a comment.

## Gap 2 — CLI-level negative for the unsigned caveat: closed, not deferred

`internal/cli/merge_test.go` — `TestMerge_PostMergeTipUnsigned_ReportsUnsignedCaveat`,
plus fixture `stripSignatureAfterMerge`.

Both earlier reports concluded this was infeasible. That conclusion was reached by
attacking the wrong half of the condition: both tried to make *signing itself*
fail (an unreadable or mismatched `gpg.ssh.allowedSignersFile`), which either trips
the pre-merge probe (exit 30, nothing lands) or yields `%G?` = `U`, which this
codebase treats as verified. The check's actual precondition is narrower and is
reachable: **`git merge -S` exits 0 and the tip that git-tools then reads is
unsigned.** Anything that rewrites the tip between the merge and the read
satisfies it.

The fixture does exactly that, using only git's own extension point:

- A repository-local `core.hooksPath` (overriding the host's global hooks) with one
  `post-merge` hook.
- The hook rebuilds the just-minted merge commit as an identical but unsigned one:
  same tree, same two parents, `git commit-tree --no-gpg-sign`, then
  `git update-ref HEAD`. (`git commit --amend` cannot be used — git refuses to
  amend while the merge state is still present, verified: `fatal: You are in the
  middle of a merge -- cannot amend.`)
- `go/git@v0.4.0`'s `Merge` resolves `NewHead` from `HEAD` *after* the merge
  command returns (`merge.go:114`), so `result.NewHead` is the unsigned commit and
  the check sees the real condition.

The test asserts the full contract: status `caveats`/exit 10, exactly one caveat
with code `caveats.git.merge_commit_unsigned` and context `sig_state == "N"`,
`HEAD` still at the reported `new_head` (the merge is not unwound), the head is a
genuine two-parent merge commit, `%G?` is `N`, and `published` is `false` (an
unsigned merge is never published). It is fail-closed: git ignores a post-merge
hook's exit status, so a hook that failed to run leaves the tip signed and the
test fails rather than passing vacuously.

This is a legitimate scenario, not a contrivance: a repo-local hook, a wrapper, or
a concurrent tool rewriting the tip is precisely the class of thing a check that
exists to distrust `git merge -S`'s exit code must catch.

## Other findings, all fixed

| Severity | Location | Finding | Fix |
|---|---|---|---|
| minor | `internal/cli/merge.go:297` (pre-fix) | `data["published"]` was set *after* the signature check, so the `caveats.git.merge_commit_unsigned` exit — a real merge that landed — carried no `published` key at all, leaving a caller to read its absence as either answer. The help text promises the field "states plainly whether a publish happened". | Moved the `false` initialisation above the signature check, so every exit from the point the merge landed carries it. Pinned by the new caveat test's `published == false` assertion. |
| minor | `internal/cli/merge.go:530` (pre-fix) | `publishTarget` returned `fmt.Errorf("%s", strings.TrimSpace(string(res.Stderr)))` on a non-zero `git push`: an empty stderr yields an empty error string, which lands in the caveat message as a dangling `failed: `. It also diverged from the idiom every other helper in the file uses. | Returns `&git.CommandError{Args, ExitCode, Stderr}` like `resolveCommit`, `commitsLanded`, `headSigState` and `gitPathOutput`. The message now always names the command and exit code. |
| minor | `internal/cli/merge_internal_test.go:18-47` (pre-fix) | Three new helpers (`runGitHere`, `initScratchRepo`, `commitHere`) duplicated helpers that already exist in the **same package**: `cgit`, `cleanupFixture` and `commitIn` in `worktree_test.go`. `runGitHere` was byte-for-byte `cgit` under another name. | Deleted all three; both tests now use the package's existing fixtures. Net −38 lines, and the file's imports drop to `context`/`testing`. |
| minor | `internal/cli/merge.go:77` | The exit-10 help line grew to 146 characters — the file's pre-existing maximum for that block is 115, and this block renders straight into an operator's terminal. | Reworded to 114 characters, same meaning, ordered signature-first since that is the newest cause. |

## Observations accepted without change

- **`merge --push` does not go through the `push` verb's own preconditions.** `push`
  refuses `--repo`/`--config` outright, refuses a dirty tree (exit 30), and runs
  the content-guardrail scan; `publishTarget` re-implements just the
  compare-and-push core. Accepted: merge runs the same content-guardrail scan
  (`merge.go:171`) plus the signing gate and the primary-checkout refusal against
  the same `repo.Dir` it then publishes, the refspec is non-forced so a diverged
  remote is refused rather than overwritten, and a dirty working tree cannot change
  what a ref push publishes. The `--repo` difference is a genuine semantic
  divergence from `push`'s stated rule and is raised as plan feedback below, not as
  a defect in this task.
- **`pushRef` was not reused.** It is cobra-coupled — it emits its own result and
  returns `finish*` errors — so it cannot be called from inside merge's flow, which
  must keep going to the publish/cleanup reporting. The duplication is ~10 lines
  and justified; extracting a shared compare-and-push core is a refactor, not this
  task.
- **Exit 20 (`gate_negative`) carries neither new field.** Nothing landed and
  nothing published there, and that path builds its own data map. Consistent with
  the implementer's stated choice; `commits_landed` is likewise absent on a dry run
  (documented in `Long` as "a real merge's result").
- **The new `resolveCommit(ctx, dir, "HEAD")` adds no exit-90 path for an unborn
  target.** Verified by reading the call order: the signing gate runs first
  (`merge.go:179`) and refuses any source whose fork point with the target cannot
  be computed (`internal/signing/signing.go:118-127`, exit 30), which is exactly
  the unborn-HEAD case. Established by code reading, not by a test.

## Test-suite assessment

Adequate now, and honest before — every gap in the shipped suite was disclosed
rather than hidden, which is why they were straightforward to close.

- Post-fix coverage for the three features: fast-forward count, equal two-way
  octopus count, unequal three-way octopus count cross-checked against raw
  rev-list, white-box range arithmetic including the empty range; LED-109's real
  non-fast-forward signed merge; the signature check's positive path (every signed
  merge test) and now its negative path both at unit level and end to end;
  `published` false / true / dry-run / unsigned-caveat.
- Remaining gap, deliberately not filled (would be gold-plating): no test drives
  `caveats.git.merge_publish_failed` (a rejected push) or
  `internal.git.commit_count_failed`. Both are two-line error branches over
  helpers that are themselves tested.
- Cost: the two new tests add ~32s to `internal/cli` (measured 1222.4s here vs the
  test-engineer's 1181.7s on the same machine). Both are named `TestMerge_*`, so
  CI's `-run TestMerge` signing-fixture step exercises them. That step carries **no
  `-timeout`**, so it runs under Go's 10-minute default — comfortable today, but it
  is the step this task lengthened. Tracked upstream as FB18; flagged, not changed.

## Re-verification (fresh, final tree)

```
$ gofmt -l internal/ cmd/ worktree-gate/     # no output
$ go build ./...                             # exit 0, no output
$ go vet ./...                               # exit 0, no output
$ go test ./... -count=1 -timeout 30m
?   github.com/johnrichter/git-tools/cmd/git-tools    [no test files]
ok  github.com/johnrichter/git-tools/internal/cli            1222.391s
ok  github.com/johnrichter/git-tools/internal/gitexec         107.025s
ok  github.com/johnrichter/git-tools/internal/hooks             0.092s
ok  github.com/johnrichter/git-tools/internal/result            0.012s
ok  github.com/johnrichter/git-tools/internal/signing          17.521s
ok  github.com/johnrichter/git-tools/internal/worktreeclean    41.682s
ok  github.com/johnrichter/git-tools/worktree-gate/detect       6.489s
?   github.com/johnrichter/git-tools/worktree-gate/detect/cmd/worktree-gate [no test files]
ok  github.com/johnrichter/git-tools/worktree-gate/fixtures      0.004s
ok  github.com/johnrichter/git-tools/worktree-gate/lifecycle   148.191s
EXIT=0
```

Targeted run of the two new and two reworked tests (`-count=1 -v`):

```
--- PASS: TestHeadSigState_UnsignedCommit_ReportsN (2.81s)
--- PASS: TestCommitsLanded_CountsExactlyTheNewRange (11.09s)
--- PASS: TestMerge_CommitsLanded_ThreeWayUnequalOctopusMatchesRevList (22.77s)
--- PASS: TestMerge_PostMergeTipUnsigned_ReportsUnsignedCaveat (9.30s)
ok  github.com/johnrichter/git-tools/internal/cli  45.992s
```

One earlier full run was discarded rather than reported: a scratch probe file used
to establish the post-merge-hook mechanism was still on disk when it started, and
`internal/cli` rebuilds the binary per test, so the tree it measured was not the
tree being delivered. The probe file is deleted; the run above is a single clean
run over the final tree.

`internal/cli` genuinely needs `-timeout 30m`; the default 10-minute timeout
reports a false `panic: test timed out` for this package. Pre-existing (Track D9).

## Files touched by this review

- `internal/cli/merge.go` — `published` present on the post-landing caveat exits;
  `publishTarget`'s error idiom; exit-10 help line width.
- `internal/cli/merge_test.go` — two new permanent tests + `stripSignatureAfterMerge`
  fixture; `strconv` import.
- `internal/cli/merge_internal_test.go` — duplicate helpers removed in favour of the
  package's existing fixtures.

## Plan feedback

1. **Confirm the field-name contract with the consumer.** `commits_landed` and
   `published` have no in-repo consumer by design — they are wire output for
   fleet-03/FB24's caller. Nothing in this repo can catch a producer/consumer name
   mismatch, so someone holding fleet-03's spec should confirm those two exact keys
   (and that `published` is expected as always-present-once-landed rather than
   present-only-when-true).
2. **`--push` versus a config-level auto-push is still open**, as the implementer
   disclosed. The flag is the minimal mechanism; if FB24 meant config-driven
   publishing, that is a separate task.
3. **`merge --push` publishes a `--repo`-selected repository**, which the `push`
   verb refuses on principle ("push always operates on the invoking process's own
   working directory"). Two sanctioned publish paths now disagree about whether a
   flag may retarget a publish. Worth an explicit decision — either document the
   difference or make merge's publish honour the same rule.
4. **CI's signing-fixture step has no `-timeout`.** It runs `-run TestMerge` under
   the 10-minute default while the `go test ./...` step gets 20m. This task added
   ~32s to that subset. A one-word change when FB18 is picked up.
