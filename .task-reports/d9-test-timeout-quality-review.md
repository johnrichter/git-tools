# D9 test-suite timeout -- quality review

**Verdict: ACCEPT WITH FIXES (FIX-APPLIED).** Goal 1's `sync.Once` build-once fix is correct,
idiomatic and race-free as written; Goal 2's wrapper was correct on the two shapes it was
tested against and broke on three others, all three now fixed and re-verified. Six findings
fixed (three of them in the wrapper's failure handling), three noted and left. Full suite
re-run fresh on the fixed tree: **PASS, exit 0, all 10 test packages ok.** The
"pre-existing host `/tmp` exhaustion" label on the test-engineer's flake is **accurate** --
independently confirmed, with a causal argument that this diff cannot cause it. One
substantive correction to both prior reports: the residual wall time is **not** CPU
contention from concurrent jobs, and the predicted "quiet host ~390s" does not happen.

## Findings

### Blocking

None.

### Major -- all fixed

1. **`scripts/test-report.sh:53,55` (pre-fix): a truncated last JSON line killed the whole
   report.** `jq` in JSON-stream mode aborts on a partial trailing object, and under
   `set -euo pipefail` that took the script down before it printed anything. The truncation is
   not hypothetical: it is the expected result of `timeout --signal=KILL` landing mid-write on
   `go test -json`, i.e. the script's primary path. Probe (bash, `set -euo pipefail`, events
   file with a truncated final line): script exits **4**, prints only
   `parse error: Unfinished string at EOF at line 3, column 13`, and no report at all.
   **Fixed:** read line-by-line with `jq -rR 'fromjson? | ...'` in both jq calls; the same
   probe now exits 0 and yields the parseable events. This also tolerates any non-JSON line
   `go test` may interleave.
2. **`scripts/test-report.sh:41` (pre-fix): the census counted `go test -list`'s own status
   lines as tests.** `grep -v '^ok'` strips only `ok` lines, not `?  <pkg> [no test files]`
   or `FAIL <pkg> [setup failed]`. Live probe against a scratch package with a deliberate
   compile error:
   ```
   tests completed:  0/2
   resume command:
     go test -run '^(FAIL|FAIL	d9broken/pkg [setup failed])$' -timeout 60s ./pkg/...
   ```
   -- two fabricated "tests" and a nonsense resume regex. **Fixed:** census keeps only
   test-name-shaped lines, `grep -E '^(Test|Benchmark|Fuzz|Example)'`. The `internal/cli`
   census is unchanged at 175 by this fix, so the happy path is untouched.
3. **`scripts/test-report.sh:86-93` (pre-fix): a vacuous resume command, and every non-zero
   `go test` exit reported as a timeout.** Whenever `remaining` was 0 -- which is *every*
   genuine test-failure run, where all tests ran and one failed -- the script still printed
   `go test -run '^()$'`, a regex that matches nothing and exits 0, i.e. a "resume command"
   that would report success without running a test. The headline also claimed
   `did not finish within <budget>` for any non-zero status, including a run that finished
   fine and simply failed. **Fixed:** exit 137 (the `timeout` kill) and other non-zero exits
   get distinct headlines; the ETA and resume lines print only when `remaining > 0`; an empty
   census prints an explicit no-census line naming the likely cause. Re-probed live:
   compile-failure run and one-failing-test run now both report accurately (see Evidence).

### Minor -- all fixed

4. **`internal/cli/integration_test.go:19-28` (pre-fix): the shared temp directory leaked
   whenever the build failed.** Cleanup was keyed on `cliBinPath`, which stays `""` if
   `go build` fails after `os.MkdirTemp` already succeeded, so `TestMain` skipped the
   `RemoveAll`. Proven both directions live, by forcing the build command to a nonexistent
   binary with a private `TMPDIR`: pre-fix left `git-tools-cli-test-2677291603/` behind;
   post-fix leaves nothing. Small in bytes, but it is a leak into exactly the filesystem this
   host is already failing on. **Fixed:** track `cliBinDir` alongside `cliBinPath` and clean
   the directory, which also drops the `filepath.Dir(cliBinPath)` indirection.
5. **`.github/workflows/ci.yml:145-146` (pre-fix): the new script had no lint gate.** The
   SC-SH-LINT step named `scripts/surface-hygiene.sh` literally, so `scripts/test-report.sh`
   -- shellcheck-clean today -- would drift silently forever. **Fixed:** widened the
   enumerator to `shellcheck scripts/*.sh`, which also covers the next script added. Verified
   clean across both scripts.
6. **`.github/workflows/ci.yml:111-118` (pre-fix): this diff made the CI comment false.** The
   comment asserted "internal/cli builds and runs the git-tools binary end-to-end per test
   case" and pointed at FB18's build-once fix as the durable work still to do -- precisely
   what this diff delivered. A stale factual claim next to the timeout it justifies is how the
   next maintainer mis-sizes that timeout. **Fixed:** rewritten to state the current shape
   (built once per package run, ~390s CPU), keep the history, and keep FB18's still-open
   second half (fail the gate on a runtime budget). Budget left at 20m: changing it needs a
   real CI measurement, which is not this task.

### Minor -- noted, not fixed (out of this task's scope)

7. `scripts/test-report.sh`'s clean-pass line counts skipped tests in its "N tests passed"
   figure (the jq select takes `pass|fail|skip`). Harmless on the exit-0 path, imprecise
   wording.
8. `timeout --signal=KILL` kills the `go` process, not the test binary and `git` grandchildren
   it already spawned, so a killed run can leave orphans churning. Reaping the process group
   (`setsid` + `kill -- -pgid`) is more machinery than this task warrants. Related cosmetic:
   bash prints its own `... Killed  timeout --signal=KILL ...` job line above the report;
   suppressing it would also swallow `go test`'s stderr.
9. A multi-package argument (`./internal/...`) dedupes same-named tests across packages, so
   the census can undercount. The script documents single-package use; left as documented.

## Goal 1 correctness pass

- **`sync.Once` pattern: correct and idiomatic.** `cliBinPath`/`cliBinErr` are written only
  inside the `Do` body and read only after it returns, so `Once`'s happens-before edge covers
  every reader. `cliBinErr` is checked on every call, so a build failure fails all callers,
  not just the first (independently re-confirmed by the test-engineer across three test
  files, and by my own forced-failure runs in finding 4).
- **`TestMain` exit paths: all covered.** `m.Run()` returns only after every top-level test
  and subtest has finished, so cleanup cannot race a live test; a `t.Fatal` is just a failing
  test and still returns through `m.Run()`; `os.Exit(code)` after cleanup preserves the exit
  status. The package uses zero `t.Parallel()`, so there is not even a latent parallel case.
  The one path genuinely not covered by `TestMain` is a panic that kills the process before
  `m.Run()` returns -- unavoidable in this pattern, and the residue is one 6.4M temp dir.
- **`-race` clean.** `go test ./internal/cli/... -race -count=1 -run` over four `buildCLI`
  callers spanning two test files (so the `Once` is contended across files): `ok 7.323s`, no
  race reports.
- The only other `go build` in a test (`worktree-gate/detect/decide_sc15_..._test.go:31`)
  builds once for one test and hashes the result; it is correct as-is and out of scope.

## Wall-time diagnosis -- correction to both prior reports

Both reports attribute the residual ~1035s wall time to CPU contention from concurrent
`go test` jobs, and the implementer's report predicts "on a quiet host this suite should land
closer to its ~390s CPU-time floor." **My fresh run disproves the prediction.** At the time
of my run there were **no other `go test` processes on the host** (checked with `ps` sorted by
CPU: top consumers were `trajectory` 13%, agent `python`/`claude` ~5%, `bees` btrfs dedupe
3%; load average 3.2-3.9 on 16 cores). Result: `internal/cli` **1040.0s**, whole-suite wall
17:20.49, CPU 309.85s user + 198.38s sys = 508s, **48% CPU** -- within 0.5% of the two loaded
runs (1038.4s, 1037.0s) and the test-engineer's (1038.0s).

Removing the competing jobs changed nothing, so the ceiling is not CPU starvation. Sub-50%
utilization here is the signature of a suite dominated by serialized subprocess spawns and
filesystem sync waits -- ~175 tests, each building scratch git repos and shelling out to
`git` and `git-tools` -- which idle cores cannot absorb.

**This does not weaken Goal 1.** Collapsing 144 `go build` call sites to one build is real and
confirmed (instrumented once-only run by the implementer, and my own forced-failure runs show
exactly one build attempt per package run). It matters most where the toolchain is slowest:
the D1 review recorded CI at 1101s against the 1200s budget, so the removed rebuilds are the
headroom.

**Plan feedback:** the next lever on this suite is `t.Parallel()` with per-test repos, or
cheaper fixtures -- not a quieter host. Whoever picks up FB18's runtime-budget half should
size it against ~1040s of measured wall time, not against an expected ~390s.

## `/tmp`-exhaustion flake -- independently confirmed pre-existing

1. **The condition is live and foreign.** `/tmp` is a 31G tmpfs at 98% (716-722M free).
   Occupants: `/tmp/mise_data_test*` (3.2G + 2.8G + four more), `/tmp/fleet02-verify-mise-data`
   (3.2G), six `ruby-build.2026082[12]*` dirs at 0.8-1.1G each, `python-build.*` logs -- all
   timestamped **Aug 21-22**, 8-9 days before this branch's work, from mise/ruby/python
   toolchain builds. No git-tools artifact appears in the top consumers.
2. **This diff has no mechanism to cause it.** Pre-fix, each of 144 `buildCLI` calls wrote a
   6.4M binary into that test's `t.TempDir()` and removed it at test end: ~920M of write
   churn, ~6.4M held at any moment. Post-fix: one 6.4M binary, held for the package run.
   Churn drops ~140x and peak is unchanged, so the diff writes strictly *less* to the
   filesystem it is accused of exhausting. Finding 4 closes the one direction in which it
   could have leaked.
3. **Corroboration from my own run.** My private `TMPDIR` collected 22 stray 114-byte files
   containing `refs/heads/main <old> <new>` -- pre-push payloads written by the host's
   `/usr/local/lib/dd-git-hooks`, not by git-tools. Host tooling drops files into `TMPDIR`
   during any git-heavy run: the same class of foreign occupant that filled `/tmp`.

Conclusion: the label is honest and no regression is hiding behind it. FB30's re-verification
(SSH-format signing, no GPG keyring) is likewise sound on both the code read and the
empty-`GNUPGHOME` run; I add nothing to it.

## Test-suite assessment

- **Goal 1 is adequately covered** without new tests: all 175 `internal/cli` tests exercise
  `buildCLI`, and the two hand-off risks (error propagation, cleanup race) were each closed
  with the right kind of evidence -- a live forced failure for the first, the stdlib join
  guarantee plus a zero-`t.Parallel()` check for the second.
- **Goal 2's verification tested only the shapes the tool was designed for.** Clean pass and
  clean timeout both got careful, numerically-recomputed checks; no probe asked what happens
  when the *input* is malformed. All three major findings above surfaced in under a minute
  each from two trivial adversarial inputs: a scratch package that fails to compile, and an
  events file with a truncated last line. **Gap for the test-engineer: exercise a tool's
  failure and degenerate shapes, not only its designed-for shape** -- especially for a tool
  whose entire job is to report on a run that went wrong.
- No authored test for `scripts/test-report.sh` itself: accepted (no shell test harness in
  this repo, developer utility), and its only standing gate is the CI shellcheck wired in by
  finding 5.

## Re-verification evidence (fixed tree, fresh)

```
go build ./...                          clean, no output
go vet ./...                            clean, no output
go test ./... -count=1 -timeout 30m     exit 0
  ok  internal/cli                1040.003s
  ok  internal/gitexec             106.893s
  ok  internal/hooks                 0.125s
  ok  internal/result                0.005s
  ok  internal/signing              17.494s
  ok  internal/worktreeclean        41.691s
  ok  worktree-gate/detect           6.455s
  ok  worktree-gate/fixtures         0.004s
  ok  worktree-gate/lifecycle      148.823s
  309.85s user  198.38s system  48% cpu  17:20.49 total
go test ./internal/cli/... -race -count=1 -run <4 buildCLI callers, 2 files>   ok 7.323s, no races
shellcheck scripts/*.sh                 clean (both scripts)
bash scripts/surface-hygiene.sh         0 violations
.github/workflows/ci.yml                parses as YAML; jobs build-test, golangci-lint, surface-hygiene
```

`TMPDIR` pointed at `/var/tmp/d9-review-tmp` (overlay, 134G free). This is a deviation from a
default-`TMPDIR` run and is stated plainly: with 716M free on the `/tmp` tmpfs, a suite that
builds hundreds of scratch git repos fails for host reasons on any branch, `main` included.

`scripts/test-report.sh` probes, post-fix:

| Path | Result |
|---|---|
| clean pass (`./internal/hooks/... 120s`) | `ok - ./internal/hooks/...: 7 tests passed in 1s`, exit 0; census cross-checked at 7 |
| forced timeout (`./internal/cli/... 6s`) | exit 137 headline, 8/175 completed, 167 remaining, ETA ~167s, resume `-timeout 250s`; 8+167=175 |
| compile failure (scratch pkg) | `go test exited 1`, `0/0`, explicit no-census line, no bogus resume command, exit 1 |
| one failing test (scratch pkg) | `go test exited 1`, `2/2` completed, 1 failed, `nothing to resume`, exit 1 |
| truncated events file | `jq -rR 'fromjson?'` exits 0 and yields parseable events (pre-fix: script died, exit 4) |
| forced build failure + private TMPDIR | no leftover `git-tools-cli-test-*` dir (pre-fix: one left behind) |

## Residual risk

- Orphaned test/`git` processes after a `--signal=KILL` timeout (finding 8): a wrapper for
  interactive/agent use, so a stray subprocess is untidy rather than dangerous.
- The 20m CI budget is unchanged and now un-measured against the fixed tree; FB18's
  runtime-budget half stays open, and the CI comment says so.
- `internal/cli` still costs ~1040s wall on this host. Within the 30m budget, but the
  wall-time cause is different from what the prior reports state (see above), so the next
  optimization should not be planned around a "quiet host" assumption.

## Files touched by this review

- `internal/cli/integration_test.go` -- finding 4 (temp-dir leak on build failure).
- `scripts/test-report.sh` -- findings 1, 2, 3 (jq truncation tolerance, census filter,
  honest failure reporting and no vacuous resume command).
- `.github/workflows/ci.yml` -- findings 5, 6 (shellcheck enumerator, stale comment).
- `.task-reports/d9-test-timeout-quality-review.md` -- this report.
