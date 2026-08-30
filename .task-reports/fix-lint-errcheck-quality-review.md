# Quality review: fix-lint-errcheck

**Verdict: FIX-APPLIED** — code accepted unchanged; the only edits were factual
corrections to `.task-reports/fix-lint-errcheck-report.md` (see Fixes applied).

Branch `chore/fix-lint-errcheck` @ `7e1eb79`, reviewed in worktree
`.claude/worktrees/fix-lint-errcheck`.

## Change under review

Two lines, both deliberate best-effort cleanups whose errors have no actionable
consumer:

- `internal/cli/integration_test.go:25` — `os.RemoveAll(cliBinDir)` → `_ = os.RemoveAll(cliBinDir)`
- `internal/commitmsg/commitmsg.go:69` — `defer os.Remove(tmp.Name())` → `defer func() { _ = os.Remove(tmp.Name()) }()`

Diff scope confirmed: exactly those 2 code lines plus the task report. No scope
creep, no `//nolint` directives anywhere in the module — the fix is a genuine
idiomatic suppression, not a lint-directive bypass.

## Base state: the dispatch's "1 commit behind" premise is stale

**No rebase was performed, because none was possible or needed.** After
`git fetch origin`:

- `origin/main` = `72e5732` = this branch's merge-base, byte-identical
- `git rev-list --left-right --count origin/main...HEAD` → `0  2` (0 behind, 2 ahead)
- `git merge-base --is-ancestor origin/main HEAD` → true

The branch is a strict fast-forward from the current remote tip. A rebase onto
`origin/main` is a provable no-op, so the acceptance evidence below already *is*
against the branch's true landing state. `main` in the primary checkout also sits
at `72e5732`, so nothing had landed past the fork point.

## Findings

### Blocking
None.

### Major
None.

### Minor

1. **`.task-reports/fix-lint-errcheck-report.md` (3 places, now corrected)** — the
   report asserted "No existing repo pattern for ignored cleanup errors of this
   exact shape existed." That is false, and it is the one claim the dispatch
   asked me to adjudicate. `worktree-gate/lifecycle/lock.go:50` is
   `defer func() { _ = fl.Unlock() }()` — the same shape, the same single-line
   form, the same purpose. Left uncorrected, a future reader doing repo-dialect
   archaeology would conclude no precedent exists when one does. Corrected;
   code untouched.

## The idiom question: anonymous func vs named helper

**Judgment: the inline anonymous func is correct as written. A named helper is
not warranted.** Reasoning, in descending weight:

1. **Exact in-repo precedent.** `worktree-gate/lifecycle/lock.go:50` already
   spells this shape identically. So the fix is not merely "idiomatic Go in the
   abstract" — it matches how *this* codebase already writes a deferred
   best-effort cleanup with a discarded error. Matching an existing single-line
   precedent is the whole test, and it passes.
2. **`_ =` is the established local dialect** for a discarded error, at five
   non-test sites: `reap.go:78,117,145` and `detect/hook.go:72,84`. The fix
   speaks the dialect already in use.
3. **A named helper would be strictly worse here.** It would add indirection for
   a one-line stdlib call at a single call site; it would move the suppression
   somewhere less visible (errcheck still needs the `_ =` *inside* the helper, so
   the suppression does not disappear, only hides); and it would obscure at the
   call site precisely *what* is being discarded — the property you most want
   local and greppable in a lint suppression. Note the repo declined to wrap even
   at the second occurrence of the shape (`lock.go`).
4. **No alternative spelling exists.** `defer _ = os.Remove(...)` is a compile
   error — `defer` requires a call expression, and `_ = expr` is a statement. The
   only other option, `defer func() { if err := ...; err != nil { log } }()`,
   would invent error-reporting semantics the task forbids and that neither
   `TestMain` (already past `m.Run()`, about to `os.Exit(code)`) nor `Check` (no
   channel left to report to) has anywhere to send.

**No explanatory comment added, deliberately.** Surrounding density does not call
for one: of the six pre-existing discard sites, five carry no comment
(`lock.go:50`, `reap.go:117,145`, `hook.go:72,84`); the one that does
(`reap.go:78`) explains a non-obvious *reason* to call `worktree prune` at all.
Both sites here are self-evident, and `integration_test.go:25` already sits under
a `TestMain` doc comment that explains the cleanup and why it lives outside
`t.TempDir()`. Adding more would exceed local density.

## Re-verification

CI's lint gate is `golangci-lint-action@v8.0.0` pinned to **v2.12.2 with no
config file** in the repo (confirmed: no `.golangci.*` anywhere), i.e. the v2
default linter set — errcheck enabled, test files linted, `check-blank: false`
(which is exactly why `_ =` satisfies it).

| Gate | Result |
| --- | --- |
| `gofmt -l internal/` | clean (no output) |
| `gofmt -l .` (whole repo) | clean (no output) |
| `go build ./...` | pass |
| `go vet ./...` | pass |
| `golangci-lint run ./...` | **0 issues** |
| `go test -count=1 ./internal/cli/... ./internal/commitmsg/...` | `ok` 23.171s / `ok` 0.147s |
| `go test -timeout 20m ./...` (full, CI-equivalent) | **all 10 packages `ok`, exit 0** |

Toolchain: go1.27.0, local golangci-lint **2.13.2** vs CI's pinned **2.12.2**
(see Residual risk).

### The fix is load-bearing, not a hollow green

A green tree alone would not prove the two edits did the work — so I reverted
both lines in place and re-ran the linter. The reverted tree reproduces exactly
the two reported failures, at exactly the reported `file:line`:

```
internal/cli/integration_test.go:25:15: Error return value of `os.RemoveAll` is not checked (errcheck)
internal/commitmsg/commitmsg.go:69:17: Error return value of `os.Remove` is not checked (errcheck)
2 issues:
* errcheck: 2
```

Restored to `HEAD` afterward; tree confirmed clean. Two consequences worth
recording: the errcheck gate genuinely does lint test files (so the
`integration_test.go` fix is real, not decorative), and those two were the
**only** errcheck issues in the entire module — which answers "was any other
errcheck-shaped issue introduced or missed" module-wide rather than merely
nearby.

## Correctness review beyond the lint gate

- `internal/commitmsg/commitmsg.go` — no fd leak: `tmp.Close()` at line 71 runs
  unconditionally on every path after `CreateTemp` succeeds, with no early return
  between lines 69 and 71. The deferred `os.Remove` firing after `Close` is
  correct on all platforms. Discarding is right: `Check` has already committed to
  its return value by the time the defer runs.
- `internal/cli/integration_test.go` — `cliBinDir` is tracked separately from
  `cliBinPath` precisely so `TestMain` still removes the directory when the build
  itself fails; that design is intact. Discarding is right here too, and stronger
  than the alternative: surfacing a teardown error after `m.Run()` could only
  mask or contradict the real test result.

## Test-suite assessment

**Adequate, and no new tests are warranted.** This change has no observable
behavior to test — it discards two error values that were already being
discarded implicitly. The meaningful verification is the linter itself, which I
exercised in both directions (fails before, passes after). The existing suites
serve their purpose here as a no-regression check, and they pass, including
`internal/cli`, which is `commitmsg`'s end-to-end consumer. Asking the
test-engineer for a test that asserts "an error was ignored" would be
gold-plating and would pin down an implementation detail.

## Residual risk

- **Linter minor-version drift (low).** Verified locally on golangci-lint 2.13.2;
  CI pins 2.12.2. Same v2 major, hence the same default linter set, and the blank
  identifier is errcheck's canonical, long-stable suppression — recognized
  identically across v2 minors. Residual, not eliminated: I cannot run 2.12.2
  here.
- Because CI runs errcheck at its defaults, `check-blank` and
  `check-type-assertions` stay off. Unchecked type assertions elsewhere in the
  module would therefore not be caught by this gate. Not a defect in this change,
  and out of its scope — noted only so the gate's actual coverage is on record.

## Plan feedback

1. **The dispatch's base-state premise was wrong.** It stated the branch was "1
   commit behind main's actual current tip" and instructed a rebase first. It was
   0 behind and 2 ahead of a freshly fetched `origin/main`. Whatever produced that
   claim was reading stale or mismatched refs. Harmless here — the corrective
   action was a no-op — but the same wrong premise on a branch that *had* drifted
   would send a reviewer into an unnecessary and potentially conflict-generating
   rebase. Worth checking `rev-list --left-right --count` before asserting drift
   in a dispatch.
2. **Report-vs-repo claims deserve a cheap check.** The implementer's "no
   precedent exists" claim was the one thing escalated for judgment, and it was
   falsifiable with a single `grep` for `defer func() { _ =`. A "grep before
   asserting absence" habit at the implementer stage would have resolved this
   without a round trip — and would have let the implementer cite `lock.go:50` as
   positive justification rather than framing the fix as an unguided fallback.
