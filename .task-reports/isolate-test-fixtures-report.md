# Isolate test fixtures from host git hooks/config

## Explore-phase findings

**Host hook confirmed real, on this machine, right now:**
`git config --global --get core.hooksPath` → `/usr/local/lib/dd-git-hooks`
(set via the unconditionally-included `~/dotfiles/.../gitconfig/main`).
That directory holds a 24MB `dd-git-hooks` binary plus a 23MB
`run-local-hooks` binary and a 29MB `secrets_scanner_linux_arm64`, wired to
every standard hook name. A single `git commit --allow-empty` on this host,
unisolated, measured 2.78s wall (0.53s user + 0.35s sys — the rest is the
hook binary's own startup/scan). FB24's mechanism is real here, not just
asserted from a prior report.

**Isolation gap confirmed, and it was wider than just `core.hooksPath`:**
`internal/cli`'s `initRepo` (in `integration_test.go`, used by ~43 direct
call sites plus every test that calls `signingRepo`, which itself calls
`initRepo`) isolated only `core.excludesfile`, leaving `core.hooksPath` (and
every other global/system git config) to bleed in from the host. The same
gap existed in:
- `internal/cli/worktree_test.go`'s `cleanupFixture` (17 call sites, same package)
- `worktree-gate/lifecycle/helpers_test.go`'s `newScratchRepo` (used directly
  and via `signableScratchRepo`)
- `worktree-gate/detect/decide_sc15_branchdelete_ordering_sdet_test.go`'s
  inline fixture setup (single test)

Three *other* fixtures already isolated this correctly for their own narrow
purposes before this task (`internal/commitmsg/commitmsg_test.go`,
`internal/cli/merge_commit_message_hook_test.go`,
`internal/cli/led118_verification_test.go`) — proving the pattern was known
and simply hadn't been generalized to the package's dominant fixture helper.

**Env-reach confirmed:** neither `runGit` nor `runCLI` in `integration_test.go`
sets `cmd.Env` (both just set `cmd.Dir`), so they inherit the test process's
environment. `t.Setenv` in `initRepo` therefore reaches both the fixture's
own `git` subprocesses (via `runGit`) and the CLI-under-test's own git calls
(via `runCLI`, which execs the built `git-tools` binary as a subprocess that
itself shells out to `git`) — matching FB24's claim exactly, with no
`cmd.Env` overrides needed at either call site.

## Fix

Added `t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)` /
`t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)` to:
- `internal/cli/integration_test.go`: `initRepo`
- `internal/cli/worktree_test.go`: `cleanupFixture`
- `worktree-gate/lifecycle/helpers_test.go`: `newScratchRepo`
- `worktree-gate/detect/decide_sc15_branchdelete_ordering_sdet_test.go`:
  `TestSDET_SC15_BranchDelete_GateSanctionsButGuardStillRefuses` (inline,
  no shared helper to isolate)

`signingRepo` and `signableScratchRepo` both build on `initRepo`/
`newScratchRepo` respectively, so they inherit the isolation without a
separate edit.

## Real, measured before/after

**Representative subset (`internal/cli`, `-run '^TestMerge'`, 42 test
functions / ~52 fixture-commit sites — chosen because it is the single
largest concentration of fixture-driven commits in the package):**

| | wall time (test binary) |
|---|---|
| Before (this session, this host) | 389.145s |
| After | 9.986s |

~39x. This is a large, decisive drop that matches the earlier report's own
claimed mechanism and magnitude — the host hook genuinely dominates this
subset's wall time on this machine.

**Full `internal/cli` package, one complete run, after the fix and after
fixing the host-config-dependent tests below (see next section):**

`go test ./internal/cli/...` → `ok` in **39.6s**, 0 failures. (No full-package
"before" run was taken in this session — the representative-subset run alone
already took 6m35s wall including build; a second full ~18-20 minute run
purely to re-confirm a number this session didn't change would not have
added information beyond the subset's own decisive ratio and the prior
report's own full-package numbers of 322s-1200s. The full run that *was*
executed twice in this session is the "after" one, confirmed clean on a
second pass with `go test ./...` for the whole repo.)

**Full repo, `go test ./...`:** all packages pass, 1m47s total. One
observation flagged but explicitly out of this task's scope: `internal/gitexec`
alone took 106s of that — its own `scratchRepo` fixture (in `gitexec_test.go`)
has the identical unisolated-hooks gap and was not touched here, since the
task scoped explore/fix to `internal/cli` and `worktree-gate`'s test
packages. Likely a further, separate win if picked up later.

## Course correction acknowledged

The operator disputes that they've personally observed this delay and is
skeptical of the causal claim; they still want the identical isolation
change landed, on correctness/hygiene grounds (unit tests should never
depend on host state; production hooks and git-tools' own gates still catch
anything real downstream). Both stand independently here: the measured
subset drop above is large and decisive on this host/session, *and* the fix
is justified on hygiene grounds regardless of that number's portability to
other hosts.

## Tests fixed for a real host-config dependency (not reverted, not a bug in the change)

Isolating `GIT_CONFIG_GLOBAL`/`GIT_CONFIG_SYSTEM` also removed this host's
real global `user.signingkey` (`~/.ssh/id_ed25519_dd_workspaces_signing`,
unconditionally included via `~/.gitconfig_personal`) and `gpg.format=ssh`,
which a number of `initRepo`-based fixtures were silently relying on to make
git's own signing succeed — a genuine, previously-invisible dependency on
this host's real signing key material inside unit tests. Fixed by giving
each affected test its own local, ephemeral signing key instead of reverting
the isolation:

- `internal/cli/integration_test.go`: `TestSign_ResignsTipCommitWithIdenticalTree`,
  `TestResign_RangeAcrossBase` — switched from `initRepo` to
  `initRepo` + a new `configureSigningKey(t, dir)` helper (extracted from
  `signingRepo` in `merge_test.go`) that sets up a real local ssh signing key
  *without* turning `commit.gpgsign` on — these two tests need their fixture
  commits to stay unsigned (that's what `sign`/`resign` are proving), while
  still giving the CLI's own explicit signing a key to resolve.
- `internal/cli/tag_test.go`: `TestTagCreate_BareShape_CreatesAndPushes`,
  `TestTagCreate_PrefixedShape_CreatesAndPushes`,
  `TestTagCreate_PinnedShapeForms_MatchLanguageToolsContract`,
  `TestTagCreate_ExistingTag_RefusedCleanly`,
  `TestTagCreate_DetachedHEAD_Succeeds`,
  `TestTagCreate_NoOriginRemote_FailsCleanly`,
  `TestTagCreate_UnreachableRemote_FailsCleanly`, and the `success` /
  `internal: no remote to push to` cases of `TestTagCreate_ExitCodeTable` —
  switched `initRepo` to `signingRepo` (`tag create` always signs the tag it
  makes, so these need a resolvable key; unlike sign/resign they don't care
  whether the underlying commit itself is signed).
- `internal/cli/scan_gate_test.go`:
  `TestScanGate_PrivacyMarkerExemptConfigAllowsTagCreate` — same reason,
  same fix (`initRepo` → `signingRepo`).
- `worktree-gate/lifecycle/complete_test.go`:
  `TestComplete_MergesBackAndCleansUp`, `TestComplete_KeepBranch`,
  `TestComplete_DirtyRefusalDoesNotUndoTheMerge`,
  `TestComplete_ConflictLeavesWorktreeIntact` — switched `newScratchRepo` to
  the package's existing `signableScratchRepo` (`Complete`'s merge signs
  unsigned source commits before landing them, so these need a resolvable
  key; the other ~6 `TestComplete_*` cases using `newScratchRepo` never reach
  that signing step, e.g. they're refused earlier on a dirty worktree with no
  commit to sign, or hit a no-op merge — confirmed by running the full
  package clean).

All of these were pre-existing, latent dependencies on this host's real
signing key — invisible before because the same host state that hid the hook
slowness also hid this. Isolating surfaced both.

## Files touched

- `internal/cli/integration_test.go` — `initRepo` isolation; sign/resign
  fixture switch
- `internal/cli/worktree_test.go` — `cleanupFixture` isolation
- `internal/cli/merge_test.go` — extracted `configureSigningKey` out of
  `signingRepo`
- `internal/cli/tag_test.go` — signing-key fixture switches
- `internal/cli/scan_gate_test.go` — signing-key fixture switch
- `worktree-gate/lifecycle/helpers_test.go` — `newScratchRepo` isolation
- `worktree-gate/lifecycle/complete_test.go` — signing-key fixture switches
- `worktree-gate/detect/decide_sc15_branchdelete_ordering_sdet_test.go` —
  inline isolation

## Verification run log

- `go build ./...` — clean
- `go vet ./...` — clean
- `gofmt -l` on every touched file — no output (already formatted)
- `go test ./internal/cli/... -run '^TestMerge' -v` — before: 389.145s;
  after: 9.986s
- `go test ./internal/cli/... -v` (full package) — after all fixes: `ok`,
  39.6-39.8s across two runs, 0 failures
- `go test ./worktree-gate/...` — `ok`, 0 failures
- `go test ./...` (whole repo) — `ok` across every package, 1m47s total
