# D4 single-call-site lint — report

## Task

Add a permanent, automated lint asserting that a real git `tag` subcommand
invocation (`gitexec.RunGit(ctx, dir, "tag", ...)`-shaped, not a plain
substring match on "tag") occurs at exactly one call site in the Go module:
`internal/cli/tag.go`'s `tag create` verb. Follow-up to D1's backup-marker
migration off `git tag` onto `refs/backup/...` via `git update-ref`.

## Finding

The lint already exists in this worktree's checked-out history, at
`internal/cli/tag_call_site_test.go`, function
`TestRawGitTagCallSite_ConfinedToTagGo`. It landed as part of the D4
tag-create-hardening work (commits `bd190bf`, `288f9d8`), predating this
task's own worktree/branch creation — not authored in this session. No
production code or test code needed to change; the deliverable was already
satisfied on `chore/d4-single-call-site-lint` at dispatch time.

## What the existing lint does

- Walks the module tree from `internal/cli`'s own package root outward
  (`filepath.Abs("../..")`), skipping `.git`, `.claude` (other agents'
  worktrees), `.task-reports`, `.dat`, and all `_test.go` files.
- For every `.go` file, finds each `gitexec.RunGit(` call via `extractCalls`
  (shared with `worktree_test.go`'s own balanced-paren extractor — reused,
  not duplicated) and inspects that call's top-level arguments (via
  `splitTopLevelArgs`, which respects nested parens/brackets/braces and
  quoted strings) for a literal `"tag"` argument.
- Fails loudly, naming the exact file path, if any such site's path does
  not end in `internal/cli/tag.go`.
- Fails if it finds **zero** such sites too — a self-check against the
  guard silently going blind (e.g. if `tag.go`'s own invocation shape ever
  changes so the pattern no longer matches anything).
- This is a source-text/call-shape check scoped explicitly (in the test's
  own doc comment) to the casual-regression case — a determined evasion
  (building the args slice separately, then splatting it in) is out of
  scope by design, matching the task's own "small, targeted lint" framing.

This matches the task's every requirement: detects the real subcommand
invocation shape (not a bare substring match on "tag"), confined to the one
sanctioned site in `tag.go`, implemented as a Go test (this repo's existing
idiom for structural/call-site guards — `worktree_test.go` has an analogous
"no site outside X mentions Y" pattern the tag lint's author explicitly
matched), fails with file+context on violation, self-checks against going
silently blind.

## Verification performed this session

1. Located the sole current legitimate call site: `internal/cli/tag.go`,
   `newTagCreateCmd`'s `RunE`, three `gitexec.RunGit(ctx, ".", "tag", ...)`
   calls (`-s` create, `-v` verify, `-d` rollback-delete) — all inside the
   one verb, exactly as the task described.
2. Ran `TestRawGitTagCallSite_ConfinedToTagGo` standalone: passes.
3. **Negative test (task step 5):** temporarily added a fourth, fake site —
   `gitexec.RunGit(ctx, ".", "tag", "-l")` inside a throwaway function in
   `internal/cli/push.go` (clearly outside `tag.go`).
   - Re-ran the test: it failed, naming the exact injected file:
     `raw \`git tag\` call site outside tag.go: .../internal/cli/push.go`.
   - Removed the fake addition, confirmed `git status --short` is clean
     again, and reran: passes.
4. Ran the full verification suite from a clean tree:
   `gofmt -l .` (no output), `go build ./...` (clean),
   `go vet ./...` (clean), `go test ./... -count=1` (all packages `ok`).

## Acceptance

- Lint detects a real git-tag-subcommand invocation shape, not a bare
  substring match on "tag" — met (uses `gitexec.RunGit(` + top-level
  argument split, verified against the actual `newTagCreateCmd` shape).
- Sole legitimate call site confirmed first, by reading `tag.go` — met.
- Implemented as a Go test, matching this repo's own existing
  "exactly one call site" idiom (`worktree_test.go`) — met.
- Fails loudly and names the file on a second site — met, verified live
  with an injected fake site.
- Fake addition added, confirmed failure, removed, confirmed pass again —
  met.
- No production code touched — met (the one edit made this session, to
  `push.go`, was the temporary verification probe, fully reverted;
  `git status --short` is clean).

## Sanity result

`gofmt -l .`, `go build ./...`, `go vet ./...`, `go test ./... -count=1`
all pass on the clean tree, both before and after the injection/removal
cycle used to verify the lint.

## Assumptions & deviations

- No code change was needed or made — this report documents verification
  of a pre-existing implementation, not new authorship. Given the explicit
  instruction not to touch production code and to keep the lint
  "test-side," and that the existing test already meets every acceptance
  criterion byte for byte, re-implementing an equivalent second lint would
  have been pure duplication with no acceptance benefit.
- The temporary fake call site used for verification (step 5) was reverted
  before finishing; nothing from it is left in the tree.

## Hand-off notes

- For the test-engineer: the existing `TestRawGitTagCallSite_ConfinedToTagGo`
  is itself the permanent lint: no further test authorship needed for this
  task specifically, though the surrounding suite (already passing) remains
  in scope for whatever else the test-engineer stage covers.
- For the quality-reviewer: confirm no residual diff outside this report
  file — `git status --short` was clean at hand-off, and the fake
  call-site probe used to verify the guard was added and removed within
  this session, never left in a commit.
