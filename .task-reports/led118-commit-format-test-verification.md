# LED-118 commit-message-format check — independent test-engineer verification

## Verdict: PASS

The implementation delegates genuinely to the repository's own configured
`commit-msg` hook, is a true no-op when none resolves, and all three
required outcomes (accept / reject / no hook) verify against a live,
independently-constructed CLI subprocess, not just the engineer's own
tests. The scope decision to touch only `merge` is confirmed correct by
direct inspection of `resign`/`sign`/`branch create`'s own code. One real,
pre-existing gap (scope note 2, exit-90 classification of a hook rejecting
git's own default merge message) is confirmed structurally reproducible and
is a legitimate defer-to-future-ledger call, not a blocking defect — see
Check 5. One test-suite quality finding, orthogonal to LED-118's own
correctness, is flagged in Check 4c: this sandbox's real global
`commit-msg` hook means the existing "no hook configured" tests do not
actually exercise a true no-op path here.

Files reviewed: `internal/commitmsg/commitmsg.go`,
`internal/commitmsg/commitmsg_test.go`, `internal/cli/merge.go`,
`internal/cli/merge_commit_message_hook_test.go`, plus (read-only, from
`GOMODCACHE`) `claude-shared-tooling/go/git@v0.4.0`'s `resign.go`,
`branch.go`, `merge.go`.

New file added by this verification, staying in the worktree:
`internal/cli/led118_verification_test.go` — five tests, independent
fixtures/assertions from the engineer's own, described in Check 4/5 below.

---

## Check 1 — delegation, not reimplementation; genuine no-op

Read `internal/commitmsg/commitmsg.go` directly (not just the report).

- `hooksDirectory` resolves exactly the way git itself does:
  `git config --get core.hooksPath` (exit 1 → unset, falls through) then
  `git rev-parse --path-format=absolute --git-path hooks` for the default.
  Relative `core.hooksPath` values are joined against `dir` (git-config(1)
  semantics); absolute values used directly. No format policy anywhere in
  this file — it only locates and invokes whatever the repo already names.
- `resolveHook` requires the candidate file to exist and be `+x`
  (`info.Mode()&0o111 == 0` → not configured). A missing or non-executable
  file returns `(ok=false, err=nil)` — a plain "nothing to delegate to"
  answer, never an error and never a fallback rule.
- `Check` returns `(nil, nil)` on that `!ok` branch — line 61-63 — before
  any temp file is created or any process spawned. This is a genuine
  no-op: zero subprocess invocations, zero I/O beyond the two `git`
  resolution calls, when no hook exists.
- When a hook exists, it is invoked exactly as git's own commit-msg
  contract: message written to a scratch file, that file's path passed as
  the hook's sole argument (`sysops.Run(ctx, hook, []string{tmp.Name()}, ...)`),
  exit code alone decides accept/reject. The hook's own stderr/stdout (not
  a canned message) becomes the refusal detail.

**Confirmed by direct read: genuine delegation, genuine no-op.**

## Check 2 — fresh `go build` / `go vet`

```
$ go build ./...
(no output, exit 0)
$ go vet ./...
(no output, exit 0)
```

**PASS.**

## Check 3 — fresh full suite, `-count=1 -timeout 30m`

Run twice: once before adding this verification's own test file (baseline,
matching the engineer's claim), once after (with
`internal/cli/led118_verification_test.go` included), both `-count=1`.

Baseline (no QA file yet):
```
ok  	github.com/johnrichter/git-tools/internal/cli            1196.211s
ok  	github.com/johnrichter/git-tools/internal/commitmsg         7.243s
ok  	github.com/johnrichter/git-tools/internal/gitexec          106.277s
ok  	github.com/johnrichter/git-tools/internal/hooks              0.100s
ok  	github.com/johnrichter/git-tools/internal/result             0.005s
ok  	github.com/johnrichter/git-tools/internal/signing            17.470s
ok  	github.com/johnrichter/git-tools/internal/worktreeclean      41.413s
ok  	github.com/johnrichter/git-tools/worktree-gate/detect         6.453s
ok  	github.com/johnrichter/git-tools/worktree-gate/fixtures       0.004s
ok  	github.com/johnrichter/git-tools/worktree-gate/lifecycle    147.337s
```
464 `--- PASS`, 0 `--- FAIL`, exit 0. Full log: `/tmp/led118_full_test_output.log`.

With this verification's own test file included:
```
ok  	github.com/johnrichter/git-tools/internal/cli            1213.630s
ok  	github.com/johnrichter/git-tools/internal/commitmsg         7.216s
ok  	github.com/johnrichter/git-tools/internal/gitexec          106.167s
ok  	github.com/johnrichter/git-tools/internal/hooks              0.089s
ok  	github.com/johnrichter/git-tools/internal/result             0.022s
ok  	github.com/johnrichter/git-tools/internal/signing            17.405s
ok  	github.com/johnrichter/git-tools/internal/worktreeclean      41.386s
ok  	github.com/johnrichter/git-tools/worktree-gate/detect         6.408s
ok  	github.com/johnrichter/git-tools/worktree-gate/fixtures       0.022s
ok  	github.com/johnrichter/git-tools/worktree-gate/lifecycle    147.323s
```
exit 0. Full log: `/tmp/led118_full_test_output_2.log`.

**Reproduces the engineer's claim exactly. PASS.**

## Check 4 — independent live scenarios (own fixtures, not the engineer's)

Reused `buildCLI`/`runCLI`/`signingRepo`/`signedBranch`/`runGit` test
helpers (already in the package) but wrote a new hook script
(`qaHook`, content-inspecting: rejects unless the message contains the
literal string `APPROVED`, a different verdict rule than the engineer's
fixed-exit-code stub) and new assertions, in
`internal/cli/led118_verification_test.go`. This proves argument-passing
and message content are real, not just exit-code plumbing.

**(a) hook configured to ACCEPT** —
`TestLED118_QA_HookAccepts_MergeLands`: merge with
`--message "APPROVED: land qa-feature"` against a hook that only accepts
messages containing `APPROVED`. Result: exit 0, `success`, hook's marker
file shows it actually ran and saw the real message text, `HEAD` subject
equals the supplied message verbatim. **PASS.**

**(b) hook configured to REJECT** —
`TestLED118_QA_HookRejects_MergeBlockedNothingMoves`: same hook, message
omits `APPROVED`. Result: exit 30, `precondition_unmet`, exactly one error
diagnostic, code `precondition_unmet.git.commit_message_hook_rejected`,
message surfaces the hook's own stderr (`"lacks APPROVED marker"`), hook's
marker proves it ran, and neither `main` nor `qa-feature` moved — the
signing gate never touched the source branch for a doomed merge. **PASS.**

**(c) NO hook configured at all** — this needed extra care: this sandbox's
own git installation carries a real global `core.hooksPath` in
`$HOME/.gitconfig` (`/usr/local/lib/dd-git-hooks`, a Datadog-installed
secrets-scanning hook, unrelated to git-tools). A fresh `git init` in this
environment is never actually hookless unless a test isolates itself from
that global config. See Check 4c below for the finding this produced.
`TestLED118_QA_TrueNoHook_MergeUnaffected` isolates the repo and the CLI
subprocess via `GIT_CONFIG_GLOBAL=/dev/null` / `GIT_CONFIG_SYSTEM=/dev/null`,
confirms `core.hooksPath` genuinely resolves to nothing under that
isolation, then merges with `--message "no hook here at all, verified"`.
Result: exit 0, `success`, `HEAD` subject equals the supplied message.
**PASS — genuine no-op confirmed, not just "the real hook happened to
accept it."**

All three ran live: `go test ./internal/cli/... -run TestLED118_QA -v -count=1 -timeout 5m`
→ `ok  github.com/johnrichter/git-tools/internal/cli  18.673s`, 5/5 pass.

### Check 4c — finding: the existing "no hook configured" tests are not exercising a true no-op in this environment

`TestLED118_QA_HostGlobalHookIsInherited_NotActuallyNoHook` confirms
directly: a bare `git init` in this sandbox resolves
`core.hooksPath` → `/usr/local/lib/dd-git-hooks`, which carries a real,
executable `commit-msg` script (a Datadog secrets scanner, chained to any
repo-local hook via `run-local-hooks`). Neither
`internal/commitmsg/commitmsg_test.go`'s `TestCheck_NoHookConfigured_IsNoOp`
nor `internal/cli/merge_commit_message_hook_test.go`'s
`TestMerge_CommitMessageHook_NoneConfigured_ProceedsNormally` isolates
`GIT_CONFIG_GLOBAL`/`HOME` from this host default. In this sandbox, both
tests actually exercise the delegation path — `commitmsg.Check` resolves
and really invokes the host's global hook — and pass only because that
real hook's secrets scan happens to accept the benign test message. Their
names and doc comments claim "no-op" but the environment they run in here
does not produce that condition.

This is not a functional defect in `commitmsg.Check` itself — the
delegation and no-op logic is correct per Check 1's direct code read, and
this verification's own isolated test (`TestLED118_QA_TrueNoHook_MergeUnaffected`)
confirms the true no-op path independently. It is a test-fixture gap: the
"no-op" acceptance criterion is asserted by tests that, in an environment
carrying a real global hook (this sandbox, and plausibly any CI runner
provisioned the same way), do not actually reach the no-op branch. Whether
this is worth a follow-up fix (isolate `GIT_CONFIG_GLOBAL`/`HOME` in those
two existing tests) is the quality-reviewer's call — flagging precisely
rather than fixing it myself.

## Check 5 — scope note 2: default-merge-message hook rejection classifies as exit 90/internal

Reproduced directly: `TestLED118_QA_DefaultMergeMessage_RejectingHook_ClassifiesAsInternal`
merges with no `--message` (git composes its own default) against a hook
that rejects unless `APPROVED` appears — git's own default merge message
never will. Observed:

```
exit=90 status=internal
errors=[{code: internal.git.merge_failed,
         message: "merge qa-feature: git: merge qa-feature: git: merge --no-ff -S qa-feature: exit 1: qa-hook: message lacks APPROVED marker Not committing merge; use 'git commit' to complete the merge."}]
```

`main` and `qa-feature` both stayed at their pre-merge tips — nothing
landed, so the misclassification is a reporting/exit-code problem, not a
correctness/safety one.

**Root-cause confirmed structurally, not just asserted from the report.**
Read `claude-shared-tooling/go/git@v0.4.0/merge.go`'s `Merge()`: on a
non-dry-run failure it calls `r.conflictedFiles(ctx)` and only builds a
`*ConflictError` when that returns entries. A commit-msg-hook rejection
during merge leaves zero conflicted files (the content merge succeeded;
only the commit step failed), so it falls through to the generic
`fmt.Errorf("git: merge %s: %w", ...)` wrap — which `handleGitError`
(via `gitresult.ConflictDiagnostic`, which only recognizes
`*git.StaleRefError` and `*git.ConflictError`) cannot distinguish from any
other non-conflict merge failure, and folds into
`internal.git.merge_failed` (exit 90) by design.

This confirms the report's own justification is accurate, not just
asserted: this codebase's established precedent for classifying git
failures is **structural** (checking git's post-failure repository state:
conflicted files, stale ref), never stderr text-matching. The only string
that would let a caller recognize this specific case is git's own fixed,
version-coupled message ("Not committing merge; use 'git commit' to
complete the merge."), which is not the hook's own output and would be a
new kind of classification (parsing git's own internal wording) this
codebase does not currently do anywhere.

**Judgment: this is a real, but genuinely non-blocking, pre-existing gap.**
Reasoning:
- It predates LED-118 — any pre-LED-118 merge with a rejecting native hook
  and no `--message` already hit this same generic-error fallthrough. This
  task did not introduce it and does not make it worse; it only narrowed
  where the *new* explicit check applies (the `--message` path), leaving
  git's own native invocation (the only path this gap can occur on)
  untouched by design.
- Fixing it properly needs one of: (a) extending the shared, separately
  versioned `go/git` module to detect and surface hook rejection
  structurally (a version bump, out of this task's and this repo's own
  control), or (b) string-matching git's own internal wording, which is
  exactly the kind of hook-logic/git-internals reimplementation the
  ledger's own fix for LED-118 explicitly warns against, and which this
  codebase's own precedent avoids everywhere else.
- It is precisely scoped and named in the report already, with a
  recommendation for a follow-up ledger entry — not silently absorbed.

Flag for quality-reviewer: accept the report's framing on this point; it
holds up under independent structural verification. If a future ledger
entry is opened for it, exit 30/`precondition_unmet` (matching D4's own
recent exit-code correction, per this branch's git log) is the right
target classification once a structural (not textual) detection mechanism
exists.

## Check 6 — `resign`/`sign`/`branch create` genuinely never compose a new commit message

Grepped `internal/cli` for any other commit-message composition site:
`grep -rln "\-m \|commit-tree\|\"commit\"," internal/cli/*.go` (excluding
tests) → **no matches** beyond `merge.go`. `merge` is the sole call site.

- `internal/cli/resign.go` (`sign` and `resign` are the same command,
  `newSignCmd`/`newResignCmd`, both call `runResign` → `repo.Resign`):
  read `claude-shared-tooling/go/git@v0.4.0/resign.go` directly.
  `readCommit` slices the message "verbatim... byte for byte" out of
  `git cat-file -p`'s raw output (comment at resign.go:170-178, confirmed
  by the implementation at lines 190-193), and `commitTree` passes that
  exact `c.message` as `commit-tree`'s stdin (line 368) — no `-m` flag, no
  new text ever constructed. Resign changes only the signature (`SignArgs`
  passed to `commit-tree`), never the message.
- `internal/cli/branch.go`: only calls `repo.CreateBranch` (git's plumbing
  is a bare `git branch [-f] <name> <start-point>`, confirmed by reading
  `claude-shared-tooling/go/git@v0.4.0/branch.go` lines 25-32) and
  `DeleteBranch`/list — ref moves/creation/deletion/listing only, no
  `commit`/`commit-tree` invocation anywhere in that file.

**Confirmed correct by direct code inspection of both this repo's call
sites and the shared library's implementation, not by trusting the
report's claim.**

## Coverage

- `internal/commitmsg`: unit-level, isolated (no CLI binary): no-op
  (default, non-executable, no-script-at-configured-path), accept, reject,
  relative `core.hooksPath`, absolute `core.hooksPath` — 7 cases,
  `commitmsg_test.go`.
- `internal/cli`: 4 end-to-end cases (`merge_commit_message_hook_test.go`)
  + 5 independent end-to-end cases added by this verification
  (`led118_verification_test.go`): accept (content-checking hook), reject
  (content-checking hook, exit/code/message/no-move assertions), the
  host-global-hook finding, the true-isolated-no-hook case, and the
  default-message/rejecting-hook classification pin.
- Gap not covered by either suite: `commitmsg.Check`'s own error return
  path (`err != nil`, e.g. a `git config` invocation itself failing) has no
  direct test — low-risk plumbing, not one of the three named acceptance
  outcomes, not blocking.

## Failures

None. All PASS/PASS/PASS across build, vet, full suite (x2), and all nine
live end-to-end scenarios (4 engineer's + 5 this verification's own).

## CI/e2e

No separate CI/e2e pipeline defined in this repo's `test_strategy` beyond
`go build`/`go vet`/`go test ./...`, all run above.

## Verdict

**PASS.** Acceptance criteria all met, confirmed by independent live
exercise and direct code inspection, not by trusting the report. Two items
for the quality reviewer's judgment, both already flagged precisely rather
than silently absorbed:
1. Check 4c — the existing "no hook configured" tests do not reach a true
   no-op in an environment (like this sandbox) with a real global hook;
   consider isolating `GIT_CONFIG_GLOBAL`/`HOME` in those two tests.
2. Check 5 — the default-merge-message/rejecting-hook exit-90 gap is real,
   pre-existing, and structurally hard to fix without either a shared-library
   version bump or the git-internals string-matching this codebase avoids
   elsewhere; deferring it to a future ledger entry (as the report
   proposes) is the reasonable call.
