# Isolate remaining test setup routines from host git config/hooks

## Scope

Applied the internal/cli / worktree-gate isolation pattern (`t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)`
plus `t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)`, set immediately before the first real git invocation in
each setup routine) to the four packages named in the task:

- `internal/gitexec`
- `internal/worktreeclean`
- `internal/signing`
- `internal/hooks`

## Enumeration method

Ran `grep -rln 'exec.Command("git"' --include='*_test.go' .` across the whole repo, then checked each
hit for existing `GIT_CONFIG_GLOBAL`/`GIT_CONFIG_SYSTEM` isolation. Hits outside the four named packages
(`internal/commitmsg`, `worktree-gate/detect`, `worktree-gate/lifecycle`) are out of task scope and were
left untouched. `worktree-gate` is already isolated per the task's own note. `internal/commitmsg` is not
one of the four named packages and was not touched.

## Files touched (all test-only)

- `internal/gitexec/gitexec_test.go` — `scratchRepo` (a shared repo-building routine, also used
  unmodified by `probe_adversarial_test.go`, which builds no repo of its own).
- `internal/gitexec/gitignore_test.go` — two inline git-init sites:
  `TestIsIgnoredByCommittedGitignore_PathTraversalRelPathIsError` and
  `TestIsIgnoredByCommittedGitignore_UnbornHeadIsError`.
- `internal/worktreeclean/worktreeclean_test.go` — `cleanupFixture`.
- `internal/signing/signing_test.go` — `scratchRepo`.
- `internal/hooks/hooks_test.go` — `initScratchRepo`.

No production code was touched in any package.

## Build-ordering caution (checked, not applicable)

Checked all four packages for a repo-building routine that runs `go build` on the CLI under test
before/around its git setup (the specific hazard called out in the task, where nulling GIT_CONFIG_* too
early could break a build that legitimately needs the host's `safe.directory` or similar). None of the
four packages build a CLI binary as part of any test setup. `grep -n "go build\|exec.Command(\"go\""
internal/gitexec/*.go internal/worktreeclean/*.go internal/signing/*.go internal/hooks/*.go` returned no
hits. The before-build-call ordering concern does not apply here. Isolation env is set at the very top of
each routine, before any git subprocess.

## internal/signing apply-resign coverage question

Checked, per the task's caution. `internal/signing/signing_test.go`'s own file header states directly
that the paths needing a working signing key (resigned/would_resign) are exercised end to end by the
merge verb's own suite. Confirmed against `internal/signing/signing.go`. The apply branch
(`repo.Resign(ctx, ref, git.ResignOptions{Base: base})`, setting `ActionResigned`) and the dry-run branch
(setting `ActionWouldResign`) are both real, but this package's own test file never provisions a signing
key. Neither branch is reached from within `internal/signing`'s own suite. Isolating the package's setup
routine is still correct because it removes a host dependency regardless. It does not change what this
package's own tests cover. The apply-resign branch remains covered only through `internal/cli`'s
merge-verb suite, the same as before this change.

## Verification

```
gofmt -l internal/gitexec internal/worktreeclean internal/signing internal/hooks   # no output
go build ./...                                                                     # clean
go vet ./...                                                                        # clean
```

`go test ./internal/gitexec/... ./internal/worktreeclean/... ./internal/signing/... ./internal/hooks/... -count=1 -v`
— all tests pass.

Ran the same four packages' tests three times fresh (`-count=1`, no cache) to confirm stability: all
three runs passed with consistent, sub-second-per-package timings and no flake.

## Before / after per-package test time

Measured with `time go test ./internal/<pkg>/... -count=1` (the host has a global git hook that fired on
every fixture-file commit before this change):

| Package                  | Before    | After (avg of 3 runs) | Saved     |
|---------------------------|----------:|-----------------------:|----------:|
| `internal/gitexec`        | 106.437s  | ~0.98s                  | ~105.5s   |
| `internal/worktreeclean`  | 41.504s   | ~0.76s                  | ~40.7s    |
| `internal/signing`        | 17.363s   | ~0.21s                  | ~17.2s    |
| `internal/hooks`          | 0.077s    | ~0.04s                  | ~0.04s    |
| **Total**                 | **165.4s**| **~2.0s**               | **~163.4s** |

`internal/hooks`'s repo-building routine (`initScratchRepo`) only runs `git init`, never a commit, so it
was never exposed to the host's commit hook. Isolation was applied there for consistency, not for time,
matching the task's own expectation.

## Assumptions

- Matched the exact idiom already used in `internal/cli`/`worktree-gate`: `t.Setenv` (not raw
  `os.Setenv`) in every case, because every routine touched here is itself a `*testing.T`-scoped helper
  function. No case required the `os.Setenv` variant.
- `internal/gitexec/probe_adversarial_test.go` needed no direct edit. It only calls `scratchRepo`,
  `runGitOrFatal`, and `writeFileOrFatal`, all defined in the two files already isolated in this change.
