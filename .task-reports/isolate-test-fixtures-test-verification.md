# Test-engineer verification: isolate test fixtures from host git hooks/config

## Verdict: PASS

Independent re-verification confirms the report's mechanism, correctness, and magnitude,
with fresh evidence gathered in this session (not copied from the implementer's numbers).

## 1. Diff coverage and mechanism trace

`git diff main...HEAD --stat`: 9 files, 261 insertions / 17 deletions. Confirmed every
fixture-creating helper named in the dispatch now sets both `GIT_CONFIG_GLOBAL` and
`GIT_CONFIG_SYSTEM` to `os.DevNull` via `t.Setenv`:

- `internal/cli/integration_test.go`: `initRepo` (line ~121-122)
- `internal/cli/worktree_test.go`: `cleanupFixture` (line ~95-96)
- `worktree-gate/lifecycle/helpers_test.go`: `newScratchRepo` (line ~62-63)
- `worktree-gate/detect/decide_sc15_branchdelete_ordering_sdet_test.go`: inline in
  `TestSDET_SC15_BranchDelete_GateSanctionsButGuardStillRefuses`

All four files already import `os` (verified via grep), so no missing-import risk.

**Call-site trace (3 independent paths, read the actual code, not the report's prose):**

1. `initRepo` -> `runGit` (`exec.Command("git", args...)`, only `cmd.Dir` set, no `cmd.Env`)
   -> inherits process env, so `t.Setenv` in `initRepo` reaches every `git` subprocess this
   fixture spawns directly (init, config, commit, etc).
2. `initRepo` -> tests also call `runCLI(t, bin, ...)` (`exec.Command(bin, args...)`, again
   only default env, no `cmd.Env` override) -> the built `git-tools` binary is a *separate*
   process that itself shells out to `git`; because `exec.Command` with no `Env` field
   inherits the parent (test) process's environment, the binary's own `git` calls also see
   `GIT_CONFIG_GLOBAL=/dev/null` / `GIT_CONFIG_SYSTEM=/dev/null`. Confirmed by reading
   `runCLI` at `internal/cli/integration_test.go:83-92` (no `cmd.Env` line present).
3. `newScratchRepo` -> `signableScratchRepo` (`worktree-gate/lifecycle/helpers_test.go:17-38`)
   layers a real ssh signing key on top of `newScratchRepo`'s dir; `signableScratchRepo`
   itself calls `newScratchRepo(t)` first, so it inherits the `t.Setenv` isolation
   transitively — no separate `t.Setenv` needed there, confirmed by reading the function body.
4. `cleanupFixture` in `worktree_test.go` sets both vars directly before calling `cgit`
   (same `exec.Command`-with-inherited-env shape as `runGit`).

No call site bypasses `t.Setenv` with an explicit `cmd.Env` that would need the vars added
separately — every fixture-side and CLI-side git invocation in the touched packages goes
through one of `runGit`/`runCLI`/`cgit`, all of which inherit process env.

## 2. Host hook confirmed real, and isolation confirmed to block it

- `git config --global --get core.hooksPath` on this host -> `/usr/local/lib/dd-git-hooks`
  (confirmed independently, matches the report).
- `git config --global --list | grep -i sign` -> `gpg.ssh.allowedsignersfile=...`,
  `commit.gpgsign=true`, `tag.forcesignannotated=true` — confirms the host's real signing
  config that underlies check 5 below.

**Disposable repro, unisolated** (scratch repo, no env override):
```
git -C /tmp/hookrepro1 commit -q -m "test"
```
-> `0.53s user 0.33s system 30% cpu 2.755 total` wall. The hook binary genuinely runs and
dominates wall time on a single commit, matching the report's own single-commit measurement
(2.78s) almost exactly.

**Same repro, isolated** (`GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null` set on
every git invocation for that repo):
```
git -C /tmp/hookrepro2 commit -q -m "test"
```
-> `0.00s user 0.01s system 98% cpu 0.010 total` wall (275x faster). A follow-up
`git config --get core.hooksPath` inside the isolated repo exits 1 (unset) — confirming the
host's hook path genuinely does not reach the isolated repo, not just that the commit was
merely fast for an unrelated reason.

## 3. `internal/cli` package, fresh timed run (this session's own number)

```
go test ./internal/cli/... -count=1
```
-> `ok ... 39.563s` (measured wall via `time`: `10.00s user 20.63s system 76% cpu 39.961s
total`). This is an independently-measured number, not copied from the implementer's 39.6s
claim — it happens to agree closely, which is expected since both runs are on the same
now-isolated code on the same host, and is itself evidence of a real, stable effect rather
than a one-off.

## 4. Full repo `go test ./...`, fresh timed run, zero failures

```
go test ./... -count=1
```
Run 1: all 10 test packages `ok`, 0 failures, wall `1:46.52` (`45.69s user 46.00s system
86% cpu`). Per-package breakdown: `internal/cli` 39.533s, `internal/gitexec` 106.053s
(flagged, unmodified, out of scope — see below), `internal/signing` 17.336s,
`internal/worktreeclean` 41.251s, `worktree-gate/lifecycle` 3.959s, remaining packages
sub-second.

## 5. Host-signing-key dependency: investigated and confirmed correct, not a workaround

**What broke and why:** the host's real `~/.gitconfig_personal` unconditionally sets global
`gpg.format=ssh` and `user.signingkey=~/.ssh/id_ed25519_dd_workspaces_signing` (confirmed
directly: `git config --global --get gpg.format` -> `ssh`; `--get user.signingkey` -> the
real key path). Before isolation, every `initRepo`/`newScratchRepo`-based fixture that
needed to *actually sign* something (tag `create`, `merge`'s re-sign-on-land, `sign`/
`resign` themselves) silently resolved this real, personal signing key from the host's
global config — invisible because nothing about that dependency showed up as a failure on
this developer's own machine. Isolating `GIT_CONFIG_GLOBAL`/`GIT_CONFIG_SYSTEM` removes that
inherited key along with the hook path, so any fixture that needs to sign now has no key
unless one is configured locally.

**Fix verified correct by direct repro, not by reading claims:** temporarily removed the
`configureSigningKey(t, dir)` call from `TestSign_ResignsTipCommitWithIdenticalTree` (the
only line touched, isolation left in place) and reran:
```
go test ./internal/cli/... -run '^TestSign_ResignsTipCommitWithIdenticalTree$' -v -count=1
```
-> `FAIL`, `status=internal exit=90`, error
`internal.git.resign_failed ... gpg: skipped "Test User <test@example.com>": No secret key`.
This is the exact failure mode the report describes and confirms the dependency is real,
not hypothetical. Reverted the edit (`git status --short` clean afterward, confirming exact
restoration) and reran the same test: `PASS` (0.61s), confirming the shipped
`configureSigningKey` fix resolves it.

**Fix is not a workaround:** `configureSigningKey` generates a fresh, ephemeral ssh key
under the test's own `t.TempDir()` (via `ssh-keygen`), writes a matching
`gpg.ssh.allowedSignersFile`, and points local `user.signingkey`/`gpg.format` at it — no
change to `sign`/`resign`/`tag create`/`merge`/`Complete`'s own logic. It replaces a
previously-invisible dependency on the operator's actual personal signing key with a
disposable per-test key, which is strictly better hygiene (tests no longer touch real key
material) and does not mask any behavior of the code under test — confirmed by the repro
above showing the *fixture*, not the implementation, is what needed the key.

**Unrelated to the isolation change itself:** the dependency predates this task — it always
existed once a fixture needed to sign something, it was only ever masked by the host's own
config bleeding through. The isolation change is what *exposed* it (by correctly removing
config inheritance), not what *caused* it. This matches "pre-existing gap the isolation
happened to expose" rather than a regression introduced by the isolation.

`signableScratchRepo` (`worktree-gate/lifecycle/helpers_test.go:17-38`) was independently
read and confirmed to call `newScratchRepo(t)` internally, so `TestComplete_*`'s switch from
`newScratchRepo` to `signableScratchRepo` correctly inherits the env isolation as well as
gaining a real ephemeral key — no double-isolation bug, no missed isolation.
`go test ./worktree-gate/lifecycle/... -run '^TestComplete_' -v -count=1` -> all 14
subtests `PASS`, wall 1.718s.

## 6. Second full-suite run (flake check)

```
go test ./... -count=1
```
Run 2: all 10 packages `ok`, 0 failures, wall `1:46.50` (`45.73s user 45.91s system 86%
cpu`). Per-package times essentially identical to run 1 (`internal/cli` 39.525s vs 39.533s,
`internal/gitexec` 106.021s vs 106.053s, `internal/worktreeclean` 41.240s vs 41.251s,
`worktree-gate/lifecycle` 4.260s vs 3.959s). Two consecutive fresh runs, consistent timing,
zero failures on either — no flake observed.

## Static checks

- `go build ./...` — clean
- `go vet ./...` — clean
- `gofmt -l` on all 8 touched Go test files — no output (already formatted)

## Scope note (matches report, independently confirmed)

`internal/gitexec/gitexec_test.go`'s `scratchRepo` fixture has the identical unisolated
global-config gap (confirmed via grep: no `GIT_CONFIG_GLOBAL`/`GIT_CONFIG_SYSTEM` anywhere
in that file) and was correctly left untouched — out of this task's stated scope
(`internal/cli` and `worktree-gate` only). It shows up as the largest single package time
in both full-suite runs (106s), consistent with still inheriting the host hook. A follow-up
candidate, not a defect in this change.

## Coverage / gaps

No new test files were added or needed — this is an infrastructure/fixture change, not a
behavior change, and the existing suites (already comprehensive per prior task reports)
are the correct instrument to verify it: their pass/fail state and wall time are exactly
the observable effects of this change. The adversarial angle exercised here is the
signing-key repro in check 5, which is the one place this change could plausibly have
introduced a real regression (a fixture that now can't resolve a key it used to get for
free) — reproduced, confirmed, and confirmed fixed correctly.

## Failures

None. All runs clean across two independent full-suite passes plus targeted reruns.
