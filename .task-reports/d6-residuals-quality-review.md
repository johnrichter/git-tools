# Quality review: D6 residuals report (LED-153, LED-160)

- **Verdict: FIX-APPLIED.**
- **Reviewed:** `.task-reports/d6-residuals-report.md` at commit `33f6e9e`, branch `chore/d6-residuals`.
- **Outcome:** LED-153's conclusion holds exactly as written. LED-160's conclusion was wrong — the defect is real, is this repo's own, and was already fixed by `870d4fe`. It reproduces end-to-end against `870d4fe^` with the ticket's own length-dependence. Corrected the report and added the missing regression test.

## Check 1 — `isUnexpandable`/`namedPathDenial` fail-closed behavior: CONFIRMED

`worktree-gate/detect/decide.go:419-429` reads as claimed:

```go
for _, raw := range namedPaths(verbs, p) {
        t := stripQuotes(raw)
        if t == "" { continue }
        if isUnexpandable(t) {
                d := deny(fmt.Sprintf(
                        "%q names a write target this gate cannot resolve statically, so it cannot rule out a primary checkout", t), remedyLiteralTarget)
                return &d
        }
```

- `isUnexpandable` (`worktree-gate/detect/cwd.go:178-183`) returns true for a `~` prefix or any of `$`, backtick, `*`, `?`, `[`, `]`. `"$PROJ/plan.md"` → `stripQuotes` → `$PROJ/plan.md` → contains `$` → deny. The deny precedes every filesystem resolution, so no expansion is attempted.
- `namedPaths` (`decide.go:548-575`) starts unconditionally with `targets := outputRedirectTargets(p.raw)` and returns those targets in every branch, including the `default` for an unrecognized command word. So a redirect target is judged whether or not the command is modeled — which is what makes the LED-153 redirect half deny while its read-argument half does not.
- Live confirmation, real binary built from HEAD, cwd inside a real worktree:

| command | verdict |
| --- | --- |
| `"$DAT_TOOLS" render "$PROJ/plan.json" > <wt>/plan.md` | allow |
| `"$DAT_TOOLS" render "$PROJ/plan.json" > "$PROJ/plan.md"` | deny — `"$PROJ/plan.md" names a write target this gate cannot resolve statically` |
| `dat-tools render x > "$PROJ/out.md"` | deny — same reason |

Fail-closed by design, and correctly so: a shell variable's value is not knowable without running the command, which the gate never does. No static fix exists in this repo. Every line citation in the report's LED-153 evidence list is accurate (`decide.go:419-429`, `decide.go:548-575`, `decide_led023_led153_test.go:93-124`).

## Check 2 — commit `870d4fe`: CONFIRMED

Real commit, `870d4fe631bea8c8c426f512b016c591e3579321`, "Fix worktree-gate misattributing an unmodeled command's own operand as a write target". Touches `worktree-gate/detect/decide.go` (+51/-…), adds `decide_led023_led153_test.go` (124 lines), updates `decide_sc23_gitignore_test.go` and the bash corpus fixture. Its message names LED-023 and LED-153 explicitly and describes exactly the change the report attributes to it: only `pathOperandCommand`-qualified commands judge their own operands; every other unmodeled command is judged by redirect targets alone. `TestLED153_VariableNamedToolArgumentNoLongerMistakenForPath` passes at HEAD.

## Check 3 — `dat-tools` in `verbs.json`: CONFIRMED ABSENT

`worktree-gate/detect/verbs.json` (71 lines) holds three keys: `read_prefixes` (15), `write_prefixes` (39), `write_contains` (9). A case-insensitive search for `dat` returns no match at all. `classifyPiece` (`worktree-gate/detect/bash.go:488-537`) therefore cannot reach `ClassWrite` for `dat-tools` from its command word.

**Correction to the report's reasoning, not to this fact:** command-word classification is not the only route to `ClassWrite`. `classifyPiece`'s first statement is `if p.writesFile { return ClassWrite }` — a shell redirect classifies the piece write *before* the verbs model is consulted at all. That is the branch the report's LED-160 analysis overlooked, and it is the branch that makes LED-160 reachable.

## Check 4 — live scratch probe: LED-160 REPRODUCES against `870d4fe^`

Probe method (stronger than the report's): built the real `worktree-gate` binary from each revision and fed it real hook payloads against a real filesystem topology (`/tmp/led160/primary/.git` a directory, `/tmp/led160/wt/.git` a `gitdir:` redirect naming `…/.git/worktrees/wt`). A real filesystem is required — the synthetic `fakeFS` the report used models no `NAME_MAX`, and `ENAMETOOLONG` is this defect's entire signature.

At HEAD (`33f6e9e`), 24 cases (`log-note`/`record`/`"$DAT_TOOLS" log-note`/redirect form × note lengths 2, 353, 396 × worktree and primary cwd):

- Worktree cwd: **allow** in all 12 cases, every note length.
- Primary cwd: deny in all 12, on the generic cwd leg, with an identical message for a 2-character note. No `ENAMETOOLONG`, no concatenation.

This matches the report's HEAD claim. But the same probe against `870d4fe^` (`b9d99db`), cwd inside the worktree, command `dat-tools log-note <wt>/state.json --note "<n chars>" > <wt>/out.txt`:

| note length | `b9d99db` | HEAD |
| --- | --- | --- |
| 100 / 150 / 200 / 253 / 254 | allow | allow |
| 300 | **deny** — `cannot determine whether the write target "<wt>/<note text>" is inside a git repository (lstat <wt>/<note text>/.git: file name too long)` | allow |
| 353 | **deny** — same | allow |

That is LED-160 verbatim: the note text folded into a write-target path, `ENAMETOOLONG`, denied above roughly 255 characters and allowed below, identical call either way. The ledger's "shortening the note to under about 150 characters lets the identical command succeed" is the same threshold observed from the caller's side.

Mechanism: the redirect sets `piece.writesFile` → `ClassWrite` → pre-`870d4fe` `namedPaths` judged every non-flag operand → `filepath.Join(cwd, note)` → `lstat` → `ENAMETOOLONG` → `namedPathDenial` fails closed on an indeterminate target. Same root cause as LED-023 and LED-153, exactly as the ledger's own "Related family: LED-023" line indicated.

## Check 5 — LED-160's "Affects" line: CONFIRMED as quoted, but attribution is wrong

`/home/bits/Development/workspaces/psa-platform/workspace/.dat/ledger.md:2723` (read-only, not modified):

> **Affects:** governance-git — the worktree gate's unrecognized-command fallback classifier · **Impact 2 · Urgency 2 · Criticality 4**

The report quotes it correctly: it does name `governance-git`, not `git-tools`. The report's *inference* from it is wrong. The same line's own description — "the worktree gate's unrecognized-command fallback classifier" — is precisely `namedPaths`' unmodeled-command default in `git-tools`, the function `870d4fe` fixed. `governance-git` ships the gate; it does not implement it. Its `pretooluse-worktree-gate.sh` is a checksum-verification exec wrapper that never inspects the payload, as the report itself verified. So the "Affects" line names the shipping surface, and the ledger entry should be corrected to `git-tools` and closed as fixed.

## Findings

### Blocking (fixed here)

- `.task-reports/d6-residuals-report.md:64-118` (original) — LED-160 recorded as "not reproduced … in the current revision or any prior one found in this repo's history" and reassigned to `governance-git` or `dat-tools`. Both claims are false: it reproduces at `870d4fe^`, one commit back, and it was this repo's defect. The report's key inferential step — that `dat-tools`' absence from `verbs.json` means no `ClassWrite` classification is reachable — ignores `classifyPiece`'s `p.writesFile` short-circuit for a shell redirect. Left uncorrected, this sends a future session away from the correct repo, leaves an already-fixed ledger entry open, and leaves the fix unpinned.

### Major (fixed here)

- No test covered LED-160's shape. `decide_led023_led153_test.go`'s operands (jq/python3/awk program bodies, a `"$PROJ/plan.json"` read argument) resolve inside the worktree and are allowed with or without the fix, so none of them discriminates. A symptom filed independently twice, live one commit ago, had zero regression cover.
- Method: a non-reproduction recorded on a synthetic filesystem for a symptom whose signature is an OS errno is not evidence. `fakeFS` answers `ENOENT` for the joined path at any length, `FindRepoRoot` treats that as "keep climbing", and the walk-up reaches the worktree's own `.git` — so the pre-fix code *also* allows on `fakeFS`. The probe could not have failed, whatever the code did.

### Minor

- `.task-reports/d6-residuals-report.md` "Files touched" (original) claimed a scratch test file was created and deleted "before this report was written — it never appears in `git status`". Accurate but not verifiable after the fact; the differential probe it describes is the load-bearing claim and it did not hold up. Superseded by a committed test.

## Fixes applied

1. **`worktree-gate/detect/decide_led160_test.go`** (new, test only, no production code touched). Pins the fixed behavior against the reported shape: from a worktree cwd, `dat-tools log-note <wt>/state.json --note "<note>" > <wt>/out.txt` must be allowed for both a ~295-character note and a 2-character one, with `syscall.ENAMETOOLONG` registered on the fake filesystem for exactly the target whose final component exceeds `NAME_MAX`. Registering the errno only above the threshold is deliberate — registering it for the short note too would erase the length contrast the test rests on.
2. **`.task-reports/d6-residuals-report.md`** — rewrote the LED-160 section against the evidence: real defect, this repo's, fixed by `870d4fe`, with the redirect trigger, the reproduction table, and why the synthetic-filesystem probe could not see it. Updated the scope paragraph, Files touched, Test results, Assumptions, and Hand-off notes. Added the ledger-correction hand-off. LED-153's section is unchanged — it was correct.

## Re-verification

- `go build ./...`: PASS.
- `go vet ./...`: PASS.
- `gofmt -l` on the new file: clean.
- `go test ./worktree-gate/...`: PASS — `detect` 6.4s (includes the new test), `fixtures`, `lifecycle`.
- `go test ./worktree-gate/detect/ -run 'LED160|LED153|LED023' -v`: PASS, all subtests.
- `go test ./internal/gitexec/ ./internal/hooks/ ./internal/result/ ./internal/signing/ ./internal/worktreeclean/`: PASS.
- `go test ./internal/cli/`: PASS (1219s under host contention). Two earlier failures in this package during the review window were environment artifacts, not defects, and should be discounted in any log from that window: a `no space left on device` failure while `/tmp` sat at 100% (another session was concurrently running `go test ./...` in a sibling worktree), and a `tag_test.go:365 exit=41, want 40` failure caused by this review's own `TMPDIR` override, which put `t.TempDir()` inside a git worktree and so invalidated the "not a git working tree" premise of that subtest. Both are gone with the default `TMPDIR`; `TestTagCreate_ExitCodeTable` passes all six subtests standalone and in the full run.
- **Differential (the project's own D6 lesson):** the new test copied into a clean `870d4fe^` tree — long-note subtest **FAILS** with `cannot determine whether the write target "/repo/wt/<note text>" is inside a git repository (… file name too long)`, short-note subtest **PASSES**. The test discriminates fixed from unfixed and is not vacuous.

## Test-suite assessment

Adequate now for both items, with the gap closed here.

- LED-153's redirect half was already pinned by `decide_led023_led153_test.go:114-123` (denies, reason contains "cannot resolve statically"), and the live binary probe agrees.
- LED-160's shape was uncovered and is now pinned, differentially verified.
- Remaining gap, not blocking and out of this task's scope: no test exercises `piece.writesFile` as an independent route to `ClassWrite` for an *unmodeled* command word, which is the reachability fact the report's analysis missed. The new LED-160 test covers the one instance; the general property (a redirect classifies write ahead of the verbs model, so operand-judging policy governs every unmodeled command with a redirect) is worth a named test of its own if `namedPaths` is touched again.

## Residual risk

- The LED-160 fix is behavioral coverage narrowing an operand-judging default. `870d4fe` verified no verdict regressions across the full corpus; this review added no production change, so that verification still stands.
- The gate now allows a long free-text operand of an unmodeled command from a worktree cwd. That is intended: the operand is a flag value, not a path, and the redirect target — the real write signal — is still judged. A genuine write smuggled as a bare operand of a command the model does not name remains unjudged by SC20, which is `870d4fe`'s own disclosed trade-off, not a new one.
- The `.task-reports/` report file is the deliverable; the scratch topology under `/tmp/led160` and the probe scripts under `/tmp` are throwaway and uncommitted.

## Plan feedback

- **Ledger correction (`workspace`'s `.dat/ledger.md:2720-2731`):** LED-160 is fixed by `git-tools` commit `870d4fe`. Its `Affects` line should read `git-tools — the worktree gate's unmodeled-command operand-as-path default`, and its class should move from `open` to fixed, cross-referenced to LED-023 as the same root cause. Both filings (toolchain-conformance FB10, ste-detector-and-voice-hardening FB15) are the same defect. Not edited here — the ledger lives outside this worktree and this task had read-only access to it.
- **Method lesson worth filing:** a non-reproduction is only as strong as the harness's fidelity to the symptom's signature. A synthetic filesystem that models no errnos cannot rule out an errno-signature defect, and a passing probe against it proves nothing. For any reported gate denial, build the binary and probe a real topology before recording a non-reproduction. This is the general form of the D6 lesson about differential testing: verify the probe can fail.
- **LED-153 stands as scoped.** No `git-tools` change closes it. The real fix is a `delivery-agent-team` SKILL.md text change (literal absolute redirect targets in the helper-call examples) or a `dat-tools` `--out FILE` flag. The report's recommendation is correct and should be split into tickets on those two repos.
