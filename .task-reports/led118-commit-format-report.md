# LED-118 — in-binary commit-message-format check, delegating to the existing hook

Status: DONE — all acceptance criteria met.

## What LED-118's own ledger entry asked for

`workspace/.dat/ledger.md`, LED-118 ("The commit-message standard is
documented and nothing checks it"): no mechanism today looks at a commit
message's own format before a commit lands. The ledger's own proposed fix
names a commit-msg hook as the stronger mechanism, but flags the real risk:
installing one must **delegate to any pre-existing global hook rather than
displacing it** — read the configured value, invoke it as a program
forwarding arguments/input, propagate its exit status, treat an absent
delegate as a no-op.

The plan's own D7 section narrows this to a single, scoped task: "Add an
in-binary check. It must delegate to any existing global hook, never replace
it." Not a new format-policy engine — a mechanism that makes sure an
already-configured `commit-msg` hook actually gets a chance to run against a
commit git-tools is about to mint, and that its verdict is honored.

## What was built

New package `internal/commitmsg` (`internal/commitmsg/commitmsg.go`):

- `Check(ctx, dir, message)` resolves the repository's own configured
  `commit-msg` hook — `core.hooksPath` (local/global/system, read through
  `git config --get`, exactly as git itself resolves it; absolute path used
  directly, relative path resolved against `dir` per git-config(1)) if set,
  else the default `.git/hooks` (via `git rev-parse --git-path hooks`).
- If no executable `commit-msg` file exists there, `Check` is a true no-op:
  `(nil, nil)`. It never falls back to a format rule of its own.
- If one exists, `Check` runs it exactly as git would: the message written
  to a scratch file, that file's path passed as the hook's sole argument,
  the hook's own exit code decides the verdict.
- A non-zero exit becomes a `*Refusal` (mirrors `internal/signing`'s
  `Refusal` shape: `Code()`/`Message()`/`Advice()`), carrying the hook's own
  stderr/stdout as the rejection detail, under a new diagnostic code
  `precondition_unmet.git.commit_message_hook_rejected`.

Call site: `internal/cli/merge.go`, the one sanctioned verb that composes a
brand-new commit message before minting a commit. `merge --message <msg>`
now runs `commitmsg.Check` on that message before the signing gate and the
content scan — a message a configured hook would reject is refused before
either of those (the signing gate's re-signing, especially) does any work
for a merge that was always going to be rejected on its message alone.

## Files touched

- `internal/commitmsg/commitmsg.go` (new)
- `internal/commitmsg/commitmsg_test.go` (new — unit tests)
- `internal/cli/merge.go` (wired the check in; updated `Long` help text and
  the exit-30 doc line)
- `internal/cli/merge_commit_message_hook_test.go` (new — end-to-end tests)
- `.task-reports/led118-commit-format-report.md` (this file)

## Why only `merge`, not `resign`/`sign`/`branch create`

Read first, per the dispatch instruction, before writing anything:

- `resign`/`sign` (`internal/cli/resign.go`, backed by
  `claude-shared-tooling/go/git`'s `Repo.Resign`) rewrite a commit's
  signature via `commit-tree`, reusing the **original, unchanged message**
  byte-for-byte (`readCommit`'s message extraction, `commitTree`'s stdin).
  They never compose a new message, so there is nothing for a message-format
  check to examine — applying one here would be inventing a check where the
  ledger names none.
- `branch create` (`internal/cli/branch.go`) only moves or creates a ref; it
  never mints a commit at all.
- `merge` is the one verb that composes a genuinely new message (the
  `--message` flag) before minting a commit, so it is the one real call
  site.

This matches the dispatch's own instruction to match the ledger's stated
scope exactly, not invent a broader one.

## Scope boundary confirmed by a live probe

Verified (via a scratch repo and a planted rejecting hook, in
`internal/cli/merge_commit_message_hook_test.go`'s
`..._NoExplicitMessage_HookStillRunsNatively` case) that when `--message` is
omitted, git's own default merge-message composition already runs any
configured `commit-msg` hook natively through the real, unmodified `git
merge` call underneath — untouched by, and unaffected by, this change. The
explicit `commitmsg.Check` call only ever covers the one message git-tools
itself composes and hands to git ahead of time (`--message`); it is a
no-op when that flag is absent, by design, not an oversight.

One adjacent, pre-existing rough edge surfaced while probing this boundary:
today, a `commit-msg` hook rejecting a merge's *git-default* message (no
`--message` given) surfaces as `internal.git.merge_failed` (exit 90) via
git's own opaque stderr, not as a named `precondition_unmet`. That
classification gap is real but is git's own native hook invocation on a
path this task's explicit check by design never reaches (no
git-tools-composed message exists there to check) — reclassifying it would
mean parsing git's own hook-failure stderr heuristically, which is exactly
the kind of hook-logic reimplementation LED-118's own fix explicitly warns
against. Flagging it here rather than silently absorbing it into this
task's scope; a follow-up ledger item may be warranted if that
classification gap matters on its own.

## Acceptance

- **In-binary check added, as part of a sanctioned commit-creating verb** —
  met: `merge`'s `--message` path now runs `commitmsg.Check`.
- **Delegates to any existing global hook, never replaces it** — met:
  `Check` resolves and invokes exactly the hook `core.hooksPath`/the default
  `.git/hooks` already names; it carries no format policy of its own.
- **No-op when no global hook is configured** — met: `TestCheck_NoHookConfigured_IsNoOp`
  and `TestMerge_CommitMessageHook_NoneConfigured_ProceedsNormally`.
- **Tests: hook accepts, hook rejects, no hook at all — verb respects each
  outcome** — met:
  - Accept: `TestMerge_CommitMessageHook_Accepts_ProceedsAndHookActuallyRan`
    (asserts a hook-written marker file to prove the hook actually ran, not
    just that the outcome happened to match).
  - Reject: `TestMerge_CommitMessageHook_Rejects_RefusesBeforeAnythingLands`
    (asserts exit 30, the new diagnostic code, the hook's own rejection text
    surfaced in the message, the hook actually ran, and — critically —
    that neither the target nor the source branch moved: the signing gate
    never got a chance to do its re-signing work for a doomed merge).
  - No hook: `TestMerge_CommitMessageHook_NoneConfigured_ProceedsNormally`
    (merge lands normally, `--message` reaches the commit unchanged).
  - Unit-level coverage of the delegation mechanism itself (default
    `.git/hooks`, relative and absolute `core.hooksPath`, a non-executable
    hook file, a configured directory with no `commit-msg` script) lives in
    `internal/commitmsg/commitmsg_test.go`.

## Sanity result

- `go build ./...` — pass, no output.
- `go vet ./...` — pass, no output.
- `go test ./... -timeout 30m` — pass, all packages:
  ```
  ok  	github.com/johnrichter/git-tools/internal/cli            1195.432s
  ok  	github.com/johnrichter/git-tools/internal/commitmsg         7.202s
  ok  	github.com/johnrichter/git-tools/internal/gitexec          105.996s
  ok  	github.com/johnrichter/git-tools/internal/hooks              0.091s
  ok  	github.com/johnrichter/git-tools/internal/result              0.005s
  ok  	github.com/johnrichter/git-tools/internal/signing            17.386s
  ok  	github.com/johnrichter/git-tools/internal/worktreeclean      41.249s
  ok  	github.com/johnrichter/git-tools/worktree-gate/detect         6.390s
  ok  	github.com/johnrichter/git-tools/worktree-gate/fixtures       0.003s
  ok  	github.com/johnrichter/git-tools/worktree-gate/lifecycle    146.860s
  ```
  (`internal/cli` at ~20 minutes, matching the dispatch's own expected
  range; exit code 0 overall.)

## Assumptions & deviations

- Scoped the check to `merge`'s explicit `--message` only, per the "match
  the ledger's stated scope, do not invent a broader check" instruction —
  `resign`/`sign` never compose a new message and `branch create` never
  mints a commit, so none of the three carries anything for this check to
  examine (documented above, not silently skipped).
- The hook, once resolved, is invoked directly by this check whenever
  `--message` is given — in addition to whatever `git merge` itself does
  natively underneath when it actually mints the commit. There is no
  `--no-verify`-equivalent plumbed through the shared `go/git` module's
  `MergeOptions` to suppress the second, native invocation without a
  version bump to that separately-owned shared library, which is out of
  this task's scope. A `commit-msg` hook is expected to be a pure read/exit
  check (format linting), so running an accepting hook twice on the happy
  path costs one extra, cheap subprocess and no observable side effect; a
  rejecting hook never reaches the second invocation at all, since this
  check refuses before `git merge` ever runs.

## Hand-off notes

- Test-engineer / quality-reviewer: the four end-to-end cases in
  `internal/cli/merge_commit_message_hook_test.go` are the acceptance
  evidence for "respects each outcome" — worth an independent run given
  they share the `signingRepo` ssh-keygen fixture and take ~6-9s apiece.
  `internal/commitmsg/commitmsg_test.go` exercises the delegation mechanism
  itself in isolation (no CLI/binary build needed, fast).
  - Note that if this session's own worktree-gate/governance-git wrapper
    intercepts raw `git commit`/`git merge` Bash invocations, direct
    ad hoc probing (outside `go test`) of this behavior will be refused —
    exercise it through `go test`, as these tests already do, not through
    a raw shell command.
  - Consider whether the classification gap noted above (a
    git-default-message hook rejection surfacing as `internal`/exit 90
    rather than `precondition_unmet`) merits its own, separate ledger entry;
    it predates this change and sits outside this task's stated scope.
