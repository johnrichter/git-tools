# D4: tag create hardening — quality review

## Verdict: ACCEPT WITH FIXES

The implementation is sound: the three gaps D4 targeted are genuinely closed,
the tests are real and adversarial, and the test-engineer's PASS holds. Six
fixes applied, all in the diff's own scope. Full suite re-run fresh on the
final tree: exit 0, every package `ok`.

The test-engineer's exit-code finding is **confirmed and fixed**, with one
refinement they did not raise: the same branch's *triage directive* was also
wrong, and had to move with the exit code.

---

## 1. The exit-code decision

**Verdict: `precondition_unmet` (30) for verify-failed/rollback-succeeded.
`internal` (90) retained for verify-failed/rollback-also-failed.**

Confirmed independently against the real contracts, not the recommendation.

### Why 90 was wrong

Two independent sources rule it out:

- `internal/cli/root.go:157-160` — `finishErr`'s own doc comment: "an
  infrastructure failure from this CLI itself, **not a diagnostic the
  underlying tool reported**." A nonzero `git tag -v` is precisely a
  diagnostic the underlying tool reported.
- `clikit@v0.1.0/result.go:86-87` — `NewInternal`: "the tool itself failed,
  **or produced an outcome it cannot classify**." Neither half holds. The
  tool did not fail — it worked, and the answer was no. The outcome is
  fully classified: it has its own named code, its own message, and a
  correct automatic rollback.

### Why 30 is right

- `clikit@v0.1.0/result.go:42-43` — `NewPreconditionUnmet`: "the state the
  operation requires is not in place, **and the operation was not
  attempted**." The required state is a signing setup that produces a
  *verifiable* signature; it is not in place. The second clause is the only
  one worth arguing, and the rollback is what satisfies it: the local tag is
  deleted and nothing was pushed, so the branch leaves the repository
  exactly as it found it. Observably, nothing was attempted — which is the
  guarantee the clause exists to give a caller, not a statement about
  internal steps.
- Same-file precedent, twice over: the up-front signing probe refuses at 30
  (`tag.go:162`), and so does the content-guardrail scan gate. Both are
  detected-and-refused conditions on state the operation needs. The verify
  step tests the *same* precondition category as the probe — the probe
  proves git *can* produce a signature, the verify proves the signature it
  *did* produce is trustworthy. Splitting one precondition across two exit
  families by which checkpoint happens to catch it is an incoherent contract
  for anything scripting on exit codes.

### Why not 41 `conflict` (the test-engineer's second choice)

Rejected on two grounds:

- `result.go:57-58` — "the subject **exists** in a state incompatible with
  the request." After a successful rollback the subject does not exist. The
  claim the status makes would be literally false.
- 41 is already taken in this same verb for
  `conflict.git.tag_already_exists` (`tag.go:137`). Reusing it would give
  one exit code two unrelated meanings within one command — strictly worse
  for a caller than the status quo.

`gate_negative` (20) was also considered and rejected: `create` is an action
verb, not a gate, and the verify is an internal safety step rather than the
question the caller asked.

### Why the rollback-also-failed branch keeps 90

That branch leaves an unverifiable tag sitting in the local repository. It
cannot claim "nothing was attempted", the self-heal itself failed, and no
recovery is available without a person. That is `NewInternal` exactly. The
test-engineer reached the same split; confirmed.

### Consequent renames

The two branches no longer share a status class, so they can no longer share
a code name (clikit validates the class prefix against the status —
`result.go:validatePairing`).

| branch | before | after |
|---|---|---|
| verify failed, rollback succeeded | `internal.git.tag_signature_unverified` / 90 | `precondition_unmet.git.tag_signature_unverified` / 30 |
| verify failed, rollback also failed | `internal.git.tag_signature_unverified` / 90 | `internal.git.tag_rollback_failed` / 90 |

The second rename is not cosmetic: with the classes split, that branch's
governing failure is the failed rollback, not the signature verdict — which
is now the other branch's code.

---

## 2. Findings

### Major

**M1 — `internal/cli/tag.go:207`: the rollback-also-failed branch carried
actively wrong triage.** `finishErr` hardcodes
`clikit.Manual("retry; if this persists, file an issue with the log
output")`. On this branch a retry is guaranteed to fail differently: the
surviving tag makes the next `create` refuse at the existence check
(`conflict.git.tag_already_exists`). The triage directive is the
machine-actionable field an agent caller reads, so this told a caller to do
the one thing that cannot work. Not raised by the test-engineer.

Fixed: this branch now uses `finishDiagnostic` with `clikit.NewInternal` —
same status and exit code, but carrying a correct triage that names the
manual `git tag -d <name>`, states the tag is still present and nothing was
pushed, and orders the trust-configuration fix after it. `root.go:243-248`
explicitly sanctions `finishDiagnostic` for a caller needing "a different
status, triage or diagnostic context".

The manual command consequently moved from the message into the triage
instruction, where it belongs; the test assertion moved with it and now
reads `errors[0].triage.instruction`, matching the convention in
`scan_gate_test.go:49-52` and `merge_test.go:620-621`.

### Minor

**m1 — `internal/cli/tag_call_site_test.go:38`: the lint walked sibling
agent worktrees.** The walk skipped `.git`, `.task-reports` and `.dat` but
not `.claude`. This repo keeps its agent worktrees at
`.claude/worktrees/<slug>/`, each a full checkout of a different branch —
five exist right now. Run from the primary checkout, the lint would judge
this branch by other branches' call sites, and a violation on any of them
would fail this branch's suite. Verified real, not hypothetical: the five
worktree directories are present and each contains its own
`internal/cli/*.go`. Fixed by adding `.claude` to the skip set.

**m2 — `internal/cli/tag_call_site_test.go:10`: the guard's doc comment
overstated its coverage.** The lint is a source-text check for a literal
`"tag"` argument. A call that builds its argument slice first and passes it
variadically is invisible to it — and `internal/signing/signing.go:252`
already uses exactly that shape for other subcommands, so the evasion is
idiomatic in this codebase, not exotic. The guard is still worth having
(it catches the casual regression) but a future reader should not over-trust
it. Fixed by documenting the scope limit on the test itself.

**m3 — `internal/cli/tag_test.go`: ~15 lines of fixture setup duplicated
verbatim** between the two verify-failure tests (ssh-keygen, read pubkey,
write allowed_signers, set the config). Extracted to
`trustOnlyAnUnrelatedKey(t, dir)`, matching the file's existing helper
convention (`localTags`, `gitStubFailingOn`, `signingRepo`). The helper's
doc comment now carries the mechanism explanation — why the split between
`-s` and `-v` is what makes the path reachable — which was previously
restated in both test comments.

**m4 — `internal/cli/tag_test.go:262`: `exec %s "$@"` unquoted** in the PATH
shim. Latent breakage if `exec.LookPath("git")` ever resolves through a path
containing a space. Fixed to `exec "%s" "$@"`.

**m5 — `internal/cli/tag.go:189`: `if verify, err := ...; err != nil { } else
if ...`** did not match the shape the sibling `git tag -s` handling five
lines above uses for the identical two-failure-mode pattern. Flattened to
plain assignment plus two guards.

**m6 — `internal/cli/tag.go:201`: `rollbackDetail := ""`** was a dead
initialization, overwritten on both branches of the if/else that follows.
Changed to `var rollbackDetail string`.

**m7 — `internal/cli/tag_test.go:459`: stale comment.**
`TestTagCreate_ExitCodeTable`'s doc claimed to pin "the full 0/40/41/50/90
contract" while its own table has carried a 30 case (the scan-gate finding)
all along. Pre-existing, not introduced by D4. Corrected to
0/30/40/41/50/90.

**m8 — `internal/cli/tag.go:158`: `signing.NewProber(&git.Repo{Dir: "."})`
fabricates a `git.Repo` by hand** — the only such literal in non-test
production code in the module. `openHere(cmd)` opened and validated that
exact working tree thirty lines earlier and discarded the `*git.Repo` it got
back, so a validated value was available. **Not fixed, deliberately:**
`NewProber` immediately reduces its argument to `repo.Dir`
(`signing.go:233`), so nothing can go wrong today, and widening a helper
shared with `push.go` is churn out of proportion to a cosmetic finding. Left
as plan feedback below.

### Not findings, checked and cleared

- **Placement of the verify step** — after `git tag -s`, before
  `pushRef`. Correct and load-bearing: a failure can only ever touch the
  local tag, never the remote. Confirmed `pushRef` is still the final
  statement.
- **No `-m` flag regression** — `newTagCreateCmd` defines only `--shape`;
  the derived tag name is still its own message.
- **`-s` rather than `-a`** — correct, and the reason is documented at the
  call site.
- **Security** — no new input reaches a shell. `verifyDetail` and
  `rollbackDetail` are git-authored text that flows only into a diagnostic
  message, and `finishDiagnostic` routes every message through
  `sanitizeMessage` (`root.go:250`), which strips control characters and
  bounds the length. The triage instructions I added are fixed literals plus
  the already-validated tag name. The hardening itself is a net security
  gain: an unverifiable release tag can no longer reach a remote.

---

## 3. Fixes applied

All in `internal/cli/`, none outside the diff's own scope:

1. `tag.go` — verify-failed/rollback-succeeded branch moved from
   `internal`/90 to `precondition_unmet`/30 via `finishDiagnostic` +
   `clikit.NewPreconditionUnmet`, code renamed to
   `precondition_unmet.git.tag_signature_unverified`, with a real triage
   naming the trust-configuration fix (the exit-code finding).
2. `tag.go` — rollback-also-failed branch renamed to
   `internal.git.tag_rollback_failed` and moved off `finishErr` so it
   carries correct triage instead of "retry" (M1). Status and exit code
   unchanged at `internal`/90. Both branches now also carry
   `data.tag`, matching the sibling precondition refusal.
3. `tag.go` — `--help` exit-code table updated: the verify-failure clause
   moved from the 90 row to the 30 row, and the 90 row now describes the
   failed-rollback case it actually covers.
4. `tag.go` — control-flow shape aligned with the sibling handler; dead
   `rollbackDetail` initialization removed (m5, m6). Two comments added
   stating why each branch lands in the class it does.
5. `tag_call_site_test.go` — `.claude` added to the skip set (m1); scope
   limit documented (m2).
6. `tag_test.go` — assertions updated to the new statuses and codes, plus a
   code assertion added to each of the two branches (the verify branch had
   none; the rollback branch's manual-command assertion moved to the triage
   field); `trustOnlyAnUnrelatedKey` helper extracted (m3); shim quoting
   fixed (m4); stale exit-code comment corrected (m7).

No test was weakened or deleted. Every assertion added or moved is strictly
stronger than what it replaced: two code-name assertions are new, and the
manual-command check now verifies the machine-readable triage field rather
than prose in a message.

---

## 4. Re-verification — fresh, on the final tree

```
=== go build ./... ===
build exit: 0
=== go vet ./... ===
vet exit: 0
=== go test ./... -count=1 -timeout 30m ===
?   github.com/johnrichter/git-tools/cmd/git-tools               [no test files]
ok  github.com/johnrichter/git-tools/internal/cli                 1114.319s
ok  github.com/johnrichter/git-tools/internal/gitexec              106.273s
ok  github.com/johnrichter/git-tools/internal/hooks                  0.086s
ok  github.com/johnrichter/git-tools/internal/result                 0.005s
ok  github.com/johnrichter/git-tools/internal/signing                17.496s
ok  github.com/johnrichter/git-tools/internal/worktreeclean          41.521s
ok  github.com/johnrichter/git-tools/worktree-gate/detect             6.432s
?   github.com/johnrichter/git-tools/worktree-gate/detect/cmd/worktree-gate [no test files]
ok  github.com/johnrichter/git-tools/worktree-gate/fixtures            0.004s
ok  github.com/johnrichter/git-tools/worktree-gate/lifecycle         147.596s
FULL TEST EXIT: 0
```

`gofmt -l .` clean. `internal/cli` at 1114.3s matches the test-engineer's
1114.4s — expected runtime, not a hang.

Targeted flake check on the changed paths, `-count=2`, all passing both
times:

```
$ go test ./internal/cli/ -run 'TestTagCreate_FailedPostCreationVerification|TestTagCreate_RollbackDeleteAlsoFails|TestTagCreate_NoSigningKey|TestRawGitTagCallSite' -v -count=2
--- PASS: TestRawGitTagCallSite_ConfinedToTagGo (0.00s)
--- PASS: TestTagCreate_NoSigningKey_RefusesBeforeAttemptingToSign (3.42s)
--- PASS: TestTagCreate_FailedPostCreationVerification_RollsBackAndRefuses (3.48s)
--- PASS: TestTagCreate_RollbackDeleteAlsoFails_NamesManualCommand (3.50s)
--- PASS: TestRawGitTagCallSite_ConfinedToTagGo (0.00s)
--- PASS: TestTagCreate_NoSigningKey_RefusesBeforeAttemptingToSign (3.40s)
--- PASS: TestTagCreate_FailedPostCreationVerification_RollsBackAndRefuses (3.47s)
--- PASS: TestTagCreate_RollbackDeleteAlsoFails_NamesManualCommand (3.46s)
ok  github.com/johnrichter/git-tools/internal/cli  20.757s
```

The new code assertions are non-vacuous by construction: each reads
`errors[0].code` and compares to a literal, and each passes only because the
production code emits the new name. The same holds for the triage assertion
— an absent `triage.instruction` would yield `""` and fail the check.

### Environment note (not a code finding)

Two intermediate full-suite runs failed spuriously and are worth recording
so nobody mistakes them for regressions:

- **Run A — every failure `no space left on device`.** `/tmp` is a separate
  31G mount and was at 8K free. The cause is unrelated to this repo: ~12G of
  week-old `ruby-build.*`, `python-build.*` and `mise_data_test*` scratch
  from other efforts. I did not touch those. I reclaimed only this repo's
  own orphaned Go test scratch — 136 `t.TempDir()` and `go-build*` directories
  left behind by killed runs, ~670M — which was sufficient.
- **Run B — three `not_found`/40 tests returned `success`/0.** Self-inflicted:
  I had pointed `TMPDIR` at a directory inside the worktree to dodge the full
  `/tmp`. Several tests require their temp dir to sit **outside any git
  working tree** (`TestRequireRepo_NotAGitTree_IsNotFound`,
  `TestTagCreate_ExitCodeTable/not_found`,
  `TestOtherVerbs_DataMaps_DoNotCarryMergeDataKeys/worktree_list_not_found`),
  and inside the repo `git.Open` legitimately succeeded. Reverted; the
  scratch directory is gone and the tree is clean. Worth knowing: this suite
  cannot be relocated via `TMPDIR` into the repo.

The reported passing run above is with default `TMPDIR=/tmp` and adequate
free space.

---

## 5. Test-suite assessment

**Adequate.** The suite is genuinely adversarial rather than green-but-hollow:

- Both verify-failure branches are forced by real mechanism, not mocked
  status codes. The mismatched-allowed-signers trick is the right way to
  split "can sign" from "verifies" — it exploits the actual asymmetry that
  `git tag -s` never reads `gpg.ssh.allowedSignersFile` and `git tag -v`
  does.
- Every refusal test asserts the *world state*, not just the record: no
  local tag survives on the rollback path, the tag *does* survive on the
  rollback-failed path, and the bare remote is empty in both. That is what
  makes them non-vacuous.
- The lint was proven non-vacuous by adversarial injection, and it retains
  its own `len(sites) == 0` self-check.
- The test-engineer's PATH-shim choice over a chmod race is correct, and
  their reasoning for rejecting the task's own chmod hint is sound: the same
  directory permission gates both the create and the delete inside one
  subprocess.

Remaining gap, unchanged and acceptable: `git tag -v` failing to *execute at
all* (`internal.git.tag_verify_failed`) is untested. It is structurally
identical to the spawn-failure halves already accepted elsewhere in this file
(`internal.git.tag_create_failed`'s own `err != nil` branch), and the PATH
shim added here would make it cheap to close later if that class is ever
worth covering systematically. Not worth a task on its own.

No gaps I would send back to the test-engineer.

---

## 6. Residual risk

**A repository that signs but cannot verify locally now fails `tag create`
where it previously succeeded.** This is inherent to D4's spec, not a defect,
but it is a real behavior change and should be in the release notes.

The concrete case: `gpg.format = ssh` with no `gpg.ssh.allowedSignersFile`
configured. `probeSigning` (`internal/signing/signing.go:325-341`) proves
signing works with `git commit-tree -S`, which never consults an
allowed-signers file, so the up-front check passes. `git tag -v` does consult
it, fails, and `create` now refuses at exit 30 having rolled the tag back.
Such a user could previously cut and push a tag they had no local means to
verify.

I judge the new posture correct — a release tag you cannot verify locally is
one you should not push — and it is recoverable by configuration, which the
triage instruction I added names explicitly ("the allowed-signers file or
keyring the verifier reads"). Flagging it because the failure will look
surprising to anyone who signs but has never set up a trust store.

---

## 7. Plan feedback

1. **Document the exit-30 widening downstream.** `tag create`'s 30 now covers
   three distinct causes (scan finding, no signing key, unverifiable tag).
   Any caller that branches on 30 for `tag create` should read
   `errors[0].code` rather than the exit code alone. Worth a line wherever
   the release-tag flow is documented for consumers.
2. **`openHere` should return the repo it opens** (m8). A small, separate
   cleanup: it validates a working tree and throws the `*git.Repo` away,
   which is why `tag.go:158` hand-builds one. Deliberately out of scope here
   — it touches `push.go` — but worth folding into whatever task next
   touches that helper.
3. **The single-call-site guard generalizes.** The confined-to-one-file
   pattern in `tag_call_site_test.go` would suit other post-migration
   invariants in this module. If it gets reused, the `.claude` skip and the
   variadic-slice blind spot fixed and documented here should move with it —
   ideally into one shared walker rather than being re-derived per lint.
4. **Task framing correction, minor.** D4's brief specified the lint as "the
   literal substring `git tag` appears in exactly one place". The implementer
   was right to reinterpret it as *raw `git tag` subcommand invocations
   confined to `tag.go`*, and right about why: the literal two words appear
   legitimately in help text and in `worktree-gate/detect`'s raw-command
   classifier, while the real call sites never spell them adjacently. Fix 2
   also adds two more legitimate invocations in the same file, so
   "exactly one" was unsatisfiable as written. The divergence is correct and
   documented; noting it so the spec is not re-derived the same way next
   time.
