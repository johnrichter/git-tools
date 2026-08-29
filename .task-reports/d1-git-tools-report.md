# D1: git-tools consumes the go/git BackupRef rename

## Status: DONE

All `BackupTag`/`backup_tag` and tag-based-marker references in git-tools are
migrated to `BackupRef`/`backup_ref`, backed by a temporary `go.mod` replace
against the not-yet-merged `ai-shared-lib` branch. `go build`, `go vet`, and
`go test ./...` (all packages) pass. No git-tools-owned duplicate of the
LED-033 count-based check was found — see that section below.

## go.mod

`go.mod`: added a temporary `replace` directive pinning
`github.com/johnrichter/claude-shared-tooling/go/git` to the local
`ai-shared-lib` worktree
(`.../ai-shared-lib/.claude/worktrees/d1-backup-ref-migration/go/git`), with a
`// TEMPORARY:` comment stating it must be removed and replaced with a real
version pin once the ai-shared-lib merge is approved and a new go/git tag is
cut. The `require` line still lists the last real released version
(`v0.3.0`); the replace overrides only the resolved source, not the recorded
requirement.

## Rename sites (BackupTag -> BackupRef, backup_tag -> backup_ref)

Production code:
- `internal/result/git.go` — `RewriteOutcomeData`'s `"backup_tag"` data key ->
  `"backup_ref"`, reading `o.BackupRef` (was `o.BackupTag`).
- `internal/cli/merge.go` — `rewrittenSources`: emits `backup_ref` (was
  `backup_tag`); doc comment updated.
- `internal/signing/signing.go` — `Gate`: `record["backup_ref"]` /
  `rewritten` entries now carry `applied.BackupRef` (was `applied.BackupTag`
  under `backup_tag`); two doc comments ("backup tags" -> "backup refs") and
  one refusal's triage advice text ("its backup tag" -> "its backup ref").

Test code (same rename, plus two assertions that actually needed to change
behavior, see below):
- `internal/result/git_test.go` — `BackupTag` -> `BackupRef` struct literals
  and `backup_tag` -> `backup_ref` map-key assertions; example ref values
  updated from `"backup/aaa"` to `"refs/backup/aaa"` for readability.
- `internal/signing/signing_test.go` — `backup_tag` -> `backup_ref` in the
  `Refusal.Context` test fixture.
- `internal/cli/merge_test.go` — `backup_tag` -> `backup_ref` in every
  assertion and doc comment (5 sites), plus the two "no backup left" checks
  described below.
- `internal/cli/branch_test.go` — `backup_tag` -> `backup_ref`; test renamed
  `TestBranchDelete_MergedBranch_Succeeds_BackupTagPresent` ->
  `..._BackupRefPresent`.
- `internal/cli/integration_test.go` — `backup_tag` -> `backup_ref`
  (variable renamed `backupTag` -> `backupRef` too); comment updated.

## Marker-shape fix: no longer a tag

Two tests in `internal/cli/merge_test.go` asserted "no backup was left
behind" by running `git tag --list` and expecting empty output. That check
was a false negative waiting to happen: the marker is now a plain ref under
`refs/backup/`, so `git tag --list` would report "no tags" whether or not a
backup ref actually leaked, silently defeating the assertion. Both are now
`git for-each-ref refs/backup/`, which lists exactly what the marker actually
is:
- `TestMerge_AlreadySignedSource_IsSkippedWithoutRewriting`
- `TestMerge_SelfTargetInWorktree_RefusesBeforeGate`

No other `git tag`/`refs/tags/` reference in the module is backup-marker
related — `internal/cli/push.go`, `internal/cli/tag.go`,
`internal/cli/tag_test.go`, `internal/cli/push_test.go`, and the
`worktree-gate/detect` command-classifier files all concern git-tools' own
real `tag` verb (annotated release tags) and are untouched.

## LED-033-adjacent check: no git-tools duplicate found

Searched `internal/cli/resign.go`, `internal/cli/rebase.go`,
`internal/signing/signing.go`, and the rest of the module for any
count-based rewrite-verification logic (commit counts, parent counts,
"_lost_commits"-style checks). None exists: git-tools' resign/rebase/merge
commands call straight into `go/git`'s `Resign`/`Rebase`/`Merge` and report
whatever those return; the reachability-vs-count logic LED-033 fixed lives
entirely inside `go/git`'s own `verify()` and is not duplicated here.
**No change made in this area** — plain statement per the task brief's
explicit fallback.

## Build/vet/test results

- `go build ./...` — clean, no output.
- `go vet ./...` — clean, no output.
- `go test ./... -count=1 -timeout 20m` — all packages pass:
  ```
  ok  	github.com/johnrichter/git-tools/internal/cli	1099.683s
  ok  	github.com/johnrichter/git-tools/internal/gitexec	105.917s
  ok  	github.com/johnrichter/git-tools/internal/hooks	0.086s
  ok  	github.com/johnrichter/git-tools/internal/result	0.008s
  ok  	github.com/johnrichter/git-tools/internal/signing	17.335s
  ok  	github.com/johnrichter/git-tools/internal/worktreeclean	41.212s
  ok  	github.com/johnrichter/git-tools/worktree-gate/detect	6.420s
  ok  	github.com/johnrichter/git-tools/worktree-gate/fixtures	0.004s
  ok  	github.com/johnrichter/git-tools/worktree-gate/lifecycle	147.003s
  ```
  All backup-ref-touching tests pass explicitly, including
  `TestBranchDelete_MergedBranch_Succeeds_BackupRefPresent`,
  `TestMerge_UnsignedSource_IsResignedBeforeLanding`,
  `TestMerge_ConflictAfterRewrite_CarriesTheRewrittenSourceList`,
  `TestMerge_OctopusLaterSourceRefusal_ReportsEarlierRewrite`,
  `TestMerge_OctopusUnrelatedLaterSource_ReportsEarlierRewrite`,
  `TestMerge_AlreadySignedSource_IsSkippedWithoutRewriting`, and
  `TestMerge_SelfTargetInWorktree_RefusesBeforeGate`.

### Investigation: the apparent "hang" in `internal/cli`

The first `go test ./...` run (default `-timeout 10m`, the go test default)
failed with `panic: test timed out after 10m0s`, always mid-flight on the
same test (`TestMerge_SelfTargetInWorktree_RefusesBeforeGate`), with 0
`--- FAIL` lines logged before the panic. Investigated whether this was a
real deadlock:

- Re-ran that one test in isolation: passes in ~9s.
- Sent `SIGQUIT` to a live run mid-flight: the goroutine dump showed only
  ordinary GC/runtime goroutines idle plus exactly one goroutine blocked in
  `os/exec.Cmd.Wait` on a `git` child process that was itself just doing
  normal, uncontended work (no lock file, no gpg-agent/pinentry, no shared
  socket in the call chain) — not a stuck syscall, just this test's own git
  subprocess mid-run when the signal arrived.
  Repeated with `-p 1` (packages strictly sequential, ruling out
  cross-package resource contention): same eventual timeout, same shape.
- Timed `go build` directly (what `buildCLI` calls once per test function,
  contrary to its own doc comment claiming "once per test binary run"):
  ~180ms warm, ~180ms even with 4 run concurrently — not the bottleneck.
- Ran `internal/cli` alone with `-v`: steady, uninterrupted progress the
  entire time (124 straight `--- PASS` lines, 0 failures) until the 10-minute
  mark, landing exactly on `600.010s` — the go test default per-package
  timeout — mid-way through the same test as before.
- Ran the full suite with `-timeout 20m`: `internal/cli` completed cleanly in
  `1099.683s` (~18.3 min), all green.

**Root cause: not a hang.** `internal/cli` has 175 `Test*` functions, and
`buildCLI` (`internal/cli/integration_test.go`) runs a full `go build` inside
every one of them rather than once for the package, on top of each test's own
several `git`/`ssh-keygen` subprocess invocations; in this environment that
totals a bit over 10 minutes end to end, so the package trips go test's
default 600s per-package timeout close to its very end regardless of which
test happens to be in flight at that instant — reproducible with zero code
changes, with or without cross-package concurrency. This predates this task
and is unrelated to the `BackupRef` rename or the temporary `go/git` replace
(confirmed both by the per-call `go build` timing above and by the identical
failure shape existing before this task's edits). It is a test-suite
performance/CI-config gap, not a git-tools defect this task's scope covers —
flagging for the test-engineer/quality-reviewer rather than fixing it here,
since fixing it (e.g. building the CLI once per package via `TestMain` or
`sync.Once`, or raising CI's `-timeout`) is test infrastructure, outside a
naming-rename task's scope.

## Assumptions & deviations

- Left the `require` line for `go/git` at `v0.3.0` (untouched) and layered
  the replace on top, per the task's explicit instruction — the real pin
  bump happens only once ai-shared-lib's branch merges and a tag is cut.
  This is a deliberate, temporary, marked deviation from a normally-accurate
  `go.mod`.
- Changed two placeholder ref-value strings in `internal/result/git_test.go`
  from `"backup/aaa"` to `"refs/backup/aaa"` — cosmetic only (test asserts
  identity, not shape), done for readability against the real marker's
  namespace.
- Did not touch the `internal/cli` per-test `buildCLI` architecture despite
  finding it responsible for the near-timeout: it is pre-existing,
  functionally correct, and out of this task's rename-only scope.

## Hand-off notes

- Test-engineer / quality-reviewer: CI running `go test ./...` for
  `internal/cli` with the default (or any <~18min) `-timeout` will
  intermittently or consistently panic on timeout, unrelated to correctness.
  Worth a follow-up task to cache the built CLI binary once per test binary
  run and/or raise CI's configured timeout for this package.
- The `go.mod` replace directive is a merge-blocker marker: it must be
  removed and replaced with a real `go/git` version bump before this
  branch's own PR can merge to git-tools main. It is intentionally left in
  place in this commit per the task's explicit instruction.
- All backup-marker-shape test coverage (both former `git tag --list`
  checks) now actually verifies the `refs/backup/` namespace instead of a
  vacuously-true tag check — worth confirming in review that this was the
  intended tightening, not just a mechanical rename.
