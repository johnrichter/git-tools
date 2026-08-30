# D6 residuals: LED-153 (redirect-target half) and LED-160

Scope: the two smaller D6 items left after this session's main D6 fixes
(LED-023's operand-as-path default, the bare-flag `git --version` fix) already
shipped in commits `870d4fe` and `38a426a`. No code change resulted from
either item below; both are precise non-fixes, not oversights.

## LED-153 — shell-variable redirect target

**Status: confirmed correct, no fix needed in this repo. Any real fix is
out of scope here.**

The ledger's own ticket names two halves. Both are already resolved as far
as `git-tools`' own worktree gate can resolve them:

1. The read-argument half (`"$PROJ/plan.json"` misread as a write
   destination because the calling word, `"$DAT_TOOLS"`, was unrecognized).
   Already fixed by commit `870d4fe` this session — `namedPaths` in
   `worktree-gate/detect/decide.go` only judges non-flag operands as
   candidate write targets for a command `pathOperandCommand` names as a
   modeled writer; an unrecognized command's operands are no longer treated
   as paths at all. Covered by
   `TestLED153_VariableNamedToolArgumentNoLongerMistakenForPath` in
   `worktree-gate/detect/decide_led023_led153_test.go` (passing).

2. The redirect-target half (`> "$PROJ/plan.md"` itself is an unresolved
   shell variable). This still denies, and it is meant to: `namedPathDenial`
   (`worktree-gate/detect/decide.go:419-429`) applies `isUnexpandable` to
   every real redirect target `outputRedirectTargets(p.raw)` returns
   (`decide.go:548-549`), and denies outright on SC5's precedent — the gate
   cannot rule out a primary-checkout landing for a target it cannot
   statically resolve. This is fail-closed by design, confirmed by the same
   test file's second assertion (denies, reason contains "cannot resolve
   statically"). There is no static-analysis fix available here: a shell
   variable's value is not known until runtime, and the gate deliberately
   never executes the command it is screening.

**What would actually close this ticket, and where:**

- Change the `plan-with-team`/`build-with-team` SKILL.md helper-call examples
  to a literal absolute redirect target instead of a shell variable. Those
  files live in the `delivery-agent-team` plugin/repo, not `git-tools` or
  this worktree. Out of scope here.
- Or add an explicit `--out FILE` flag to `dat-tools`' `render`,
  `state-init`, `state-render` subcommands, so no shell-redirect variable is
  ever a call's own operand. `dat-tools` is a different plugin's binary,
  not `git-tools`'. Out of scope here.

No file in this repo needs to change for LED-153. The classifier-side fix
already landed; the remaining behavior is correct, intentional, fail-closed
denial that only a change in a different repo's skill text or a different
plugin's binary can avoid.

**Evidence:**
- `worktree-gate/detect/decide.go:419-429` (`namedPathDenial`, the
  `isUnexpandable` check and its deny message).
- `worktree-gate/detect/decide.go:548-575` (`namedPaths`, showing
  `outputRedirectTargets` always contributes the real redirect target,
  independent of whether the command word is recognized).
- `worktree-gate/detect/decide_led023_led153_test.go:93-124`
  (`TestLED153_VariableNamedToolArgumentNoLongerMistakenForPath`), run via
  `go test ./worktree-gate/... -run LED153 -v`: PASS.

## LED-160 — long `--note` text misread as an unresolvable write path

**Status: not reproduced against `git-tools`' own worktree gate, in the
current revision or any prior one found in this repo's history. The
ledger's own "Affects" line names `governance-git`, not `git-tools` — if
this is real, it is very likely a different repo's finding, not this one's.**

Claimed mechanism (ledger text): a `dat-tools record`/`log-note` call
carrying a long (~300+ character) free-text `--note` value is denied, with
the gate reportedly concatenating the note text with the target
`execution.json` path and reporting that concatenation as an unresolvable
write target, `ENAMETOOLONG`.

**Reproduction attempts** (scratch test against `Decide()`, not committed,
removed after use):

- `dat-tools log-note /repo/wt/execution.json --note "<~400 chars>"` from a
  worktree cwd: **allowed**, no denial, any note length.
- `dat-tools record /repo/wt/execution.json --note "<~400 chars>"` from a
  worktree cwd: **allowed**, same.
- The same command from a primary-checkout cwd: **denied**, but with the
  generic "cannot classify ... so the gate cannot rule out a write" message
  — the same denial a one-character note would get. No `ENAMETOOLONG`, no
  concatenation, and the denial has nothing to do with note length.

**Why the claimed mechanism cannot occur here.** `dat-tools` is not
`git`, not `git-tools`, and not in `verbs.json`'s `write_prefixes`/
`write_contains`, so `classifyPiece` (`worktree-gate/detect/bash.go:488-537`)
always falls through to `ClassUncertain` — checked against every commit
touching that function's fallback, back to the very first commit
(`95c6c68`); the default has always been `ClassUncertain`, never `ClassWrite`.
`namedPathDenial` (SC20), the only code path that resolves an argument
against a filesystem path (`filepath.Join` plus `lstat`, where a real OS
`ENAMETOOLONG` could actually surface), only ever runs against a piece
already classified `ClassWrite` (`decide.go:408-419`, called from
`scanBash`). An `Uncertain`-classified piece never reaches it. So there is
no path inside this gate's own logic, in any revision checked, where a
`--note` value's length or content could reach the code that does static
path resolution.

I also checked `governance-git`'s own `pretooluse-worktree-gate.sh`
wrapper directly (`marketplace/plugins/governance-git/hooks/
pretooluse-worktree-gate.sh`) — it is a pure checksum-verification exec
wrapper, by its own comment "This wrapper never inspects the payload," and
delegates all classification to this same Go binary. It carries no
independent fallback classifier of its own that could produce this
mechanism either.

**Conclusion.** Cannot reproduce, and cannot locate a code path in this
repo capable of producing the claimed symptom. The ledger's own "Affects"
line names `governance-git`, a different repository/plugin surface, so if
the observation is real, tracking it further belongs there, or against
whatever tool actually issued the `dat-tools` call (`dat-tools` itself, a
different plugin's binary, not `git-tools`'s). No fix applied here; this is
recorded as an honest non-reproduction, not a fabricated fix.

## Files touched

None. No production code changed. A scratch reproduction test
(`worktree-gate/detect/zzled160_scratch_test.go`) was created and run, then
deleted before this report was written — it never appears in `git status`.

## Test results

- `go build ./...`: PASS (no diagnostics).
- `go vet ./...`: PASS (no diagnostics).
- `go test ./worktree-gate/...`: PASS —
  `github.com/johnrichter/git-tools/worktree-gate/detect` (6.4s),
  `.../worktree-gate/fixtures` (0.0s), `.../worktree-gate/lifecycle`
  (147.3s). No test file changed, so this is a baseline confirmation, not a
  regression check.
- `internal/cli`'s suite was not rerun: no file under `internal/cli`, or
  any file this task touched, changed at all, so there is nothing for that
  suite to newly verify for this task.

## Assumptions

- LED-153's own ledger text already commits to one of two out-of-repo
  fixes ("Change the SKILL helper-call examples... or add an explicit
  output-path flag"); this task took the dispatch's own instruction at
  face value and did not pick one on `delivery-agent-team`'s or
  `dat-tools`' behalf, since both live outside this repo and this task's
  scope.
- LED-160's reproduction used the exact command shape and rough note
  length the ledger describes, run against `Decide()` directly with a
  synthetic filesystem (the same harness the existing LED-023/LED-153 tests
  use), covering both a worktree and a primary-checkout cwd. A real
  end-to-end run through the shipped `worktree-gate` binary was not
  performed, since the direct `Decide()` call already conclusively shows
  no code path can reach the claimed failure mode from any classifier
  branch.

## Hand-off notes

- No code changed, so there is nothing to differentially test for the
  full-corpus rule this project's own D6 lesson calls for; that lesson
  applies to a rule that narrows a fail-closed catch-all, and neither item
  here changed one.
- If a future task ever adds `dat-tools` (or any comparably-shaped helper)
  to `verbs.json`'s write sets so its operands get judged, re-run LED-160's
  exact reproduction shape then: adding a write-prefix/contains entry
  without an accompanying flag-value-aware operand parser is exactly the
  mechanism that would newly make LED-160's claim possible.
- If LED-153 is ever picked up as a real task, it belongs to a
  `delivery-agent-team` (SKILL.md text) or `dat-tools` (new `--out` flag)
  ticket, not a `git-tools` one — do not re-open it here again without a
  new, `git-tools`-specific angle.
