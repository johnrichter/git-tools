# D1 git-tools BackupRef consumption: quality review

## Verdict: ACCEPT WITH FIXES

Two fixes applied by this review, both re-verified. The rename itself is
complete and correct; the two `git tag --list` -> `for-each-ref` migrations are
functionally sound and were verified empirically, not from memory. One
user-facing help string still described the marker as a tag and is now
corrected. The JSON output-contract break is real, sanctioned by the upstream
brief, has zero downstream consumers today, and carries a release obligation
recorded below.

## Scope and method

Reviewed commit `a198b23` plus the test-engineer's `8d378a6` on branch
`chore/d1-backup-ref-consume`, worktree
`/home/bits/Development/workspaces/psa-platform/git-tools/.claude/worktrees/d1-backup-ref-consume`.

Read the full `git diff main...HEAD`, then re-derived every claim
independently: whole-worktree greps (all file types, not just `*.go`), a
workspace-wide consumer search, an empirical `git for-each-ref` semantics probe
in a scratch repo, an empirical probe of Go's behaviour on a missing `replace`
target, a mutation check on the assertion I restored, and a fresh full
`go test ./...`.

## Finding 1 (MAJOR, fixed) — stale user-facing help text called the marker a tag

`internal/cli/branch.go:110-111` (before this review):

```
Long: `delete removes name as a compare-and-swap against expected-head, tagging
the old head for recovery first.
```

After the migration nothing is tagged: `go/git`'s `Branch` delete path writes
the marker with `git update-ref` under `refs/backup/`
(`ai-shared-lib/.../go/git/branch.go:53-58`). The published help text for
`git-tools branch delete` therefore described a mechanism that no longer
exists, and pointed a recovering operator at the wrong namespace.

Both prior passes missed it because both greps targeted the identifiers
(`BackupTag`, `backup_tag`, the phrase "backup tag"). This site uses the verb
"tagging" and the word "backup" never appears in it, so it matched neither.

**Fixed:** now "recording the old head under `refs/backup/` for recovery
first", with the paragraph reflowed to the file's wrap width. No test asserts
this string (`TestHelp_EverySubcommandHasHelp` only requires `Usage:` to be
present, `internal/cli/integration_test.go:119-135`), so nothing else had to
change with it.

Confirmed no other stale tag-shaped prose remains: case-insensitive greps for
`backup`, `tagging`, `tagged`, `tags the`, `tag it`, `recover`, `restore`, and
`undo` across all `*.go` and `*.md` in the worktree return only real
release-tag verb sites (`internal/cli/tag.go`, `push.go`, `root.go`,
`scan.go`, and the `worktree-gate/detect` command classifier) plus the
shape-agnostic refusal advice at `internal/signing/signing.go:181`
("recover any listed rewrite from its backup ref"), which stays correct for a
plain ref. `README.md` mentions neither tags nor refs.

## Finding 2 (MINOR, fixed) — the migrated assertions dropped tag-namespace coverage

`internal/cli/merge_test.go:538` and `:875` replaced
`git tag --list == ""` with `git for-each-ref refs/backup/ == ""`. The new
check is the right one, but it is strictly narrower than what it replaced: the
old assertion also proved the merge path created no *tag*, and "a marker
written as a tag again" is the exact regression this migration exists to
prevent. The brief states the invariant explicitly
(`marketplace/.dat/git-tools-remediation-brief.md:203`: "`git tag -l` never
lists it").

**Fixed:** both sites now call one helper that asserts both namespaces empty,
`requireNoRecoveryMarker` (`internal/cli/merge_test.go:73-87`). Extracted
rather than duplicated because the rationale comment would otherwise appear
twice.

Mutation-verified, not assumed. Injecting `runGit(t, dir, "tag",
"mutation-probe")` ahead of the call made the test fail as intended:

```
--- FAIL: TestMerge_AlreadySignedSource_IsSkippedWithoutRewriting (6.60s)
    merge_test.go:555: a skipped range created a tag where no marker belongs: "mutation-probe"
```

The probe was removed; the tree carries only the helper.

## Finding 3 (MINOR, not fixed, report only) — the hand-off note overstates a CI gap

`.task-reports/d1-git-tools-report.md:167-171` says CI running `go test ./...`
"will intermittently or consistently panic on timeout" and asks for a
follow-up task. `.github/workflows/ci.yml:111-120` already runs
`go test -timeout 20m ./...`, already documents the per-test `go build` cause,
and already names the durable fix as harness-followup FB18. Nothing is missing.

The real, current fact worth escalating is headroom, not configuration:
`internal/cli` measured 1101s against the 1200s budget — about 8% left. That
is FB18 priority input, not a defect in this change, and out of this task's
scope.

## Output-contract finding: the JSON key rename is a breaking change

This is the item flagged for explicit consideration, so it gets its own
section. It is **accepted**, with a release obligation.

Three production sites emit the renamed key:

| Site | Key path in the JSON result |
|---|---|
| `internal/result/git.go:70` | `data.backup_ref` — resign, rebase, `branch delete` |
| `internal/signing/signing.go:185,187` | gate record `backup_ref`; refusal `context.rewritten[].backup_ref` |
| `internal/cli/merge.go:312` | merge's `context.rewritten[].backup_ref` |

For a CLI whose entire contract is machine-readable output, renaming a `data`
key is a breaking change. Four checks decided it:

1. **Sanctioned upstream.** The brief mandates it and states the consequence:
   `marketplace/.dat/git-tools-remediation-brief.md:220` — "Rename the reported
   field from `backup_tag` to `backup_ref` ... This changes the JSON output
   shape, so bump the minor version."
2. **Zero consumers today.** Grepped every sibling repo under
   `/home/bits/Development/workspaces/psa-platform` (including `marketplace`'s
   plugins/hooks/skills and the three knowledge repos, excluding `.git` and
   worktrees) for `backup_tag`. The only hit is the brief line above. No hook,
   script, skill, test, or doc outside git-tools reads the key by its old name.
3. **No compatibility window is warranted.** The value is a disposable,
   per-invocation recovery marker with no cross-release meaning, so
   dual-emitting both keys would add a permanent wart to buy nothing.
4. **The break has no release-note channel, which is the actual risk.**
   git-tools carries no in-repo version constant and no CHANGELOG (already
   filed as FB13); it versions purely by git tag, latest `v0.10.0`. So unless
   the release step records it, the only trace of this break lives in this
   branch's task reports.

**Release obligation (carry into the merge):** cut `v0.11.0`, not `v0.10.1`,
and state the `backup_tag` -> `backup_ref` key rename in the release notes.
Any consumer added between now and then must be repinned deliberately.

## go.mod replace directive: safe to leave, cannot silently become permanent

Confirmed on four points.

- **Findable marker.** `go.mod:41-45` carries a five-line `// TEMPORARY:`
  comment naming the branch, the reason, and the exact removal condition
  (operator approves the ai-shared-lib merge, a new `go/git` tag is cut).
  Findable with `grep -n TEMPORARY go.mod`.
- **Fails loudly off this machine.** git-tools imports `go/git` in production
  code, so Go resolves the replacement on every build, vet, and test. Verified
  the failure mode in a scratch module rather than trusting recall:

  ```
  p.go:3:8: example.com/dep@v1.0.0: replacement directory /nonexistent/path/that/does/not/exist does not exist
  ```

  CI (`ci.yml` `go vet`, `go build`, `go test`) therefore fails on the runner,
  where that absolute path does not exist. The branch is merge-blocked
  mechanically, not by convention alone. (An unimported replaced module would
  be silent — not the case here.)
- **Reverting is one line.** The `require` stays at `v0.3.0` and `go.sum` is
  untouched by the whole diff, so deleting the `replace` restores a coherent,
  resolvable module state; the repin is that deletion plus a version bump.
- **Caveat.** No policy check forbids `replace` in this repo
  (`scripts/surface-hygiene.sh` has no `go.mod` rule). The safety net is the
  missing-path build failure, so a `replace` pointing at a *published* module
  would not be caught by anything. Adequate for this case; noted so nobody
  mistakes the failure mode for a lint.

## Marker-shape checks re-derived independently

**No tag-shaped handling of the marker remains anywhere.** git-tools contains
no `git tag -v`, `tag -d`, or `refs/tags/` handling of the marker; every
surviving `tag` site is the real annotated-release-tag verb. `go/git`'s
production sources contain zero `"tag"` git invocations at all
(`grep -rn '"tag"' --include="*.go" ... | grep -v _test.go` returns nothing),
so there is no path left that could verify the marker as a tag object. No
display path prepends or strips `refs/tags/`; the only ref-prefix trims in the
module are `refs/heads/` (`internal/worktreeclean/worktreeclean.go:501`,
`worktree-gate/lifecycle/complete.go:98`).

**`for-each-ref refs/backup/` is the right pattern.** Probed in a scratch repo
with git 2.55.0, with a decoy ref deliberately planted:

```
$ git for-each-ref refs/backup/
45b983b... blob	refs/backup/heads/main/1234-abc
$ git for-each-ref refs/backup          # no trailing slash
45b983b... blob	refs/backup/heads/main/1234-abc
$ git tag --list                        # backup ref never appears
v1.0.0
$ git for-each-ref refs/nothing/ ; echo "exit=$?"
exit=0
```

- Prefix-matches under `refs/backup/`, as `go/git` writes them
  (`refs/backup/<base>/<nanos>-<short>`, `go/git/ref.go:71`).
- The decoy `refs/backupdecoy` is **not** matched, with or without the trailing
  slash — no over-match, so the assertion cannot false-positive on a
  neighbouring namespace.
- Zero matches means empty stdout and exit 0, so `runGit`'s failure path does
  not fire and `!= ""` is a valid emptiness test.
- The old `git tag --list` check was genuinely vacuous after the migration: a
  live backup ref never appears in it. The migration fixed a real false
  negative.

**No behaviour difference for any real caller.** Both changed sites are test
assertions; no production code path changed shape. The one substantive
difference was the lost tag-namespace coverage, now restored (Finding 2).

Related, checked and clean: `git-tools push` resolves a ref through
`heads`/`tags` only (`internal/cli/push.go:99-121`), so backup refs are no
longer reachable by the push verb, and `git push --tags` can no longer leak
them — a footgun the migration removes. Nothing in `internal/worktreeclean`,
`internal/hooks`, or `worktree-gate` enumerates the marker's namespace, so no
enumerator needed updating.

## Fixes applied

| File | Change | Why |
|---|---|---|
| `internal/cli/branch.go:110-114` | help text: "tagging the old head" -> "recording the old head under `refs/backup/`" | the documented mechanism no longer existed |
| `internal/cli/merge_test.go:73-87, 551, 886` | added `requireNoRecoveryMarker`, both sites now assert `refs/backup/` **and** the tag namespace empty | the migration dropped coverage of the exact regression it prevents |

## Re-verification

Fresh run on the final tree, after both fixes, from this worktree.

```
$ gofmt -l .
(clean)
$ go vet ./...
(clean)
$ go build ./...
(clean)
$ go test ./... -count=1 -timeout 25m
Sat Aug 29 21:05:33 UTC 2026
?   	github.com/johnrichter/git-tools/cmd/git-tools	[no test files]
ok  	github.com/johnrichter/git-tools/internal/cli	1102.484s
ok  	github.com/johnrichter/git-tools/internal/gitexec	106.338s
ok  	github.com/johnrichter/git-tools/internal/hooks	0.103s
ok  	github.com/johnrichter/git-tools/internal/result	0.005s
ok  	github.com/johnrichter/git-tools/internal/signing	17.431s
ok  	github.com/johnrichter/git-tools/internal/worktreeclean	41.395s
ok  	github.com/johnrichter/git-tools/worktree-gate/detect	6.425s
?   	github.com/johnrichter/git-tools/worktree-gate/detect/cmd/worktree-gate	[no test files]
ok  	github.com/johnrichter/git-tools/worktree-gate/fixtures	0.004s
ok  	github.com/johnrichter/git-tools/worktree-gate/lifecycle	147.452s
go_test_exit=0
Sat Aug 29 21:23:56 UTC 2026
```

Nine packages `ok`, exit 0, no `FAIL` and no `panic`. `internal/cli` at
1102.484s (~18.4 min) matches both prior runs, so neither fix moved the
runtime.

Targeted confirmation of the two directly-touched tests, run separately before
the full suite:

```
--- PASS: TestBranchDelete_MergedBranch_Succeeds_BackupRefPresent (3.34s)
--- PASS: TestAbandonmentRoute_MergedBranch_SucceedsInTwoActs (3.53s)
--- PASS: TestMerge_AlreadySignedSource_IsSkippedWithoutRewriting (6.57s)
--- PASS: TestMerge_SelfTargetInWorktree_RefusesBeforeGate (9.32s)
ok  	github.com/johnrichter/git-tools/internal/cli	22.783s
```

## Test-suite assessment

Adequate, and better after Finding 2's fix.

- Every renamed key is asserted by a test that reads it out of real CLI JSON
  (`branch_test.go:34`, `integration_test.go:902-907`, `merge_test.go:253,320,473,506`),
  so a partial rename could not stay green.
- `integration_test.go:906` resolves the reported marker with `git rev-parse`
  and compares it to the deleted branch's old head, which proves the value is
  a usable recovery handle and not just a non-empty string.
- The two "nothing leaked" assertions now cover both namespaces and were
  mutation-checked.
- No new test was needed for the help-text fix: the string is not
  behaviour-bearing, and `TestHelp_EverySubcommandHasHelp` already proves the
  help renders.
- Gap for the test-engineer, low priority and out of this task's scope: nothing
  asserts that a *successful* merge with a rewrite leaves the backup ref
  resolvable (the equivalent of `integration_test.go:906` on the merge path);
  `merge_test.go:253` only checks the key is non-empty.

## Residual risk

- **Accepted with caveat:** the `backup_ref` key rename ships as a silent
  breaking change unless the release step records it. Mitigated by zero current
  consumers; the obligation is stated above.
- **Merge-blocked by design:** the branch cannot merge until the `replace` is
  removed and `go/git` repinned. CI fails hard until then, which is the desired
  behaviour.
- `internal/cli` runs ~18.4 min against a 20 min CI budget (pre-existing,
  FB18).

## Plan feedback

1. **Release step:** `v0.11.0` minor bump plus a release note for the
   `backup_tag` -> `backup_ref` rename, per brief line 220. No CHANGELOG exists
   to carry it (FB13).
2. **Missing work item in the brief's own spec.** Brief line 202 states the
   marker "is disposable, and cleanup removes it". No code in either module
   deletes `refs/backup/*`: `go/git`'s only `update-ref -d` deletes the branch
   ref (`go/git/branch.go:61`), and neither module ever enumerates
   `refs/backup/`. So markers accumulate one per rewrite indefinitely, and
   unlike unreachable objects they are ref-anchored, so `git gc` will not
   reclaim what they hold. This is pre-existing (tag-based markers accumulated
   too, and worse, since `git push --tags` could leak them) and belongs to
   ai-shared-lib / Track D, not to this task. It needs a work item.
3. **FB18 headroom:** 1101s measured against the 1200s CI timeout.
4. Both prior reports' call for a CI-timeout follow-up is already satisfied by
   `ci.yml:111-120`; no new task needed for that specific point.
