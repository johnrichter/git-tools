# D4: tag create hardening — independent test verification

## Verdict: PASS

All five implementer claims confirmed by direct diff inspection and fresh
run evidence. All seven assigned checks completed. One design finding for
the quality-reviewer: exit code 90 (`internal`) for the post-creation
verify-failure/rollback path is the wrong family — recommend `precondition_unmet`
(30), matching the up-front signing check in the same file. One new test
added and passing, closing the previously-untested rollback-delete-also-fails
path. One new file changed: `internal/cli/tag_test.go` (+100 lines, one new
test + one new helper). No production code changed.

---

## 1. Diff review — claims 1-4 confirmed real

`git diff main...HEAD --stat`:
```
.task-reports/d4-tag-create-hardening-report.md | 112 ++
internal/cli/tag.go                             |  58 ++-
internal/cli/tag_call_site_test.go              |  98 ++ (new file)
internal/cli/tag_test.go                        |  68 ++
```

**Claim 1 (signing precondition check)** — confirmed in `internal/cli/tag.go`:
```go
available, detail, err := signing.NewProber(&git.Repo{Dir: "."}).Available(ctx)
if err != nil {
    return finishErr(cmd, nil, "internal.git.signing_probe_failed", ...)
}
if !available {
    return finishDiagnostic(cmd, ..., clikit.NewPreconditionUnmet,
        "precondition_unmet.git.signing_key_unresolved", ...)
}
```
`grep` confirms `merge.go` uses the identical prober call shape
(`signing.NewProber(...).Available(ctx)`) and the identical code name
`precondition_unmet.git.signing_key_unresolved` for its own analogous check
— no new signing-detection logic was written, exactly as claimed. Runs
before `git tag -s` (line order confirmed in the diff).

**Claim 2 (post-creation `git tag -v` verify + rollback)** — confirmed:
placed directly after the `git tag -s` call and before `return
pushRef(...)`. On nonzero verify exit it runs `git tag -d`, then refuses
with `internal.git.tag_signature_unverified` at exit 90. Rollback-delete-
also-fails sub-branch present and produces a distinct message naming the
manual `git tag -d <name>` command.

`TestTagCreate_FailedPostCreationVerification_RollsBackAndRefuses` present
in `tag_test.go`, forces the failure via a mismatched
`gpg.ssh.allowedSignersFile` (signing succeeds, verify fails
deterministically since `-s` never reads that file but `-v` does).

**Claim 3 (single-call-site lint, confined-to-file interpretation)** —
confirmed `internal/cli/tag_call_site_test.go` exists, walks all non-test
`.go` files, extracts `gitexec.RunGit(...)` calls via the pre-existing
`extractCalls` helper (reused from `worktree_test.go`, confirmed by grep —
not duplicated), adds a new `splitTopLevelArgs` helper, and asserts every
`"tag"`-subcommand call site is in `tag.go`. `grep -rln "gitexec.RunGit"`
across the module shows only `tag.go`, `merge.go`, `branch.go`, `push.go`,
`signing.go`, `worktreeclean.go` call `RunGit` at all, and none besides
`tag.go` pass `"tag"` as the subcommand (confirmed both by static read and
by the adversarial re-run in section 7 below).

**Claim 4 (no `-m` flag)** — confirmed: `cmd.Flags()` in `newTagCreateCmd`
defines only `--shape`; the only `-m` in the file is the fixed literal
`git tag -s <name> -m <name>` git-arg, not a CLI flag.

## 2. Fresh `go build` / `go vet`

```
$ go build ./...    # exit 0, no output
$ go vet ./...       # exit 0, no output
```
Both re-run standalone (not reused from the implementer's report), clean.

## 3. Fresh full suite, `-count=1 -timeout 30m`

```
$ go test ./... -count=1 -timeout 30m
?   github.com/johnrichter/git-tools/cmd/git-tools               [no test files]
ok  github.com/johnrichter/git-tools/internal/cli                 1114.422s
ok  github.com/johnrichter/git-tools/internal/gitexec              106.803s
ok  github.com/johnrichter/git-tools/internal/hooks                  0.101s
ok  github.com/johnrichter/git-tools/internal/result                 0.005s
ok  github.com/johnrichter/git-tools/internal/signing                17.477s
ok  github.com/johnrichter/git-tools/internal/worktreeclean          41.500s
ok  github.com/johnrichter/git-tools/worktree-gate/detect             6.477s
?   github.com/johnrichter/git-tools/worktree-gate/detect/cmd/worktree-gate [no test files]
ok  github.com/johnrichter/git-tools/worktree-gate/fixtures            0.004s
ok  github.com/johnrichter/git-tools/worktree-gate/lifecycle         147.882s
FULL TEST EXIT: 0
```
`internal/cli` took ~18.6 minutes — consistent with the implementer's own
figure, not a hang. Total wall time for the run was ~22 minutes. Exit 0,
every package `ok`. This run includes the new rollback-delete test added in
section 6 below.

## 4. Targeted re-run: `TestTagCreate_FailedPostCreationVerification_RollsBackAndRefuses`

```
$ go test ./internal/cli/... -run TestTagCreate_FailedPostCreationVerification_RollsBackAndRefuses -v -count=1
=== RUN   TestTagCreate_FailedPostCreationVerification_RollsBackAndRefuses
--- PASS: TestTagCreate_FailedPostCreationVerification_RollsBackAndRefuses (3.49s)
```
Also re-run 3x consecutively via `-count=3` alongside every other
`TestTagCreate*` test (see section 7 log) — passed all 3 times, no
flakiness observed.

Confirmed the test actually forces the failure path and actually asserts
what it claims: reading the test body, it (a) asserts `status ==
"internal"`/`exit == 90`; (b) asserts the message contains both
`"failed its own post-creation signature verification"` and `"rolled
back"`; (c) asserts `localTags(t, dir) == ""` — the local tag is gone,
proving the rollback delete actually ran and succeeded; (d) asserts the
bare remote's `git tag -l` is empty — nothing reached the push step. This
is a real, non-vacuous assertion of rollback, not a status-code-only check.

## 5. Exit-code judgment — recommend changing 90 to 30

**Finding: exit 90 (`internal`) is the wrong family for the verify-failure/
rollback-succeeded case. Recommend `precondition_unmet` (30) instead.**

Evidence from this module's own conventions, not opinion:

- `root.go`'s own doc comment on `finishErr` (the helper `tag.go` calls for
  this path) states explicitly: "an infrastructure failure from this CLI
  itself, **not a diagnostic the underlying tool reported**." The verify
  failure is exactly the excluded case — `git tag -v` reporting nonzero is
  a deterministic, well-understood diagnostic (a real signature-trust
  mismatch), not an infrastructure fault. `clikit`'s own `NewInternal` doc
  comment concurs: class-90 is for "the tool itself failed, **or produced
  an outcome it cannot classify**." This outcome is precisely classified —
  named, with a distinct code and a correct rollback — so it does not fit
  either half of that definition.
- `clikit.NewPreconditionUnmet`'s doc comment: "class-30: the state the
  operation requires is not in place." The verify step is testing exactly
  that same state — "can this tag actually be trusted/signed correctly" —
  just one step later than the up-front probe can reach (the probe proves
  git *can* produce a signature; the verify step proves the signature it
  *did* produce is trustworthy). Both checks are testing the same
  precondition category (a valid, trustworthy signing setup for this tag);
  splitting them across two different exit families depending on which
  checkpoint happens to catch the problem is an inconsistent contract for
  callers scripting against exit codes.
- Direct precedent already in this same file: the up-front signing-probe
  failure uses `precondition_unmet.git.signing_key_unresolved` at 30. The
  post-creation verify failure is the same signing precondition, detected
  one step later — treating it as `internal` breaks that consistency
  without a clear reason. The engineer's own report footnote defending 90
  ("the sibling `git tag -s` nonzero-exit handling ... already does this
  for tag creation itself") is not a clean analogy: `git tag -s` failing to
  *execute* the signing operation at all (e.g. transient environment fault)
  is a materially different failure mode than `git tag -v` cleanly *saying
  no* to a signature it just checked.
- Counter-consideration acknowledged: `merge.go`'s own exit table treats a
  detected-and-aborted merge conflict as `conflict` (41), not
  `precondition_unmet` — so this codebase does have a pattern of a
  mid-operation detected-and-unwound condition landing in a *different*
  class than the up-front precondition check for the same command. If the
  reviewer prefers that framing, `conflict` (41) — "the subject [the tag
  just made] exists in a state incompatible with the request [a verified,
  trustworthy signature]" — is a defensible second choice. Either 30 or 41
  is a better fit than 90; 90 should be reserved for the genuinely
  unexpected case (e.g. `git tag -v` failing to execute at all, which
  `internal.git.tag_verify_failed` already correctly covers a few lines
  above the branch in question).

**Recommendation: change the verify-failed-but-rollback-succeeded branch
from `internal.git.tag_signature_unverified`/90 to
`precondition_unmet.git.tag_signature_unverified`/30** (or, if the reviewer
weighs the merge.go conflict precedent more heavily, `conflict`/41). The
rollback-delete-also-failed sub-branch is a closer fit for `internal`/90 as-is
— that one genuinely is an unexpected, unclassified infrastructure failure
(the self-heal itself failed) with no clean recovery, matching `NewInternal`'s
definition. This is a design finding, not a fix — no code was changed here.

## 6. New test: rollback-delete-also-fails path

Added `TestTagCreate_RollbackDeleteAlsoFails_NamesManualCommand` and a
`gitStubFailingOn` helper to `internal/cli/tag_test.go` (the only file
changed by this verification pass — no production code touched).

**Mechanism used:** the same mismatched-`allowedSignersFile` setup as the
existing rollback test forces `git tag -v` to fail deterministically. To
also force the *following* `git tag -d` to fail, a `git` shim is placed
ahead of the real binary on the CLI subprocess's `PATH`: it forwards every
invocation to the real `git` except one whose arguments exactly match `tag
-d`, which it refuses outright (`exit 1`) without touching git at all.
`gitexec.RunGit`/`sysops.Run` resolve `"git"` off `PATH` at call time, and
the CLI subprocess inherits whatever `PATH` the test sets via `cmd.Env`, so
this is deterministic — no timing race, no chmod/permission trick.

**Why not a race (chmod/permission) approach:** the task's own hint
suggested "making the local tag ref temporarily unwritable... in a
controlled way." I evaluated `chmod`-ing `.git/refs/tags` to strip write
permission, but rejected it: the *same* directory write permission gates
both the initial `git tag -s` ref-creation and the later `git tag -d`
removal, and both happen inside one CLI subprocess invocation with no
externally-visible hook between them — chmod-ing the directory before
launch blocks tag creation itself (never reaches the delete step at all),
and chmod-ing it mid-invocation from the test process would require racing
a filesystem-event poll against the subprocess's internal timing, which
would be a flake, not a proof. The PATH-shim approach is the deterministic
version of the same "make the delete fail" idea, and reuses the exact
mechanism `sysops.Run` already depends on (whatever resolves as `"git"` on
`PATH` is what runs) rather than fighting it.

**Assertions, matching what the report claimed:**
```
$ go test ./internal/cli/... -run TestTagCreate_RollbackDeleteAlsoFails_NamesManualCommand -v -count=1
=== RUN   TestTagCreate_RollbackDeleteAlsoFails_NamesManualCommand
--- PASS: TestTagCreate_RollbackDeleteAlsoFails_NamesManualCommand (3.47s)
```
- `status == "internal"` / `exit == 90` (delete-also-failed path, unlike
  the verify-only path discussed in section 5, does fit `internal` as-is).
- message contains `"rollback delete also failed"`.
- message contains the literal manual fallback `` `git tag -d v1.4.0` ``.
- `localTags(t, dir) == "v1.4.0"` — the tag is still present locally,
  proving the delete genuinely never ran (not a false-positive "rolled
  back" claim).
- the bare remote's tag list is empty — confirms the push step never ran.

Re-run 3x via `-count=3` alongside the whole `TestTagCreate*` family (see
section 7 log) — passed all 3 times.

## 7. Lint adversarial proof: `TestRawGitTagCallSite_ConfinedToTagGo`

Confirmed the lint is real, not vacuous, by injecting a throwaway violation
into `internal/cli/push.go` (dead code behind `if false`, so it could not
otherwise affect behavior) and re-running the lint alone:

```go
// injected, temporarily, then removed:
if false {
    _, _ = gitexec.RunGit(ctx, ".", "tag", "-l")
}
```
```
$ go test ./internal/cli/... -run TestRawGitTagCallSite_ConfinedToTagGo -v -count=1
--- FAIL: TestRawGitTagCallSite_ConfinedToTagGo (0.00s)
    tag_call_site_test.go:60: raw `git tag` call site outside tag.go: .../internal/cli/push.go
```
Violation removed immediately after; `git diff --stat internal/cli/push.go`
confirms zero net change to that file. Re-ran the lint clean afterward:
```
$ go test ./internal/cli/... -run TestRawGitTagCallSite_ConfinedToTagGo -v -count=1
--- PASS: TestRawGitTagCallSite_ConfinedToTagGo (0.00s)
```

Full `-count=3` run of every `TestTagCreate*` test plus the lint, for
flake-checking sections 4 and 6 together:
```
$ go test ./internal/cli/... -run 'TestTagCreate' -v -count=3
[... all subtests, 3 full passes, including
 TestTagCreate_NoSigningKey_RefusesBeforeAttemptingToSign,
 TestTagCreate_FailedPostCreationVerification_RollsBackAndRefuses,
 TestTagCreate_RollbackDeleteAlsoFails_NamesManualCommand ...]
ok  github.com/johnrichter/git-tools/internal/cli  275.332s
```
(Lint re-run separately above since it is not name-matched by `TestTagCreate`.)

## Coverage / gaps

- Signing precondition refusal: covered (existing + re-verified).
- Post-creation verify failure + successful rollback: covered (existing +
  re-verified, non-vacuous assertions confirmed).
- Post-creation verify failure + rollback-delete-also-fails: **now
  covered** — previously flagged as untested, closed by this pass.
- Call-site lint: covered, proven non-vacuous by adversarial injection.
- Not separately tested: a `git tag -v` invocation failing to *execute at
  all* (`internal.git.tag_verify_failed`, distinct from a clean nonzero
  exit) — this is a spawn-failure branch structurally identical to other
  untested spawn-failure branches already accepted elsewhere in this file
  (e.g. `internal.git.tag_create_failed`'s own `err != nil` half); not
  flagged as a gap specific to this task.

## Files touched by this verification pass

- `internal/cli/tag_test.go` — added
  `TestTagCreate_RollbackDeleteAlsoFails_NamesManualCommand` and
  `gitStubFailingOn` helper; added `encoding/json` and `fmt` imports.
  No other file changed; `push.go`'s throwaway lint-proof violation was
  added and removed within this session, leaving no diff.
