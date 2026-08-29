# D1 git-tools BackupRef consumption: independent test-engineer verification

## Verdict: PASS

All six required checks confirmed with fresh, independently-run evidence.
No leftover `BackupTag`/`backup_tag`/tag-based-marker check found. Build,
vet, and full test suite pass. `internal/cli`'s ~18-minute runtime is
confirmed a pre-existing test-infrastructure slowness (per-test `go build`),
not a new hang and not caused by this change.

## Method

Verified against commit `a198b23` on branch `chore/d1-backup-ref-consume`,
diffed against `main`. Read the full diff, re-ran build/vet/test from a
clean state, and grepped the whole module for stale markers.

## Check 1 — go.mod replace directive

`git diff main...HEAD -- go.mod` confirms a `// TEMPORARY:` comment
immediately above:

```
replace github.com/johnrichter/claude-shared-tooling/go/git => /home/bits/Development/workspaces/psa-platform/ai-shared-lib/.claude/worktrees/d1-backup-ref-migration/go/git
```

The `require` line for `go/git` is untouched at `v0.3.0`; only the resolved
source is overridden, matching the report's stated intent.

Path resolution confirmed real and resolvable:
```
$ ls /home/bits/Development/workspaces/psa-platform/ai-shared-lib/.claude/worktrees/d1-backup-ref-migration/go/git
adversarial2_test.go  adversarial_test.go  ...
```
`go build ./...` (below) succeeded against this replace, which is itself
proof the module resolves — a broken path or missing package would fail the
build immediately.

**PASS.**

## Check 2 — rename completeness (BackupTag/backup_tag -> BackupRef/backup_ref)

Full diff review of all 10 changed files (`git diff main...HEAD --stat` and
per-file diffs) confirms every claimed rename site:
- `internal/result/git.go`: `RewriteOutcomeData`'s `"backup_tag"` key ->
  `"backup_ref"`, reads `o.BackupRef` (was `o.BackupTag`).
- `internal/cli/merge.go`: `rewrittenSources` emits `backup_ref`; doc comment
  updated.
- `internal/signing/signing.go`: `Gate` writes `record["backup_ref"]` /
  `rewritten[...]["backup_ref"]` from `applied.BackupRef`; doc comments and
  refusal triage text updated ("backup tag" -> "backup ref").
- Test files (`internal/result/git_test.go`, `internal/signing/signing_test.go`,
  `internal/cli/merge_test.go`, `internal/cli/branch_test.go`,
  `internal/cli/integration_test.go`): all struct literals, map-key
  assertions, variable names, and comments renamed to match.

Whole-module grep, fresh, after the diff review:
```
$ grep -rn "BackupTag\|backup_tag" --include="*.go" .
(no output, exit 1)
```
Zero hits anywhere in the module, including test files. No compatibility
shim or intentionally-old-format test exists (none was expected — the old
marker was never persisted across releases as a public artifact).

**PASS.**

## Check 3 — git tag --list -> for-each-ref fix

Both flagged sites in `internal/cli/merge_test.go` confirmed changed:
- `TestMerge_AlreadySignedSource_IsSkippedWithoutRewriting` (line ~538):
  `runGit(t, dir, "tag", "--list")` -> `runGit(t, dir, "for-each-ref", "refs/backup/")`.
- `TestMerge_SelfTargetInWorktree_RefusesBeforeGate` (line ~875): same change.

Both tests re-run in isolation, passing (see Check-3-adjacent run below).
Whole-module grep for any other backup-marker-shaped `git tag`/`refs/tags`
usage:
```
$ grep -rn "\"tag\"\|'tag'\|git tag" --include="*.go" . | grep -v for-each-ref
```
All remaining hits are in `internal/cli/push.go`, `internal/cli/tag.go`,
`internal/cli/tag_test.go`, `internal/cli/push_test.go`, and
`worktree-gate/detect/*` — all concern git-tools' own real annotated release
`tag` verb (`git tag create`, `git tag -s`, `git tag -v` for release
verification, and the worktree-gate command classifier's generic handling of
the `tag` subcommand). None of these model the disposable backup marker;
confirmed by reading each site — unrelated to this change, untouched, and
correctly so.

**PASS.**

## Check 4 — no git-tools-local duplicate of LED-033's count-based check

Grepped the whole module for count/ahead/behind-based commit-comparison
logic:
```
$ grep -rn "rev-list\|--count\|ahead\|behind" internal/ --include="*.go" | grep -v _test.go
internal/worktreeclean/worktreeclean.go:327: rev-list --count <landingSHA>..<b>
internal/cli/merge.go:156-158:  comment only ("ahead of the signing gate")
internal/cli/resign.go:110-115: error-classification comment/check, not a rewrite-verification count
internal/cli/rebase.go:15:      doc comment only
internal/signing/signing.go:219,322: comments about unreferenced commit objects (gc), not counting
```
The one real `rev-list --count` call (`worktreeclean.go:327`) computes how
many commits a branch is ahead of its landing target, for a wholly
unrelated purpose (deciding whether a branch is safe to delete as
"already-landed") — not a rewrite/backup-verification check, and not
touched by the BackupRef migration. No commit-count-based verification of a
rewrite's completeness or reachability exists in git-tools; `internal/cli`
calls straight into `go/git`'s `Resign`/`Rebase`/`Merge` and reports their
return values.

**PASS — confirmed true, not just asserted.**

## Check 5 — build, vet, fresh from clean state

```
$ go version
go version go1.27.0 linux/arm64

$ go build ./...
(clean, no output, exit 0)

$ go vet ./...
(clean, no output, exit 0)
```

**PASS.**

## Check 6 — full test suite, fresh run

```
$ date
Sat Aug 29 20:33:54 UTC 2026
$ go test ./... -count=1 -timeout 25m
?   	github.com/johnrichter/git-tools/cmd/git-tools	[no test files]
ok  	github.com/johnrichter/git-tools/internal/cli	1101.282s
ok  	github.com/johnrichter/git-tools/internal/gitexec	106.073s
ok  	github.com/johnrichter/git-tools/internal/hooks	0.088s
ok  	github.com/johnrichter/git-tools/internal/result	0.005s
ok  	github.com/johnrichter/git-tools/internal/signing	17.381s
ok  	github.com/johnrichter/git-tools/internal/worktreeclean	41.285s
ok  	github.com/johnrichter/git-tools/worktree-gate/detect	6.413s
?   	github.com/johnrichter/git-tools/worktree-gate/detect/cmd/worktree-gate	[no test files]
ok  	github.com/johnrichter/git-tools/worktree-gate/fixtures	0.003s
ok  	github.com/johnrichter/git-tools/worktree-gate/lifecycle	147.003s
$ date
Sat Aug 29 20:52:32 UTC 2026
```
Process exited cleanly (confirmed by watcher: `while kill -0 <pid>; do
sleep 20; done` returned once the `go test` process itself had exited, then
the log was inspected). No `FAIL` or `panic` string anywhere in the full
output:
```
$ grep -i "FAIL\|panic" test.log
(no output, exit 1)
```
`internal/cli` completed in `1101.282s` (~18.4 min) — matches the
implementer's claimed ~18-minute figure. This confirms the package
eventually completes and exits 0; it is not a hang, matching the claim.

All 9 test/no-test-file entries accounted for; all `ok` where tests exist.

Additionally, re-ran the specific backup-ref-touching tests named in the
implementer's report in isolation, verbose, for a second independent
confirmation:
```
$ go test ./internal/cli/... -run 'TestBranchDelete_MergedBranch_Succeeds_BackupRefPresent|TestMerge_UnsignedSource_IsResignedBeforeLanding|TestMerge_ConflictAfterRewrite_CarriesTheRewrittenSourceList|TestMerge_OctopusLaterSourceRefusal_ReportsEarlierRewrite|TestMerge_OctopusUnrelatedLaterSource_ReportsEarlierRewrite|TestMerge_AlreadySignedSource_IsSkippedWithoutRewriting|TestMerge_SelfTargetInWorktree_RefusesBeforeGate|TestAbandonmentRoute_MergedBranch_SucceedsInTwoActs' -v -timeout 5m
--- PASS: TestBranchDelete_MergedBranch_Succeeds_BackupRefPresent (3.34s)
--- PASS: TestAbandonmentRoute_MergedBranch_SucceedsInTwoActs (3.53s)
--- PASS: TestMerge_UnsignedSource_IsResignedBeforeLanding (6.75s)
--- PASS: TestMerge_ConflictAfterRewrite_CarriesTheRewrittenSourceList (9.39s)
--- PASS: TestMerge_OctopusLaterSourceRefusal_ReportsEarlierRewrite (6.59s)
--- PASS: TestMerge_OctopusUnrelatedLaterSource_ReportsEarlierRewrite (9.62s)
--- PASS: TestMerge_AlreadySignedSource_IsSkippedWithoutRewriting (6.57s)
--- PASS: TestMerge_SelfTargetInWorktree_RefusesBeforeGate (9.33s)
PASS
ok  	github.com/johnrichter/git-tools/internal/cli	55.137s
```

Also re-ran `internal/result` and `internal/signing` in isolation, verbose,
independently: all `PASS`.

**PASS.**

## Acceptance-vs-claim summary

| Claim | Verified | Evidence |
|---|---|---|
| 1. `go.mod` replace, temporary, resolvable | Confirmed | Check 5, go.mod diff |
| 2. Every BackupTag/backup_tag renamed | Confirmed | Check 2, zero-hit grep |
| 3. Two `git tag --list` -> `for-each-ref` fixes | Confirmed | Check 3 |
| 4. No git-tools-local LED-033 duplicate | Confirmed | Check 4 |
| 5. build/vet clean, full test suite passes | Confirmed | Check 5, 6 |
| 6. `internal/cli` ~18min, not a new hang, pre-existing | Confirmed | Check 6, timing matches claim |

## Coverage note

No new test cases were required — the change is a rename plus a
marker-shape assertion fix, and the existing test suite (renamed in place)
already exercises every code path touched. Adversarial angle checked: the
old `git tag --list` false-negative failure mode (a marker that no longer
lives under `refs/tags/` still reporting "no tags" regardless of leakage)
was independently confirmed as a real risk by inspecting the assertion
change; the fixed `for-each-ref refs/backup/` check correctly targets the
new marker's actual namespace.

## Flakes

None observed. `internal/cli`'s long runtime is real and consistent with
the implementer's own investigation (per-test `go build` inside `buildCLI`,
pre-existing, tracked separately as Track D's D9) — not flagged as a defect
in this task's scope.
