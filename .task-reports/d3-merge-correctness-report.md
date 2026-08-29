# D3: merge correctness — commit count, LED-109 regression test, FB24 publish field

## Status: DONE

Three additive fields/behaviors on `git-tools merge`, all implemented directly
in `internal/cli/merge.go` (no change to `internal/result/git.go` — merge
builds its result `data` map inline, not through that package, and that stays
true here). `go build ./...` and `go vet ./...` are clean. `go test ./...`
results are below.

## 1. Commit count in every merge result

`internal/cli/merge.go`:
- Captures the target's pre-merge tip (`oldHead`, via the existing
  `resolveCommit` helper) immediately before `repo.Merge` runs — target is
  already confirmed checked out and non-detached by that point.
- New helper `commitsLanded(ctx, dir, oldHead, newHead)` runs
  `git rev-list --count oldHead..newHead`, matching the existing
  `rev-list --count` convention already used in
  `internal/worktreeclean/worktreeclean.go`'s `CountUnmerged`.
- On a real (non-dry-run) merge, the result's `data["commits_landed"]` carries
  this count — commits carried in verbatim (fast-forward) plus, when one is
  minted, the merge commit itself. Not added to the dry-run branch (nothing
  lands) or to the `gate_negative` exit (nothing lands there either).
- A count failure returns `internal.git.commit_count_failed` via `finishErr`,
  matching the file's existing error-handling idiom.

Tests (`internal/cli/merge_test.go`):
- `TestMerge_CommitsLanded_FastForwardReportsCarriedCommits` — three
  fast-forwarded commits, `commits_landed == 3`.
- `TestMerge_CommitsLanded_OctopusReportsSourcesPlusMergeCommit` — two
  two-commit sources into an untouched target, `commits_landed == 5` (4
  carried + the 1 minted merge commit), proving the count is not just a sum of
  each source's own commits.
- White-box unit coverage of the arithmetic itself, isolated from the gate and
  signing machinery, in the new `internal/cli/merge_internal_test.go`
  (`TestCommitsLanded_CountsExactlyTheNewRange`, plus the empty-range case).

## 2. LED-109 permanent regression test + post-merge signature verification

LED-109 itself (global signing off, a real non-fast-forward merge, the merge
commit still verifying signed) was already fixed; this closes two things:

- **The missing regression test**:
  `TestMerge_LED109_GlobalSigningOff_RealNonFastForward_VerifiesSigned` in
  `internal/cli/merge_test.go`. `commit.gpgsign` is unset (not merely
  `false`), a resolvable ssh signing key is configured, and main/feature each
  advance independently so the merge cannot fast-forward — a genuine
  non-fast-forward, structurally distinct from the existing
  `TestMerge_ForcedMergeCommit_IsSignedThoughGpgsignUnset`, which forces the
  merge commit via `--fast-forward=never` on an otherwise fast-forwardable
  source. Asserts the landed head is a real two-parent merge commit, that
  `git verify-commit` succeeds on it, and that `%G?` is `G` or `U`.

- **The residual gap — verifying the post-merge head's own signature**: added
  to `internal/cli/merge.go`. When the merge minted a signed commit
  (`sign == true`), a new `headSigState(ctx, dir, ref)` helper reads `%G?` for
  `result.NewHead` right after the merge lands. A state other than `G`/`U` is
  reported as `caveats.git.merge_commit_unsigned` (exit 10) — the merge is
  never unwound, but it is never silently reported as a plain success either.
  A read failure itself is `internal.git.merge_commit_verify_failed`.

  This step's positive path is exercised by the LED-109 regression test
  above (and by every other existing merge test asserting a signed head).
  Forcing the negative branch through the built binary turns out to be
  infeasible without literally breaking the signing fix elsewhere (every
  manual attempt — an unreadable/mismatched `gpg.ssh.allowedSignersFile` —
  still yields `%G?` = `U`, which this codebase's own `allVerify` convention,
  mirrored here, already treats as verified). The negative branch is instead
  pinned directly at the unit level:
  `TestHeadSigState_UnsignedCommit_ReportsN` in the new
  `internal/cli/merge_internal_test.go` proves `headSigState` reports `N` for
  a genuinely unsigned commit — the exact condition the RunE caveat check
  tests — isolating the read from the (very hard to fake) claim that `-S`
  minted a commit without actually signing it.

## 3. FLEET-03 FB24: `published` field

`internal/cli/merge.go`:
- New `--push` bool flag (registered alongside `--cleanup`, same idiom).
- New `publishTarget(ctx, cfg, dir, target)` helper: resolves the target's
  local tip and the remote's current value for the same ref (reusing the
  existing `localRefSHA`/`remoteRefSHA` helpers `push`'s own `pushRef`
  already relies on), and pushes only when they differ.
- `data["published"]` is always present: `false` unless `--push` was given
  and a real (non-dry-run) merge landed, in which case a successful publish
  sets it `true`. A publish failure is reported as
  `caveats.git.merge_publish_failed` (exit 10) — the merge itself is not
  unwound, matching the `--cleanup` caveat idiom immediately below it in the
  same function.
- `merge --help` documents `--push` and the `published` field's semantics.

Tests (`internal/cli/merge_test.go`):
- `TestMerge_Publish_WithoutPushFlag_ReportsPublishedFalse` — merged only;
  `published == false`; remote untouched.
- `TestMerge_Publish_WithPushFlag_MergesAndPublishes` — `--push`;
  `published == true`; remote's `main` actually advances to the merged tip.
- `TestMerge_Publish_DryRunNeverPublishes` — `--push --dry-run`; nothing
  lands, nothing publishes, `published == false`.

## Files touched

- `internal/cli/merge.go` — the three features (see above); `--push` flag;
  `commitsLanded`, `headSigState`, `publishTarget` helpers; `Long` help text
  updated.
- `internal/cli/merge_test.go` — 7 new black-box tests (commit-count x2,
  LED-109 x1, publish x3, per section above — recount: 2 + 1 + 3 = 6, plus
  none removed).
- `internal/cli/merge_internal_test.go` — new white-box test file (package
  `cli`) covering `headSigState` and `commitsLanded` directly against scratch
  repos.
- `internal/result/git.go` — inspected, not modified (merge's result data
  never routes through it).

## Test results

- `go build ./...` — pass.
- `go vet ./...` — clean, no findings.
- `gofmt -l` on every touched file — clean.
- `go test ./internal/cli/... -run 'TestMerge' -v` (the complete `TestMerge*`
  suite, pre-existing tests and all seven new ones together): all 40 tests
  pass, `ok github.com/johnrichter/git-tools/internal/cli 347.541s`. No
  interaction or regression between the new fields and any existing merge
  test.
- Full `go test ./...` (every package): pass, exit code 0. `internal/cli`
  took 1178.447s (~19.6 min) — matches its known real runtime, so a plain
  `go test ./...` (default 10-min per-package timeout) reports it as a false
  `panic: test timed out after 10m0s` failure; re-run with `-timeout 30m` (or
  higher) to see the true result. This was verified during this task: an
  initial default-timeout run failed with exactly that timeout panic on
  `internal/cli` only, every other package already `ok`; a second run with
  `-timeout 30m` then showed `internal/cli` finishing clean:
  ```
  ok   github.com/johnrichter/git-tools/internal/cli           1178.447s
  ok   github.com/johnrichter/git-tools/internal/gitexec       105.997s
  ok   github.com/johnrichter/git-tools/internal/hooks         0.088s
  ok   github.com/johnrichter/git-tools/internal/result        0.005s
  ok   github.com/johnrichter/git-tools/internal/signing       17.360s
  ok   github.com/johnrichter/git-tools/internal/worktreeclean 41.310s
  ok   github.com/johnrichter/git-tools/worktree-gate/detect   6.412s
  ok   github.com/johnrichter/git-tools/worktree-gate/fixtures 0.004s
  ok   github.com/johnrichter/git-tools/worktree-gate/lifecycle 147.232s
  ```

## Assumptions & deviations

- **Field naming**: `commits_landed` (not `commits`, to avoid reading like
  the signing gate's existing per-source `commits` field in `signing_gate`
  entries) and `published` (matches the task's own wording and `root.go`'s
  existing use of "publish" for this remote-facing operation).
- **`--push` flag added**: FB24's brief assumes merge "can, depending on
  flags/config, both merge locally and push the result" — but no such
  capability existed in `merge.go` before this change (grepped for
  `AutoPush`/`Publish`/`publish` flag; none). Added the minimal `--push` bool
  flag itself as the mechanism the `published` field reports on, following
  the existing `--cleanup` opt-in idiom exactly (flag name, doc placement,
  caveat-on-failure-not-rollback behavior). If a config-level auto-push
  toggle was intended instead of/in addition to a flag, that is out of this
  task's stated scope ("three small, additive fields and their tests, not a
  redesign") and not added.
- **Signature-verification caveat is new, not pre-existing**: confirmed via
  read of `merge.go` before editing that no post-merge signature check
  existed; this is a genuinely new step, per the task's explicit instruction
  to add it "if it does not already exist."
- **Negative-branch coverage for the unsigned-caveat check** is at the unit
  level (`headSigState` returns `N` for a real unsigned commit), not via a
  full CLI run that forces `-S` to silently fail — see section 2 above for
  why that full-stack negative case could not be constructed honestly.

## Hand-off notes

- Test-engineer: the CLI-level negative case for
  `caveats.git.merge_commit_unsigned` is not exercised end-to-end (see
  Assumptions). If a way exists to make `git merge -S` succeed while leaving
  the tip unsigned, exercising that path fully would close the gap
  end-to-end; otherwise the unit-level `headSigState` proof plus code review
  of the two-line condition is the intended coverage.
- Quality-reviewer: check `commits_landed`'s placement (only present when
  `!result.DryRun`) against the rest of `data`'s presence conventions (see
  `TestMerge_DataKeys_PerExitPath` for the existing per-exit-path contract for
  `repo`/`target`) — a similarly exhaustive per-exit-path table for
  `commits_landed`/`published` was not added, to stay within "three small,
  additive fields," but could be added if the team wants that same rigor
  extended to the new fields.
- `internal/cli`'s suite genuinely takes ~18-20 minutes. Always run
  `go test -timeout 30m ./...` (or set `-timeout` at least that high) for
  this package — the default 10-minute `go test` timeout kills it mid-run
  and reports a misleading failure that is not a real test bug.
