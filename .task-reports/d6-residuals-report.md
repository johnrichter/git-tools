# D6 residuals: LED-153 (redirect-target half) and LED-160

Scope: the two smaller D6 items left after this session's main D6 fixes
(LED-023's operand-as-path default, the bare-flag `git --version` fix) already
shipped in commits `870d4fe` and `38a426a`. Neither item needs a production
code change: LED-153's remaining half is correct fail-closed behavior, and
LED-160 turned out to be the same defect `870d4fe` already fixed, now given
its own regression test.

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

**Status: real, and this repo's own defect — already fixed by commit
`870d4fe` as a side effect of LED-023. It reproduces exactly, with the
ticket's own length-dependence, against the immediately preceding revision
(`b9d99db`), and no longer reproduces at HEAD. No further code fix is
needed here; the regression is now pinned by a test, and the ledger entry
should be closed as fixed rather than reassigned to another repo.**

Claimed mechanism (ledger text): a `dat-tools record`/`log-note` call
carrying a long (~300+ character) free-text `--note` value is denied, with
the gate concatenating the note text with the target `execution.json` path
and reporting that concatenation as an unresolvable write target,
`ENAMETOOLONG`.

**Actual mechanism.** The trigger the first reproduction attempt missed is
that the call must be write-class *before* its operands are read, and a
`dat-tools` call is not write-class from its command word — it becomes
write-class from a shell redirect of its own output (`> …`), which sets
`piece.writesFile` and short-circuits `classifyPiece`. Once write-class,
the pre-`870d4fe` unmodeled-command default in `namedPaths` judged *every*
non-flag operand as a candidate write target, so the `--note` value was
joined onto the cwd (`filepath.Join(cwd, note)`) and `lstat`ed. Past
`NAME_MAX` (255 bytes) a real filesystem answers `ENAMETOOLONG`, which
`namedPathDenial` reads as an indeterminate target and denies fail-closed.
This is the same root cause as LED-023 and LED-153, exactly as the ledger's
own "Related family: LED-023" line suggests.

The synthetic-filesystem probe used in the first attempt could not have
found this: a fake `lstat` answers `ENOENT` for the joined path however
long it is, `FindRepoRoot` treats that as "keep climbing", the walk-up
reaches the worktree's own `.git`, and the target classifies `KindWorktree`
and is allowed. Only a real errno separates the two behaviors. A
length-dependent denial whose signature *is* an OS errno cannot be ruled
out on a filesystem that has no length limit.

**Reproduction, end-to-end through the real binary** (built from each
revision, run against a real primary-plus-worktree topology, cwd inside the
worktree, command `dat-tools log-note <wt>/state.json --note "<n chars>" >
<wt>/out.txt`):

| note length | `b9d99db` (pre-fix) | HEAD (`33f6e9e`) |
| --- | --- | --- |
| 100 / 150 / 200 / 253 / 254 | allow | allow |
| 300 | **deny** — `lstat <wt>/<note text>/.git: file name too long` | allow |
| 353 | **deny** — same | allow |

That is the ticket verbatim: identical call, denied above roughly 255
characters, allowed below, with the note text appearing inside the reported
write-target path.

**Why nothing else in the chain is implicated.** `dat-tools` is absent from
`verbs.json` (no `dat` substring anywhere in the file), so `classifyPiece`
(`worktree-gate/detect/bash.go:488-537`) reaches `ClassUncertain` from the
command word alone — which is why a redirect-free `dat-tools … --note …`
call is allowed from a worktree cwd at every note length in both revisions,
and denied from a primary-checkout cwd on the generic cwd leg regardless of
note length. `governance-git`'s `pretooluse-worktree-gate.sh` is a pure
checksum-verification exec wrapper ("This wrapper never inspects the
payload") that delegates all classification to this same Go binary, so the
ledger's `governance-git` "Affects" attribution names the shipping surface,
not the defective code; the defect was here.

**Regression cover added.** `worktree-gate/detect/decide_led160_test.go`
pins the fixed behavior against the reported shape: the long note and a
short note must both be allowed from a worktree cwd, with the
`ENAMETOOLONG` errno registered on exactly the over-`NAME_MAX` target.
Differentially verified — the long-note subtest fails against `b9d99db`
with the message above while the short-note subtest passes, so the test
distinguishes fixed from unfixed rather than passing vacuously. LED-023's
own cases cannot pin this: their operands resolve inside the worktree and
are allowed either way.

## Files touched

- `worktree-gate/detect/decide_led160_test.go` — new, test only. Pins
  LED-160's reported shape against the fix already in `870d4fe`.

No production code changed.

## Test results

- `go build ./...`: PASS.
- `go vet ./...`: PASS.
- `go test ./worktree-gate/...`: PASS, including the new LED-160 test.
- `internal/cli`'s suite was not rerun: nothing under `internal/cli`
  changed, and the added file is a test in `worktree-gate/detect`.
- Differential check on the new test against `b9d99db` (`870d4fe`'s parent):
  the long-note subtest FAILS, the short-note subtest PASSES — the test
  discriminates fixed from unfixed.

## Assumptions

- LED-153's own ledger text already commits to one of two out-of-repo
  fixes ("Change the SKILL helper-call examples... or add an explicit
  output-path flag"); this task took the dispatch's own instruction at
  face value and did not pick one on `delivery-agent-team`'s or
  `dat-tools`' behalf, since both live outside this repo and this task's
  scope.
- LED-160's verification ran end-to-end through the built `worktree-gate`
  binary against a real filesystem, from both a worktree and a
  primary-checkout cwd, at HEAD and at `870d4fe^`. A real filesystem is
  required, not optional: the synthetic-filesystem harness the unit tests
  use models no `NAME_MAX`, so it cannot raise the errno that is this
  defect's entire signature.

## Hand-off notes

- LED-160 should be closed in the ledger as fixed by `870d4fe`, with its
  `governance-git` "Affects" line corrected to `git-tools` — the wrapper
  named there carries no classifier logic. Both independent filings
  (toolchain-conformance FB10, ste-detector-and-voice-hardening FB15)
  describe this same mechanism.
- Method lesson worth carrying: a symptom whose signature is an OS errno
  cannot be ruled out on a synthetic filesystem that models no errnos. Probe
  the built binary against a real topology before recording a
  non-reproduction.
- If a future task ever adds `dat-tools` (or any comparably-shaped helper)
  to `verbs.json`'s write sets, its operands start being judged again;
  adding a write-prefix/contains entry without a flag-value-aware operand
  parser reopens LED-160 directly. The new test then becomes the guard that
  catches it.
- If LED-153 is ever picked up as a real task, it belongs to a
  `delivery-agent-team` (SKILL.md text) or `dat-tools` (new `--out` flag)
  ticket, not a `git-tools` one — do not re-open it here again without a
  new, `git-tools`-specific angle.
