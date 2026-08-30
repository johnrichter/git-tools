# D9 test-suite timeout -- independent test verification

**Verdict: PASS.** Both goals hold up under adversarial re-check. FB30 re-verified true. One
environmental false-failure caught and isolated (my own test-harness mistake, not the diff);
one real environmental disk-full flake caught, diagnosed, and reproduced-away with a clean
re-run, documented below rather than hidden.

## 0. Diff reviewed

`git diff main...HEAD --stat`:
```
.task-reports/d9-test-timeout-report.md | 120 ++++++++++++++++++++++++++++++++
internal/cli/integration_test.go        |  58 +++++++++++----
scripts/test-report.sh                  |  96 +++++++++++++++++++++++++
```
Matches the report's "Files touched" section exactly. `buildCLI` now uses a package-level
`sync.Once`/`cliBinPath`/`cliBinErr`, builds into `os.MkdirTemp` (not `t.TempDir()`), and a
new `TestMain` removes that dir after `m.Run()`. `scripts/test-report.sh` is new, 96 lines,
matches the report's described behavior on read-through.

## 1. Claims real? -- confirmed by diff read + all checks below.

## 2. `go build ./...` / `go vet ./...` -- fresh, both clean, no output.

## 3. Full suite, fresh run, real wall time

Host state at test time: **not quiet.** `uptime` showed load average 3.4-5.4 on a 16-core
box throughout, and `ps` directly showed concurrent `go test ./...` processes from sibling
worktrees (`d4-tag-create-hardening`, `d6-residuals`) running the whole time -- this
independently corroborates the report's own contention claim, it isn't just the report's
say-so.

First full `go test ./... -count=1 -timeout 30m` (default `/tmp`-backed TMPDIR):
- Wall: **1004.9s** for `internal/cli` (`FAIL` -- see below), other packages unaffected.
- **6 tests failed, all "no space left on device" from `/tmp`.** `df -h /tmp` showed the
  host's 31G tmpfs at 100% full, driven by unrelated large dirs (`ruby-build`,
  `python-build`, `mise_data_test*`, several GB each) left by other concurrent host activity
  -- not by this diff or this task. Confirmed environmental: sibling worktree `d4` was
  independently already routing its own `go test` through a private `TMPDIR` inside its own
  worktree specifically to dodge this, proof this is a known, host-wide, pre-existing
  condition unrelated to D9.
- Re-ran `internal/cli` alone with `TMPDIR` outside the full tmpfs. First attempt used a
  `TMPDIR` nested **inside this git worktree** by mistake, which broke `TestRequireRepo_*`
  and 2 others because their "not a git working tree" scratch dirs sat under this repo's own
  `.git` and `git` walked up to find it -- a test-harness bug of mine, not the diff's. Fixed
  by pointing `TMPDIR` at `/var/tmp/d9-verify` (non-tmpfs, no ancestor `.git`, 134G free).
- **Clean re-run: `ok internal/cli 1038.021s`.** `time`: user 4m3.4s, sys 2m29.2s -> combined
  CPU 392.6s, wall 1038.0s -> **37.8% CPU utilization** -- matches the report's own claimed
  ~390s CPU floor and ~37% utilization figure almost exactly.
- All other packages re-run clean with the same safe `TMPDIR`: `gitexec` 106.656s, `hooks`
  0.163s, `result` 0.005s, `signing` 17.506s, `worktreeclean` 41.657s, `worktree-gate/detect`
  6.469s, `worktree-gate/fixtures` 0.004s, `worktree-gate/lifecycle` 148.316s -- all match the
  report's per-package figures within noise.

**My own measurement supports "the fix works, wall time is contention-affected," it does not
contradict it.** Wall time (1038s) landed within a second of the report's own two runs
(1038.428s, 1036.950s) on a host independently confirmed to be sharing CPU with other `go
test` jobs at the time; CPU time (392.6s) matches the report's ~390s floor. The disk-full
failures were a separate, real, host-level flake -- correctly diagnosed as unrelated to the
diff, and I do not paper over it: it is a genuine environment condition (31G tmpfs `/tmp`
saturated by other concurrent work), not a flake in this task's tests, and not evidence
against the rebuild fix.

## 4. Hand-off (a): does a build failure inside `sync.Once` fail every caller, not just the first?

Forced it live: temporarily changed `buildCLI`'s build command from `"go"` to
`"go-nonexistent-binary-forced-failure"`, then ran three tests from three different test
files in one `go test` invocation:
```
go test ./internal/cli/... -run 'TestSign_ResignsTipCommitWithIdenticalTree|TestTagCreate_BareShape_CreatesAndPushes|TestMerge_UnsignedSource_IsResignedBeforeLanding' -v -timeout 60s
```
Result: **all three failed**, each with the same clear error
(`go build git-tools: exec: "go-nonexistent-binary-forced-failure": executable file not found
in $PATH`), from `integration_test.go`, `merge_test.go`, and `tag_test.go` respectively --
not just the first to trigger `sync.Once`, and no silent skip. Confirmed: `cliBinErr` is
checked and `t.Fatal`'d on every `buildCLI` call, exactly as the hand-off note claimed.
Change reverted immediately after; `git diff` on the file shows no residual change.

## 5. Hand-off (b): can `TestMain` cleanup race a still-running subtest?

Code-level: `TestMain` is `code := m.Run(); ...cleanup...; os.Exit(code)`. Go's `testing`
package guarantees `m.Run()` does not return until every top-level test and every subtest
spawned via `t.Run` (including any `t.Parallel()` ones) has finished -- this is a hard
language/stdlib guarantee, not an implementation detail this diff could get wrong.
Additionally checked: `grep -rn "t.Parallel()"` across every `*_test.go` in the package
returns **zero matches**, and there is no `go func(){...cliBinPath...}()` outside the testing
framework's own goroutines anywhere in `internal/cli`. So there is no code path -- parallel
or sequential -- where cleanup can run before every test needing the binary is done.
**Finding: cannot race, confirmed by code inspection; no live reproduction needed given the
stdlib guarantee is unconditional.**

## 6. `scripts/test-report.sh` exercised live

- `shellcheck scripts/test-report.sh` -- clean, no findings.
- Clean pass: `bash scripts/test-report.sh ./internal/hooks/... 60s` ->
  `ok   - ./internal/hooks/...: 7 tests passed in 0s`, exit 0. `go test -list '.*'
  ./internal/hooks/...` independently confirms 7 top-level tests -- count is real, not just
  present.
- Forced timeout: `bash scripts/test-report.sh ./internal/cli/... 5s` -> exit 1, stderr:
  ```
  FAIL - ./internal/cli/... did not finish within 5s (exit 137)
    tests completed:  8/175
    tests remaining:  167
    tests failed:     0
    elapsed:          5s
    ETA to finish remaining: ~167s
    resume command:
      go test -run '^(...)$' -timeout 250s ./internal/cli/...
  ```
  Verified each number: `go test -list '.*' ./internal/cli/...` independently gives 175 total
  (matches denominator). 8+167=175 (completed+remaining accounts for the whole census, no
  double-count or drop). ETA math: avg-per-test floored to >=1s * 167 remaining = 167s
  (matches). Suggested timeout: 167 + 167/2 = 250 (matches, and clears the 60s floor).
  Independently ran `go test -list '<the resume regex>' ./internal/cli/...` and got exactly
  **167** matches -- the resume command's `-run` regex selects precisely the tests the report
  says are remaining, not more, not fewer, not off-by-one.

**All reported numbers are correct, not just present, on both the pass and the forced-timeout
path.**

## 7. FB30 re-verification -- SSH signing needs no GPG keyring

Read `internal/cli/merge_test.go`'s `signingRepo` (lines 37-61): sets `gpg.format ssh`,
`user.signingkey` to an ephemeral SSH pubkey generated per-test via `ssh-keygen`, and
`gpg.ssh.allowedSignersFile` to a matching allowed-signers file -- no GPG config, no GPG
binary invocation, no reference to any GPG keyring anywhere in the fixture.

Live check: ran every signing-adjacent test (`TestSigningRefusal_*`, `TestSign_*`,
`TestMerge_*Sign*`/`*Signed*`/`*Signable*`, `TestTagCreate_*`) with `GNUPGHOME` pointed at a
freshly created, empty directory (no agent socket, no keys, no trustdb) and a `PATH`
restricted to `/usr/bin:/bin` plus the Go toolchain (this host's system `gpg`/`gpg2` still
technically exist at `/usr/bin`, so this isolates "no keyring configured" rather than "no gpg
binary present" -- removing the system package was out of scope as a destructive,
un-revertible host change). **Result: every test in that run passed** -- 0 failures, no
signing-related error, confirming the SSH-format signing path never needed or touched a GPG
keyring. **Confidence: high** that FB30's original "missing GPG key" claim is stale against
current source, on the strength of both the code read and the live empty-`GNUPGHOME` run;
not absolute certainty only because the `gpg` binary itself remained reachable on `PATH` (it
is never invoked, per both the code and the passing run, but a literal "no gpg installed at
all" host was not constructed).

## Raw evidence retained

- `/tmp/d9_full_test.log` -- first full-suite run, disk-full failures, for the record.
- `/tmp/d9_cli_retest.log` -- second `internal/cli` run, self-inflicted git-nesting failures
  from my own bad `TMPDIR` choice, for the record (not a code bug).
- `/tmp/d9_cli_retest2.log` -- clean `internal/cli` rerun, `ok ... 1038.021s`.
- `/tmp/d9_rest.log` -- clean rerun of every other package.

## Summary by acceptance area

| Area | Verdict | Evidence |
|---|---|---|
| Goal 1: build-once fix is real | PASS | diff read; forced-failure test (sec 4) proves `sync.Once`+`cliBinErr` gates every caller |
| Goal 1: wall-time claim (contention, not regression) | PASS | independent 1038.021s measurement on a host independently confirmed to be under concurrent load; CPU time 392.6s / 37.8% util matches report's ~390s / ~37% |
| Goal 1 hand-off (a): every caller fails on build error | PASS | live forced failure across 3 files, sec 4 |
| Goal 1 hand-off (b): no cleanup race | PASS | stdlib guarantee + zero `t.Parallel()` usage, sec 5 |
| Goal 2: self-reporting timeout wrapper | PASS | live pass run + live forced-timeout run, every number independently recomputed and matched, sec 6 |
| FB30: SSH signing needs no GPG keyring | PASS (high confidence) | code read + live empty-`GNUPGHOME` run, sec 7 |
| `go build`/`go vet` | PASS | clean, no output |
| Full suite, fresh | PASS (with noted flake) | disk-full failure isolated as pre-existing host condition, not code; clean reruns of every package confirm no real regression |
