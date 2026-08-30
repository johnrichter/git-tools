# Quality review — D4 single-call-site lint

**Verdict: FIX-APPLIED.** The implementer's central claim — that the pre-existing
`TestRawGitTagCallSite_ConfinedToTagGo` already satisfies D4's bar — is **wrong**.
The test was real and did what the report described, but it covered only **one of
three** git-spawn shapes present in this module, so it was green with real,
compiling `git tag` call sites outside `tag.go`. Extended the lint to cover all
three and to self-check its own shape list. No production code changed.

## What the report got right

- `internal/cli/tag_call_site_test.go` exists, and `TestRawGitTagCallSite_ConfinedToTagGo`
  does exactly what the report says: walks the module from `filepath.Abs("../..")`,
  skips `.git`/`.claude`/`.task-reports`/`.dat` and all `_test.go` files, extracts
  `gitexec.RunGit(` calls with a balanced-paren scanner (`extractCalls`, shared with
  `worktree_test.go:576`), splits top-level args (`splitTopLevelArgs`), and fails
  naming the file on a literal `"tag"` argument outside `internal/cli/tag.go`.
- It is a **call-shape** check, not a substring match on the word "tag". Verified
  against a live would-be false positive: `internal/cli/push.go:115` has
  `refKind = "tag"` and `internal/cli/tag.go:139` has `map[string]any{"tag": tagName}`
  in non-test code, and neither trips the guard.
- The sole sanctioned site is real and is one verb: `internal/cli/tag.go:174,189,195`
  (`-s` create, `-v` verify, `-d` rollback), all inside `newTagCreateCmd`'s `RunE`.
- The zero-sites self-check (`tag_call_site_test.go`) guards against the pattern
  going blind, and the implementer's injected-fake-site verification reproduces
  exactly as described.
- `git status --porcelain` carried no leftover trace of the implementer's probe.

## Blocking finding (fixed)

**`internal/cli/tag_call_site_test.go:53` — the guard read one git-spawn shape out of
three, so it could not assert "exactly one real subprocess call site in the whole Go
module."** It scanned only `gitexec.RunGit(`. This module spawns git through three
distinct shapes in non-test code:

| Shape | Non-test call sites | Covered before |
|---|---|---|
| `gitexec.RunGit(ctx, dir, words...)` | `internal/cli/*` throughout | yes |
| `sysops.Run(ctx, "git", []string{...}, opts)` | `internal/hooks/hooks.go:99`, `internal/cli/scan.go:399,417`, `internal/cli/config.go:198`, `internal/gitexec/gitexec.go:32`, `worktree-gate/lifecycle/gitutil.go:17` | **no** |
| `runGit(ctx, dir, words...)` — worktree-gate's own private variadic twin of `gitexec.RunGit`, `worktree-gate/lifecycle/gitutil.go:16` | `gitutil.go:29,35,40,46,54`, `reap.go:78` | **no** |

This is not the "determined evasion" the test's doc comment scopes itself out of
(building an args slice in a variable, then splatting it). These are ordinary,
already-present, first-class entry points — one of them inside `worktree-gate/lifecycle`,
which is precisely the worktree/backup-marker domain D1's migration was about. A new
verb typing `runGit(ctx, dir, "tag", ...)` there is the *casual* regression the guard
claims to catch, and it sailed straight through.

Proven live, not argued: with two fake sites injected — `runGit(ctx, dir, "tag", "-l")`
in `worktree-gate/lifecycle/gitutil.go` and `sysops.Run(ctx, "git", []string{"tag", "-l"}, ...)`
in `internal/cli/config.go` — `go build ./...` succeeded and the old guard reported
`ok github.com/johnrichter/git-tools/internal/cli`. Green with two real `git tag`
subprocess call sites outside `tag.go`.

Also confirmed the boundary of the surface, so the shape list is complete and not
merely longer: `grep` finds **no** `exec.Command` in non-test code, and the
`claude-shared-tooling/go/git@v0.4.0` library exposes no tag-creating API (its backup
markers go through `refs/backup/` in `ref.go:62`), so no library call can mint a tag
behind the guard's back.

## Fix applied

`internal/cli/tag_call_site_test.go` (+85/-11, test-side only):

- Added `gitSpawnShapes`, a table of every git-spawn call shape in the module, each
  tagged with whether its git words arrive as a variadic tail or as one
  `[]string{...}` argument. The walk now scans all three markers.
- Added `gitSubcommandWords(call, sliceArg)`: returns a call's git words (third
  argument onward for both shapes), and for `sysops.Run` first requires the program
  argument be literally `"git"` — so `internal/commitmsg/commitmsg.go:79`'s
  `sysops.Run(ctx, hook, ...)` is correctly ignored.
- Added `sliceLiteralElements`: splits a `[]string{...}` literal into elements,
  returning nil for a variable it cannot follow (`scan.go:399`, `gitexec.go:32`,
  `gitutil.go:17` all pass a variable — honestly out of reach, and documented as such).
- Added a **per-shape staleness self-check**: if any marker matches zero calls
  module-wide, the test fails and says the shape list is stale. Without this, renaming
  or retiring a runner silently blinds one lens while the suite stays green — the same
  failure mode as the original finding.
- Kept the original semantics deliberately: a literal `"tag"` anywhere in the git
  words, not only in first position. Over-flagging is correct for a guard — `git push
  origin tag v1` also moves a tag and should come up for review. Documented as intent.
- Kept the zero-sites self-check and the honest scope paragraph, updated for the
  wider surface.

## Re-verification (clean tree, after fix)

| Check | Result |
|---|---|
| Negative: `sysops.Run` fake site in `config.go` | FAIL, names `internal/cli/config.go` |
| Negative: `runGit` fake site in `gitutil.go` | FAIL, names `worktree-gate/lifecycle/gitutil.go` |
| Negative: `gitexec.RunGit` fake site in `push.go` | FAIL, names `internal/cli/push.go` |
| Negative: stale shape marker | FAIL, "this guard's shape list is stale, update gitSpawnShapes" |
| All probes reverted, `git status --porcelain` | only `internal/cli/tag_call_site_test.go` modified |
| `gofmt -l .` | clean (no output) |
| `go build ./...` | clean |
| `go vet ./...` | clean |
| `go test ./... -count=1` | all packages `ok` |

Each negative case was run in isolation, so each shape's detection is independently
demonstrated rather than inferred from one combined failure.

## Test-suite assessment

Adequate after the fix, and now self-defending on two axes (zero sites found, stale
shape marker). Residual, documented gap: a git spawn whose words are assembled into a
variable first is invisible to a source-text check. Closing it needs `go/analysis`
call-graph work — disproportionate to this task and correctly out of scope, but it is
the honest ceiling on what this lint asserts.

## Residual risk

- `extractCalls` balances parens without skipping string literals, so an unbalanced
  paren inside a string argument would mis-slice that one call. No such call exists
  today (the whole suite is green and the guard finds its expected sites), and the
  helper is pre-existing and shared with `worktree_test.go`. Not fixed here — outside
  this task's acceptance, and a fix belongs with the shared helper.
- The `runGit(` marker is name-based, so a third package adding its own differently
  named private git runner would need a `gitSpawnShapes` entry. The staleness check
  catches removals and renames of listed shapes, not brand-new unlisted ones.

## Plan feedback

D4's acceptance was written as "`git tag` at exactly one real subprocess call site in
the whole Go module", but the lint that landed under D4's own tag-create-hardening work
asserted the much narrower "one `gitexec.RunGit` call site." Worth folding into the
plan: when a call-site lint targets a subprocess surface, its acceptance should require
**enumerating every spawn entry point in the module** and a self-check that the
enumeration is still live. Unit-green never proves a guard is wired to the whole
surface it claims — this task is a clean instance of that class.

Process note: the implementer verified the guard exactly as instructed and reported it
faithfully; the miss was accepting the pre-existing test's own stated scope as the bar
instead of re-deriving the surface from the module. The prescribed
inject-a-fake-and-see-it-fail check passes for a guard that is blind in two of three
shapes, because the injection was made in the one shape already covered. A negative test
only tests the path it injects into.
