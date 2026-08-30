# D9 -- internal/cli test timeout

**Status: complete.** Both goals delivered; FB30 re-verified; no code change needed for FB30.

## Goal 1 -- fix the per-test-case rebuild

**Root cause confirmed.** `buildCLI` in `internal/cli/integration_test.go` called `go build`
on every invocation -- once per test case, ~180 call sites across the package's `*_test.go`
files -- despite its own comment claiming "once per test binary run." The binary path it
built into came from `t.TempDir()`, which is unique per test, so nothing was actually shared.

**Fix.** `buildCLI` now builds exactly once for the whole `internal/cli` test binary, guarded
by a package-level `sync.Once` (`cliBinOnce`/`cliBinPath`/`cliBinErr`), into a directory made
with `os.MkdirTemp` (outside any single test's `t.TempDir()`, since it must outlive every test
that shares it). A new `TestMain` runs the suite then removes that directory. Verified by
instrumenting the `sync.Once` body with a marker and running three different test files' worth
of subtests together: the build ran exactly once.

**Baseline.**

| | wall time | source |
|---|---|---|
| old (per-test rebuild) | ~18-20 min | given, confirmed live earlier this session |
| new (build-once fix) | **~1035-1038s (~17.3 min), 2 consecutive live runs** | `go test ./internal/cli/... -count=1 -timeout 30m`, this session |

Two live runs of the fixed suite landed within a second of each other (1038.428s and
1036.950s), so the number is reproducible, not noise. It is a smaller wall-clock win than
the removed-rebuild-count would suggest on an idle machine: `time`'s own accounting shows
each run spending only ~390s of combined user+system CPU at ~37% CPU utilization, meaning
most of the ~1037s wall clock is this host waiting for CPU time, not doing work -- this
session shared the machine with multiple other concurrent `go test` processes from other
worktrees/tasks the whole time (confirmed via `ps`, PIDs and elapsed times cross-checked
against this session's own timestamps). The rebuild fix is real and load-bearing (confirmed
by the once-only instrumented run above); the wall-clock ceiling is host contention, a
separate condition and not this task's problem to fix. On a quiet host this suite should
land closer to its ~390s CPU-time floor, plus subprocess/git I/O to the extent that isn't
already inside that figure.

No hang-like behavior observed in either run (both landed normally with `ok`, no stall,
no unresponsive process) -- consistent with the plan's separately-noted "stuck pipe read"
being an unrelated, session-local issue, not reproduced here.

## Goal 2 -- self-reporting timeout wrapper

Added `scripts/test-report.sh`: runs one Go package's test census under a `timeout(1)`
budget via `go test -json`, parsed with `jq` (already a repo dependency, see
`scripts/surface-hygiene.sh`).

- Clean run (`go test` itself exits 0, whether killed early by nothing or genuinely done):
  prints `ok - <package>: N tests passed in Ts` and exits 0.
- Forced failure or timeout kill (`timeout` kills the process, or `go test` exits non-zero
  on a real test failure): prints, to stderr, and exits 1:
  - tests completed (N/total, from a `go test -list '.*'` census taken up front)
  - tests remaining (the census minus what a `go test -json` event stream shows completed)
  - tests failed
  - elapsed wall time
  - an ETA for the remaining tests (average time-per-completed-test x remaining count)
  - a ready-to-run resume command: `go test -run '^(...)$' -timeout <suggested>s <package> <extra args>`,
    with the remaining-test regex and a margined suggested timeout (ETA + 50%, floored at
    60s) already filled in -- per the "counting principle," nothing here makes a caller
    recompute a number the tool already has.

Documented constraint: callers should not pass their own `-run` in `[extra go test args...]`
-- the wrapper computes `-run` itself for the resume command, and its census assumes the
package's whole test set is in scope. This keeps the tool a thin reporting wrapper rather
than a new test-selection framework, per the task's own "keep it simple" instruction.

Sanity-run twice against `internal/cli`: a 3s budget correctly reports 7/175 completed,
168 remaining, and a working resume command; a generous budget on a filtered `-run` set
correctly reports the clean-pass line. `shellcheck scripts/test-report.sh` is clean.

## FB30 re-verification

**Claim: current source no longer needs a GPG keyring for the tests it names -- confirmed
true, live.** `internal/cli/merge_test.go`'s `signingRepo` (the fixture every commit-signing
test in the package uses) sets `gpg.format=ssh` with an ephemeral SSH allowed-signers file --
not GPG at all. The full `internal/cli` suite, including every `TestMerge_*`, `TestTagCreate_*`
and `TestSign_*` case, passed live in this session with no signing-related failure and no
GPG keyring present on this host. No code change made; FB30 is informational and its specific
claim is stale against current source.

## Test results

- `go build ./...` -- pass, no output.
- `go vet ./...` -- pass, no output.
- `go test ./... -count=1 -timeout 30m` -- pass, all 10 packages (2 packages have no test
  files):

  ```
  ok  	internal/cli              1034.669s
  ok  	internal/gitexec           106.578s
  ok  	internal/hooks               0.104s
  ok  	internal/result               0.007s
  ok  	internal/signing             17.419s
  ok  	internal/worktreeclean        41.501s
  ok  	worktree-gate/detect          6.449s
  ok  	worktree-gate/fixtures        0.004s
  ok  	worktree-gate/lifecycle     147.926s
  ```

## Files touched

- `internal/cli/integration_test.go` -- `buildCLI` build-once fix, new `TestMain`.
- `scripts/test-report.sh` -- new self-reporting timeout wrapper (Goal 2).
- `.task-reports/d9-test-timeout-report.md` -- this report.

## Hand-off notes

- Test-engineer: no new test files authored here (out of scope for this role); the fix
  itself is exercised implicitly by every existing `internal/cli` test that calls
  `buildCLI`. Worth an adversarial check that a `go build` failure inside the `sync.Once`
  still fails every test that calls `buildCLI` in that run (it does -- `cliBinErr` is
  checked on every call), and that `TestMain`'s cleanup doesn't race a still-running
  subtest (it can't -- `m.Run()` blocks until every test/subtest is done).
- Quality-reviewer: the wall-time number in this report is contention-affected; treat the
  once-only build confirmation (instrumented run) as the real evidence the rebuild is fixed,
  not the wall-clock figure alone.
- `scripts/test-report.sh`'s "don't pass your own -run" constraint is enforced by
  documentation only, not by argument validation -- flag if that's too weak for how this
  script ends up being invoked elsewhere in the repo.
