# Task: fix-lint-errcheck

Fix two pre-existing golangci-lint `errcheck` failures breaking CI on main.

## What changed

- `internal/cli/integration_test.go:25` — `os.RemoveAll(cliBinDir)` → `_ = os.RemoveAll(cliBinDir)`.
  Best-effort teardown of a shared scratch binary directory in `TestMain`; a
  failure here has nothing meaningful to act on.
- `internal/commitmsg/commitmsg.go:69` — `defer os.Remove(tmp.Name())` →
  `defer func() { _ = os.Remove(tmp.Name()) }()`. Best-effort cleanup of a
  scratch temp file used to feed a commit-msg hook; deferred, so there's no
  caller left to report an error to.

No established repo-wide pattern for this exact shape (ignored cleanup
error) was found — other `os.Remove`/`os.RemoveAll` call sites in the repo
(`worktree-gate/lifecycle/reap.go`, `reap.go` tests, `activity.go`) all check
and propagate the error meaningfully, which doesn't apply here since these
two are deliberate best-effort cleanups. Used the minimal `_ = ...`
suppression called out as the fallback in the task instructions.

## Acceptance

- errcheck failure at `internal/cli/integration_test.go:25` fixed: met.
- errcheck failure at `internal/commitmsg/commitmsg.go:69` fixed: met.
- Minimal, idiomatic fix, no new error handling semantics: met.
- No other files touched: met.

## Sanity result

```
gofmt -l internal/          -> (no output, clean)
go build ./...              -> pass
go vet ./...                -> pass
golangci-lint run ./...     -> 0 issues.
go test ./internal/cli/... ./internal/commitmsg/... -count=1
  ok  github.com/johnrichter/git-tools/internal/cli         23.528s
  ok  github.com/johnrichter/git-tools/internal/commitmsg   0.184s
```

golangci-lint was available locally (installed in the environment) and ran
successfully — no need to defer to CI for confirmation.

## Assumptions & deviations

- No existing repo pattern for ignored cleanup errors of this exact shape
  existed to match, so used `_ = ...`, the fallback explicitly authorized by
  the task instructions.
- For the deferred call in `commitmsg.go`, wrapped in an anonymous func
  (`defer func() { _ = os.Remove(tmp.Name()) }()`) since `defer _ =
  os.Remove(...)` is not valid Go syntax — `defer` requires a function call
  expression, and `_ = expr` is a statement, not a call. This is the minimal
  idiomatic way to discard a deferred call's error.

## Hand-off notes

- Both fixes are cleanup-only; no behavior change to test for beyond
  confirming the existing test suites still pass (they do, see above).
- Quality reviewer: confirm the anonymous-func wrapping in commitmsg.go
  reads as idiomatic Go for this codebase and doesn't warrant a named
  helper (none exists elsewhere in the repo for this shape, so inline is
  consistent with there being no precedent to follow).
