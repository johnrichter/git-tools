# LED-118 quality review — in-binary commit-message-format check

## Verdict: ACCEPT WITH FIXES (FIX-APPLIED)

The implementation is correct, genuinely delegating, and scoped exactly to
what LED-118's ledger entry asked for. Acceptance is met. Both flagged
findings are resolved: finding 1 is **fixed here** (the shipped tests were
worse than the test-engineer diagnosed — see below), finding 2 is
**confirmed as a defer**, with the defer's own justification strengthened by
a fact neither prior report had: the shared library destroys the evidence a
git-tools-side fix would need.

Files I changed:

- `internal/commitmsg/commitmsg_test.go` — host-config isolation + an
  assertion that it took effect.
- `internal/cli/merge_commit_message_hook_test.go` — same, for the no-hook
  end-to-end case; plus two named helpers and a corrected file header.
- `internal/cli/led118_verification_test.go` — removed one assert-nothing
  test whose documented premise this review's fix invalidates; corrected the
  stale doc comment it left behind.
- `internal/cli/merge.go` — one imprecise sentence of `--help` prose.

No production logic changed. `internal/commitmsg/commitmsg.go` is untouched.

---

## Finding 1 — shipped tests were coupled to the host's global git config: FIXED

**Decision: yes, fix the shipped suite. Applied.**

The test-engineer's diagnosis was right and understated it. This host sets a
global `core.hooksPath` (`/usr/local/lib/dd-git-hooks`, verified: `git config
--get core.hooksPath` resolves it from a bare `git init` in a fresh temp dir,
and `commit-msg` there is executable). Because `core.hooksPath` replaces the
hooks directory wholesale, four of the seven cases in `commitmsg_test.go`
were resolving that host hook instead of the one they planted:

| case | claimed to exercise | actually exercised, pre-fix |
| --- | --- | --- |
| `TestCheck_NoHookConfigured_IsNoOp` | the no-op branch | the host's hook, passing because its secrets scan accepts a benign message |
| `TestCheck_NonExecutableHookFile_IsNoOp` | the `+x` check | the host's hook; the executability branch was never reached |
| `TestCheck_DefaultGitHooksDir_Accepts` | default `.git/hooks` resolution | the host's hooks dir; the planted file was never run |
| `TestCheck_DefaultGitHooksDir_Rejects` | default `.git/hooks` resolution | the host's hook, which happens to chain onward to the repo-local hook |

The last row is the sharpest point, and it upgrades this from "green but
hollow" to "actively fragile". `TestCheck_DefaultGitHooksDir_Rejects` passed
only because this particular host hook ends with
`$dir/run-local-hooks -git_dir="${git}" -hooktype="${hook}" -- "$@"`, i.e. it
chains to the repo-local hook and forwards its stderr — which is the only way
the test's own `strings.Contains(refusal.Message(), "rejected: bad format")`
assertion could have been satisfied. On a host with a global `commit-msg` hook
that does **not** chain (the common case), that test fails outright. So the
pre-fix suite was not merely green-for-the-wrong-reason; it was red on other
people's machines, for a reason that has nothing to do with the code under
test. That alone settles the "should the shipped suite be fixed" question.

**Fix.** `GIT_CONFIG_GLOBAL`/`GIT_CONFIG_SYSTEM` set to `os.DevNull`, applied
once in `commitmsg_test.go`'s own `initRepo` so every case in the package
inherits it, plus `requireNoHooksPathConfigured` asserting the override
actually took effect. This is the only technique that works here: a local
`core.hooksPath` override (the idiom `internal/cli`'s `initRepo` already uses
for `core.excludesfile`) cannot express "unset", so it cannot test the
default-`.git/hooks` resolution branch at all. `commitmsg.Check` resolves git
config through this process's own environment (`sysops.Options.Env` is nil, so
the child inherits), which is why a process-level `t.Setenv` reaches both the
resolution calls and the hook invocation. No test in these packages calls
`t.Parallel`, so `t.Setenv` is safe.

For `internal/cli`, the same override is applied to the one affected case via
a named `isolateHostGitConfig` helper, with `requireNoCommitMsgHook`
asserting the claim the case's name makes (no `core.hooksPath` resolved
anywhere, and nothing at the default `.git/hooks/commit-msg`). `runGit` and
`runCLI` both leave `cmd.Env` nil, so one process-level call covers the
fixture's git calls and the CLI subprocess alike. The three other end-to-end
cases were already isolated by construction — `installCommitMsgHook` sets a
**local** `core.hooksPath`, which wins over the host's global.

**Independent confirmation the fix bites**, from wall time alone — the host
hook is a 24 MB scanning binary, so its absence is measurable:

| | pre-fix | post-fix |
| --- | --- | --- |
| `internal/commitmsg` package | 7.20s | **0.145s** |
| `TestMerge_CommitMessageHook_NoneConfigured_ProceedsNormally` | 6.03s | **0.19s** |

A 50x and a 30x drop with identical assertions is direct evidence that the
host's hook really was being invoked before and really is not now.

## Finding 2 — default-merge-message hook rejection surfaces as exit 90: DEFER CONFIRMED

**Decision: CONFIRM the test-engineer's judgment. Not fixed here.** I reached
this independently, and found one fact that makes the defer conclusive rather
than merely reasonable.

Both prior reports argue the fix belongs in the shared
`claude-shared-tooling/go/git` module because classification there is
structural (conflicted files) rather than textual. True, but incomplete. The
decisive line is `go/git@v0.4.0/merge.go:105-112`:

```go
if mergeErr != nil {
    files, _ := r.conflictedFiles(ctx)
    _, _ = r.git(ctx, "merge", "--abort")
    if len(files) > 0 {
        return nil, &ConflictError{Op: "merge", Files: files}
    }
    return nil, fmt.Errorf("git: merge %s: %w", strings.Join(branches, " "), mergeErr)
}
```

`git merge --abort` runs **before** the error returns. The structural signal
that would distinguish a refused commit step from any other merge failure —
`MERGE_HEAD` resolvable with zero conflicted files — is therefore already
destroyed by the time git-tools sees the error. A structural fix inside
git-tools is not merely inelegant; it is **impossible**. The only in-repo
option left is string-matching git's own wording ("Not committing merge; use
'git commit' to complete the merge."), which this codebase's classification
precedent avoids everywhere and which LED-118's own ledger entry warns
against. The fix must live in the shared module, behind a version bump and a
repin — not small, not self-contained, not this task.

Two supporting points, both verified:

- **Pre-existing and untouched.** The diff adds a pre-check on the
  `--message` path only; the classification path is git's own native hook
  invocation, unchanged. Any pre-LED-118 merge with a rejecting native hook
  and no `--message` hit the same fallthrough.
- **Not a safety issue.** Nothing lands: the QA characterization test
  asserts both branches unmoved, and I reproduced it (`exit=90
  status=internal`, `internal.git.merge_failed`).

The `TestLED118_QA_DefaultMergeMessage_RejectingHook_ClassifiesAsInternal`
characterization test that pins exit 90 should **stay**. Its comment says
plainly that it pins today's behavior rather than asserting it is correct, so
a future fix trips it deliberately instead of silently diverging from the
ledger entry below.

Feedback entry text for the orchestrator to file is in **Plan feedback**
(FB19). Do not file it from this worktree.

---

## Findings from the normal correctness/design pass

### Blocking

None.

### Major

Only finding 1, fixed above.

### Minor (no code change; recorded deliberately)

1. **`internal/cli/merge.go:76-80` — `--help` prose overstated the ordering.**
   "before anything else runs" is not true: the detached-HEAD, self-target and
   linked-worktree refusals all precede it. Reworded to "ahead of both the
   content scan and the signing gate", which is what the code does and what
   the adjacent inline comment already said correctly. Same correction applied
   to `merge_commit_message_hook_test.go`'s file header. **Fixed** (prose
   only).
2. **`internal/cli/merge.go:190` — the refusal carries no structured
   context.** `signing.Refusal` exposes `Context()`; `commitmsg.Refusal` does
   not, so `finishDiagnostic` is passed `nil` and the rejecting hook's path is
   available only inside the human-readable message. A machine consumer that
   wants to know *which* hook refused has to parse prose. No acceptance
   criterion requires it and adding it is scope creep; recorded, not changed.
3. **`internal/cli/merge.go:187` — `internal.git.commit_message_hook_failed`
   namespaces a `commitmsg` failure under `internal.git.*`.** Defensible: the
   hook is git's own mechanism, the underlying resolution really is `git
   config` / `git rev-parse`, and it reads consistently with the neighbouring
   `internal.git.head_check_failed`. No change.
4. **`internal/commitmsg/commitmsg.go:65-93` — a hook that rewrites the
   message file has that rewrite discarded.** git's commit-msg contract allows
   a hook to normalize the message (Gerrit's Change-Id hook is the canonical
   example); `Check` reads only the exit status. This is not a regression —
   whether such a rewrite reaches the commit is decided entirely by git's own
   native invocation during `git merge`, exactly as before LED-118 — and no
   acceptance criterion covers it. I did not verify git's native rewrite
   behavior on the merge path, so it stays in Residual risk rather than being
   asserted either way.
5. **`.task-reports/led118-commit-format-test-verification.md`** says
   `led118_verification_test.go` carries five tests; it now carries four. That
   report is a dated artifact and this review supersedes it on that point
   rather than rewriting it.

### Checks that came back clean

- **Scope.** `merge --message` is genuinely the only site that composes a new
  commit message. Confirmed independently: `resign`/`sign` reuse the original
  message byte-for-byte through `commit-tree`'s stdin, `branch create` never
  mints a commit. `internal/hooks` only *writes* `core.hooksPath` and never
  resolves an effective hooks directory, so `commitmsg.hooksDirectory` is not
  a second, driftable resolver.
- **Ordering.** Placing the check before `scanGate` and the signing gate is
  right and consistent with this file's established precedence (cheap
  read-only preconditions first, so nothing pays for a doomed merge). It does
  preempt exit 20 (`gate_negative`) when a rejecting message meets an
  all-empty range, but the detached-HEAD and linked-worktree refusals already
  preempt exit 20 the same way — this is the file's existing convention, not a
  new inconsistency.
- **Dry run.** The check applies to `--dry-run` too, which is correct: every
  other precondition does, and a preflight that hides a refusal it can see is
  worse than useless. (Note that `--dry-run` merges with `--no-commit`, so
  git's native hook invocation does not fire there at all.)
- **Guard.** `message != ""` rather than `Flags().Changed("message")` matches
  how the shared library treats an empty `MergeOptions.Message`. Correct.
- **Security.** The scratch message file is `os.CreateTemp` (0600,
  owner-only) and `defer`-removed; the hook is invoked with `Dir: dir` so a
  hook that derives its repo from `$(pwd)` works; the invoked program is
  whatever the repository already configured, so this introduces no new trust
  boundary. Context cancellation is honored via `sysops.Run(ctx, ...)`.
- **Idiom.** `Refusal`'s shape — including the `Error()` method no caller
  currently uses — mirrors `internal/signing.Refusal` exactly, so it is
  local-idiom parity, not dead code invented here.

---

## Fixes applied

| file | change | why |
| --- | --- | --- |
| `internal/commitmsg/commitmsg_test.go` | `initRepo` sets `GIT_CONFIG_GLOBAL`/`GIT_CONFIG_SYSTEM` to `os.DevNull`; new `requireNoHooksPathConfigured` asserts it took | four of seven cases resolved the host's global hook instead of the planted one; one of them was red on any host whose global hook does not chain |
| `internal/cli/merge_commit_message_hook_test.go` | new `isolateHostGitConfig` + `requireNoCommitMsgHook`, both used by the no-hook case; corrected file header | same coupling, end-to-end; the case now asserts the condition its name claims |
| `internal/cli/led118_verification_test.go` | removed `TestLED118_QA_HostGlobalHookIsInherited_NotActuallyNoHook`; corrected the stale comment on `TestLED118_QA_TrueNoHook_MergeUnaffected` | that test only logged or skipped — it could never fail — and its documented premise ("the engineer's own tests do not isolate") is false as of this review. Keeping it would ship a comment that misdescribes the suite. No coverage lost: it asserted nothing, and the isolation it argued for is now asserted directly in both affected cases |
| `internal/cli/merge.go` | one `Long` sentence reworded | said "before anything else runs"; three refusals precede it |

`TestLED118_QA_TrueNoHook_MergeUnaffected` is deliberately kept as-is despite
now overlapping the fixed shipped case: it isolates at the subprocess level
with its own hand-built fixture, so it remains independent evidence by a
different route rather than an echo. The duplicated signing fixture is a
maintenance cost worth watching, not worth churning now.

---

## Re-verification

Build and vet on the final tree:

```
$ go build ./...
(no output, exit 0)
$ go vet ./...
(no output, exit 0)
```

Focused run of every LED-118 test after the fixes:

```
$ go test ./internal/commitmsg/... -count=1 -v -timeout 5m
--- PASS: TestCheck_NoHookConfigured_IsNoOp (0.02s)
--- PASS: TestCheck_DefaultGitHooksDir_Accepts (0.02s)
--- PASS: TestCheck_DefaultGitHooksDir_Rejects (0.02s)
--- PASS: TestCheck_NonExecutableHookFile_IsNoOp (0.02s)
--- PASS: TestCheck_RelativeHooksPath_ResolvedAgainstRepo (0.02s)
--- PASS: TestCheck_AbsoluteHooksPath_UsedDirectly (0.02s)
--- PASS: TestCheck_ConfiguredHooksPathWithNoCommitMsgScript_IsNoOp (0.02s)
ok  	github.com/johnrichter/git-tools/internal/commitmsg	0.145s

$ go test ./internal/cli/... -count=1 -v -timeout 15m -run 'TestMerge_CommitMessageHook|TestLED118'
--- PASS: TestLED118_QA_HookAccepts_MergeLands (6.52s)
--- PASS: TestLED118_QA_HookRejects_MergeBlockedNothingMoves (5.92s)
--- PASS: TestLED118_QA_TrueNoHook_MergeUnaffected (0.20s)
--- PASS: TestLED118_QA_DefaultMergeMessage_RejectingHook_ClassifiesAsInternal (6.03s)
--- PASS: TestMerge_CommitMessageHook_NoneConfigured_ProceedsNormally (0.19s)
--- PASS: TestMerge_CommitMessageHook_Accepts_ProceedsAndHookActuallyRan (6.03s)
--- PASS: TestMerge_CommitMessageHook_Rejects_RefusesBeforeAnythingLands (5.91s)
--- PASS: TestMerge_CommitMessageHook_NoExplicitMessage_HookStillRunsNatively (6.01s)
ok  	github.com/johnrichter/git-tools/internal/cli	36.809s
```

Full suite, fresh, on the final tree — started **after** every edit in this
review, so this run is the authoritative evidence
(`go test ./... -count=1 -timeout 30m`, log at `/tmp/led118_qr_final.log`):

```
?   	github.com/johnrichter/git-tools/cmd/git-tools	[no test files]
ok  	github.com/johnrichter/git-tools/internal/cli	1204.425s
ok  	github.com/johnrichter/git-tools/internal/commitmsg	0.162s
ok  	github.com/johnrichter/git-tools/internal/gitexec	106.038s
ok  	github.com/johnrichter/git-tools/internal/hooks	0.092s
ok  	github.com/johnrichter/git-tools/internal/result	0.006s
ok  	github.com/johnrichter/git-tools/internal/signing	17.391s
ok  	github.com/johnrichter/git-tools/internal/worktreeclean	41.293s
ok  	github.com/johnrichter/git-tools/worktree-gate/detect	6.454s
?   	github.com/johnrichter/git-tools/worktree-gate/detect/cmd/worktree-gate	[no test files]
ok  	github.com/johnrichter/git-tools/worktree-gate/fixtures	0.006s
ok  	github.com/johnrichter/git-tools/worktree-gate/lifecycle	147.310s
EXIT=0
```

Every package `ok`, exit 0, no failures and no skips reported. 468 test
functions on the final tree (`go test ./... -list '.*'`), against the
test-engineer's 464 `--- PASS` — the difference is this review's own edits
plus subtests counted differently by `-list` than by `-v`; the run above is
the load-bearing evidence, not the count.

`internal/commitmsg` at 0.162s (was 7.20s) and `internal/cli` unchanged at
~1204s are both consistent with the isolation fix: only the four commitmsg
cases and one cli case stopped invoking the host's hook, so the 20-minute
package's total is untouched — which is exactly what FB21 below is about.

---

## Test-suite assessment

**Adequate, and materially stronger after this review's fix.** Coverage is
real: the delegation mechanism is unit-tested across every resolution branch
(unset, relative, absolute, directory-without-script, non-executable), and
all three required outcomes are proven end-to-end through a live CLI
subprocess with two independently written hook scripts — one exit-code-driven,
one content-inspecting, which together prove the hook's argument passing and
message contents are real rather than exit-code plumbing. The reject case
asserts the exit code, the diagnostic code, the hook's own stderr surfacing,
a marker file proving the hook ran, and — the assertion that earns its keep —
that neither branch moved, i.e. the signing gate never re-signed for a doomed
merge.

Gaps the test-engineer should close (none blocking):

1. **`commitmsg.Check`'s error return path is untested** — a failing `git
   config` / `git rev-parse` invocation. Already noted by the test-engineer.
   Low-risk plumbing, but it is the one branch with no coverage at all.
2. **The double invocation on the happy path is undocumented by any test.**
   The implementer's report states an accepting hook runs twice (this check,
   then git's own native run under `git merge -m`). No test pins that, so
   nothing would notice if it changed — and it is the behavior most likely to
   surprise someone whose hook has a side effect.
3. **No case covers a hook that rewrites the message file** (minor 4). Worth
   one test, mostly to pin what git-tools promises rather than to change it.

---

## Residual risk

- **An accepting hook runs twice per `merge --message`** — once here, once
  natively inside `git merge`. Harmless for a pure format check, observable
  for a hook with side effects. Correctly scoped out: suppressing the native
  run needs a `--no-verify` equivalent in the shared module's `MergeOptions`.
- **Hook-rewritten messages** (minor 4): `Check` discards the rewrite;
  whether git's native invocation applies it on the merge path is unverified
  by me, and unchanged by this task either way.
- **Exit 90 for a rejecting hook on the default merge message** — accepted
  knowingly, pinned by a characterization test, filed as FB19 below.
- **The rest of `internal/cli`'s fixtures remain coupled to the host's
  global git hooks** — out of this task's scope, filed as FB21 below.

---

## Plan feedback

Three entries for the orchestrator to file in
`marketplace/.dat/git-tools-gate-privacy-rework/feedback.json`. IDs continue
from the current maximum (FB18). `criticality = impact * urgency`, matching
the existing entries. **Not written from this worktree**, per dispatch.

```json
{
  "id": "FB19",
  "title": "A commit-msg hook rejecting git's own default merge message surfaces as internal/exit 90, not a precondition",
  "source_task_id": "git-tools-gate-privacy-rework:LED-118-quality-review",
  "feedback": "When git-tools merge runs with no --message, git composes its own default merge message and runs it past the repository's configured commit-msg hook natively. A rejection there surfaces as internal.git.merge_failed (exit 90, status internal), not as a precondition. Reproduced live: exit=90, status=internal, message 'merge qa-feature: git: merge --no-ff -S qa-feature: exit 1: <hook stderr> Not committing merge; use git commit to complete the merge.' Both branches stay at their pre-merge tips, so this is a reporting defect, not a safety one. Root cause is in the shared claude-shared-tooling/go/git v0.4.0 Merge(): on failure it lists conflicted files and only builds a *ConflictError when that list is non-empty (merge.go:105-112). A commit-msg rejection leaves zero conflicted files -- the content merge succeeded, only the commit step was refused -- so it falls through to a generic fmt.Errorf wrap that git-tools' handleGitError can only classify as internal. Decisively: that same block runs 'git merge --abort' BEFORE returning the error, so the structural signal a git-tools-side fix would need (MERGE_HEAD resolvable with zero conflicted files) is already destroyed by the time git-tools sees the failure. A fix inside git-tools is therefore impossible without string-matching git's own wording, which this codebase's classification precedent avoids everywhere. Predates LED-118 and is untouched by it: LED-118's in-binary check covers only the message git-tools itself composes (merge --message). Pinned today by TestLED118_QA_DefaultMergeMessage_RejectingHook_ClassifiesAsInternal, which characterizes exit 90 rather than endorsing it.",
  "proposed_solution": "In claude-shared-tooling/go/git's Merge(), capture the structural signal before 'git merge --abort' runs -- MERGE_HEAD resolvable with zero conflicted files means the content merge succeeded and only the commit step was refused -- and return a distinct typed error for it (e.g. *CommitRefusedError, carrying git's captured stderr). Then map that type onto precondition_unmet (exit 30) in git-tools' gitresult/handleGitError, matching D4's own exit-code correction. Needs a go/git minor version bump plus a repin in git-tools; update the characterization test in the same change.",
  "why_it_matters": "Exit 90/internal makes the CLI emit 'retry; if this persists, file an issue with the log output' for a deterministic, operator-fixable condition where retrying is guaranteed to fail: the commit message did not satisfy the repository's own hook. It is also the one gap the new in-binary check cannot cover, so it reports in the least actionable vocabulary the CLI has, to both operators and agents.",
  "impact": 3,
  "urgency": 2,
  "criticality": 6,
  "added": "2026-08-30T00:00:00Z"
}
```

```json
{
  "id": "FB20",
  "title": "git-tools hooks install silently displaces a global commit-msg hook, and inerts LED-118's new check",
  "source_task_id": "git-tools-gate-privacy-rework:LED-118-quality-review",
  "feedback": "hooks install writes its script to <repo>/<hooks-dir>/<hook> (defaults: .githooks, pre-commit) and then points the repository's local core.hooksPath at that directory (internal/hooks/hooks.go Install, internal/cli/hooks.go flag defaults). git's core.hooksPath replaces the hooks directory for EVERY hook name, not just the one installed -- so in any repository that ran git-tools hooks install, a globally configured commit-msg (or pre-push, or post-commit) hook stops firing, because .githooks/ carries no such file. Verified structurally by reading both files; separately verified that this sandbox's own host carries exactly such a global hook (core.hooksPath = /usr/local/lib/dd-git-hooks, an executable commit-msg secrets scanner). This is precisely the displacement risk LED-118's ledger entry names: 'installing one must delegate to any pre-existing global hook rather than displacing it.' LED-118 is not the cause -- its in-binary check sidesteps hook installation entirely and is correct. But the interaction matters: after hooks install, commitmsg.Check resolves .githooks/, finds no commit-msg, and correctly no-ops, so the new check is inert on exactly the repositories git-tools manages most closely, and the hook it was meant to honor has already been silenced there.",
  "proposed_solution": "Make hooks install preserve what core.hooksPath displaces. Preferred: before setting core.hooksPath, resolve the currently effective hooks directory and generate a thin delegating stub in the new directory for every hook the displaced one carries -- each stub execs the displaced hook, forwards \"$@\" and stdin, and propagates its exit status, the same delegate-don't-replace contract internal/commitmsg already implements for commit-msg. Minimum acceptable: detect the displacement and report it as a caveat on the install result, naming each hook that will stop firing, instead of landing it silently.",
  "why_it_matters": "A repository can lose its organization's own secrets-scanning commit-msg or pre-push hooks the moment someone runs git-tools hooks install, with no warning anywhere in the result. That is a security regression introduced by a security tool, and it stays invisible until a secret lands.",
  "impact": 4,
  "urgency": 3,
  "criticality": 12,
  "added": "2026-08-30T00:00:00Z"
}
```

```json
{
  "id": "FB21",
  "title": "internal/cli test fixtures inherit the host's global git hooks -- correctness coupling, and a likely large share of FB16's wall time",
  "source_task_id": "git-tools-gate-privacy-rework:LED-118-quality-review",
  "feedback": "internal/cli's initRepo (integration_test.go) already isolates one host git setting (core.excludesfile) but not core.hooksPath, so every scratch fixture inherits whatever global hooks the host configures, and every fixture commit runs them. Two measurements taken while fixing LED-118's own tests, same assertions before and after adding GIT_CONFIG_GLOBAL=/dev/null and GIT_CONFIG_SYSTEM=/dev/null: the internal/commitmsg package went 7.20s -> 0.145s, and TestMerge_CommitMessageHook_NoneConfigured_ProceedsNormally went 6.03s -> 0.19s. On this host the inherited hook is a 24 MB scanning binary that runs on every commit, which works out to roughly 1.8s per fixture commit. LED-118's own tests are fixed; the rest of the package is not. This is both a correctness risk (a fixture can exercise a host hook instead of the one it planted, and pass or fail for reasons unrelated to the code under test -- exactly the defect found in commitmsg_test.go) and a plausible primary cause of FB16 (internal/cli wall time ~1040-1200s with no host contention).",
  "proposed_solution": "Add the two GIT_CONFIG_GLOBAL/GIT_CONFIG_SYSTEM=os.DevNull overrides to internal/cli's own initRepo, alongside the core.excludesfile isolation already there, and to the worktree-gate packages' fixture helpers. runGit and runCLI both leave cmd.Env nil, so a process-level t.Setenv reaches fixtures and CLI subprocesses alike; no test in these packages calls t.Parallel. Needs its own task rather than a drive-by: the blast radius is every test in a ~20-minute package, and any case that quietly depends on host global config will surface as a failure that must be judged individually. Measure the package's wall time before and after and fold the result into FB16.",
  "why_it_matters": "Test outcomes currently depend on the host's git configuration, so the suite can be green on a developer machine and red in CI (or the reverse) for reasons that have nothing to do with the code. The same coupling appears to dominate the suite's wall time, which is FB16's open question.",
  "impact": 3,
  "urgency": 2,
  "criticality": 6,
  "added": "2026-08-30T00:00:00Z"
}
```

No spec or plan correction is needed for LED-118 itself: the ledger entry's
scope, the plan's D7 narrowing, and what was built agree.
