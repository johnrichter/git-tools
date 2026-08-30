# Quality review: isolate remaining test setup routines from host git config

**Verdict: ACCEPT** — no reviewer edits. `git diff HEAD` is empty, so the reviewed commits are
unchanged.

Branch `chore/isolate-remaining-test-fixtures`, rebased onto `origin/main` (`9dfc968`) as instructed.
Post-rebase commits: `e072bd9` (change), `8132b8f` (report style fix).

## Scope reviewed

Test-only isolation (`t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)` + `GIT_CONFIG_SYSTEM`) applied to
`internal/gitexec`, `internal/worktreeclean`, `internal/signing`, `internal/hooks`. Five `_test.go`
files, +24 lines, no production code.

## 1. Enumeration re-done by capability, not by helper name

`grep -rln 'exec.Command("git"' --include='*_test.go' .` reproduces the report's 17 hits. I did not
stop there — I traced every **repo-materializing** site in the four packages and then traced how every
test function in those packages obtains its repo directory, so a repo created through a helper defined
in a different file could not hide.

Every `git init` site in the four packages, and the isolation that covers it:

| Site | Covered by |
|---|---|
| `internal/gitexec/gitexec_test.go:34` (in `scratchRepo`) | same func, lines 24-25 |
| `internal/gitexec/gitignore_test.go:160` | inline, lines 153-154 |
| `internal/gitexec/gitignore_test.go:338` | inline, lines 335-336 |
| `internal/worktreeclean/worktreeclean_test.go:226` (in `cleanupFixture`) | same func, lines 223-224 |
| `internal/signing/signing_test.go:32` (in `scratchRepo`) | same func, lines 29-30 |
| `internal/hooks/hooks_test.go:19` (in `initScratchRepo`) | same func, lines 16-17 |

Consumer trace — no unisolated path to a repo exists:

- `internal/gitexec`: all 6 tests in `gitexec_test.go` and all 5 in `probe_adversarial_test.go` take
  their dir from `scratchRepo`. `gitignore_test.go`'s 16 tests take theirs from `scratchRepo` except
  the two inline-isolated ones and `:138`, which deliberately creates no repo.
- `internal/worktreeclean`: all 11 repo-using tests call `cleanupFixture`. The `worktree add` calls at
  `:126`, `:180`, `:248` all operate on a `cleanupFixture` repo, inside the same test that set the env.
- `internal/signing`: all 4 repo-using tests call `scratchRepo`.
- `internal/hooks`: all 4 repo-using tests call `initScratchRepo`.

Also checked for alternative repo-materializing capabilities in these packages (`clone`, `worktree
add`, hand-built `.git` dirs, `git.Open`): only `git.Open` on an already-isolated `cleanupFixture` /
`scratchRepo` dir. No `TestMain`, so no package-level setup escaping test scope.

**Nothing in scope was missed.** The four named packages are fully isolated.

Out-of-scope hits are all already isolated, contrary to what the implementer's report leaves
ambiguous: `internal/commitmsg/commitmsg_test.go:27-28`, `worktree-gate/lifecycle/helpers_test.go:73`,
`worktree-gate/detect/decide_sc15_branchdelete_ordering_sdet_test.go:45`, and all four
`internal/cli` init sites (`config_test.go:33`, `integration_test.go:131`, `worktree_test.go:100`,
`led118_verification_test.go:146`). By capability enumeration, **every repo-creating test site in the
repository is now isolated** — this closes the isolation thread, no follow-up package remains.

## 2. Production code untouched — confirmed

`git diff origin/main --name-only -- internal/gitexec internal/worktreeclean internal/signing
internal/hooks` returns exactly the five `_test.go` files. Whole-branch diff vs `origin/main` is those
five files plus the implementer's report. `go mod tidy -diff` clean, `go mod verify` all verified,
`go/githooks v0.6.0` present in `go.mod:11`.

## 3. Premise verified as real, not assumed

The host does carry a global hook: `git config --global --get core.hooksPath` →
`/usr/local/lib/dd-git-hooks` (and `core.excludesfile` → `~/.gitignore`). The mechanism behind the
165s is real, so the isolation is load-bearing rather than hygiene in the three committing packages.

## 4. Gates run fresh after the rebase

Matched CI (`.github/workflows/ci.yml`) rather than only the four commands in the report:

| Gate | Result |
|---|---|
| `gofmt -l .` | no output |
| `go build ./...` | clean |
| `go vet ./...` | clean |
| `golangci-lint run ./...` (local 2.13.2, CI pins 2.12.2) | 0 issues |
| `shellcheck scripts/*.sh` | clean |
| `bash scripts/surface-hygiene.sh` | 0 violations |
| `go test -timeout 20m ./...` (whole repo) | all packages ok, exit 0 |
| CI merge-signing gate: `go test ./internal/cli -run TestMerge -count=1 -v` | 59 PASS, 0 SKIP |

Four packages, three fresh `-count=1` runs — timings stable to ±0.02s, zero flake:

| Package | Run 1 | Run 2 | Run 3 | Report claim |
|---|---:|---:|---:|---:|
| `internal/gitexec` | 0.971s | 0.959s | 0.962s | ~0.98s |
| `internal/worktreeclean` | 0.754s | 0.746s | 0.747s | ~0.76s |
| `internal/signing` | 0.208s | 0.209s | 0.212s | ~0.21s |
| `internal/hooks` | 0.040s | 0.039s | 0.040s | ~0.04s |
| **Total** | **1.973s** | **1.953s** | **1.961s** | **~2.0s** |

Baseline independently reproduced, not taken on trust: reverted the five test files to `origin/main`
in place and re-ran the same four packages — gitexec 106.040s, worktreeclean 41.206s, signing 17.318s,
hooks 0.081s, **total 164.6s** against the report's 165.4s. Files restored, tree clean. The claimed
~163s saving is accurate.

Side effect worth recording: `internal/cli` now runs in 23.7s. `ci.yml:112-119` still documents that
package at 651s and warns it exceeds Go's 600s default — that comment is now stale by ~27x.

## 5. internal/signing apply-resign coverage claim — independently verified, claim is correct

Proved by coverage profile rather than by reading. `go test -coverprofile -coverpkg=./internal/signing
./internal/signing/...` gives 60.2% and **count=0 for every resign block**: `163.3,164.17` (dry-run
`Resign`), `169-174` (`ActionWouldResign`), `177.3,178.17` (apply `Resign`), `183.3,187.157`
(`ActionResigned`). `internal/signing`'s own suite provably never reaches either branch, and isolating
the package does not change that — it provisions no signing key by design.

The branch is genuinely covered from `internal/cli`'s merge-verb suite:
`internal/cli/merge_test.go:268 TestMerge_UnsignedSource_IsResignedBeforeLanding` and `:643
TestMerge_OctopusTwoUnsignedSources_BothResignedThenMerged` both pass (not skip) and assert
`signing_gate` `action == "resigned"`, a non-nil `backup_ref`, a moved ref, and a successful
`git verify-commit HEAD`. `:292 TestMerge_DryRun_...` covers `would_resign`.

**Trap for anyone re-checking this later:** those tests drive the built binary as a **subprocess**
(`runCLI` → `buildCLI`), so `go test ./internal/cli -coverpkg=./internal/signing` reports **0.0%** for
`internal/signing` even while the branch executes. A coverage profile alone would wrongly read the
apply branch as dead code. Verification has to be behavioral, as above.

`signingRepo` (`merge_test.go:38`) → `initRepo` (`integration_test.go:121`) is itself isolated, so the
signing fixture is hermetic too.

## 6. Branch is not missing anything

All required ancestry present in `HEAD` after rebase: `3d30a2d` + `37086eb` (go/githooks v0.6.0),
`37ad828` + `e53c302` + `72e5732` (earlier isolation change, its review fix, and v1.2.0),
`e983e11` + `9dfc968` (errcheck lint fix). Rebase applied cleanly, no conflicts.

## Findings

**Blocking:** none.

**Major:** none.

**Minor (no edit made — reporting only):**

1. `.task-reports/isolate-remaining-test-fixtures-report.md:17-20` — describes `internal/commitmsg`,
   `worktree-gate/detect`, `worktree-gate/lifecycle` as "out of task scope and were left untouched"
   without noting all three are **already isolated**. Accurate as literally written, but it reads as
   residual exposure and could spawn a redundant follow-up task. Corrected in section 1 above rather
   than by editing the implementer's committed report.

**Non-findings, checked and dismissed** (recorded so they are not re-raised):

- `internal/gitexec/gitignore_test.go:137 TestIsIgnoredByCommittedGitignore_NotARepositoryIsAnError`
  is the one repo-adjacent test left un-isolated. Correct: it deliberately creates no repo, and no
  global/system git config can turn a fresh `t.TempDir()` into one — the not-a-repository error fires
  before `core.excludesfile` or `core.hooksPath` could matter.
- `t.Setenv` in a `t.Helper()` makes any future `t.Parallel()` in these packages panic. No
  `t.Parallel()` exists anywhere in the four packages, and this trap is identical to the
  already-merged `internal/cli` / `internal/commitmsg` / `worktree-gate` pattern, so it is a
  convention-wide property, not a defect in this change.
- The `internal/hooks` comment (`hooks_test.go:13-15`) claims isolation there is consistency rather
  than correctness. Verified honest: `initScratchRepo` only runs `git init`, and `hooks.Install`
  (`hooks.go:99`) writes `core.hooksPath` to the **local** repo config and never reads an existing
  value, so no host global setting can alter the outcome.
- Comment idiom matches the established convention in `internal/cli/worktree_test.go:94-96`,
  `integration_test.go:123-127`, `worktree-gate/lifecycle/helpers_test.go:70-72`. Comment density is
  in line with the surrounding files, with no cross-language or archaeology cruft.

## Fixes applied

None. The change was accepted as authored.

## Re-verification

Not applicable to a fix (there was none), but every gate in section 4 was run fresh **after** the
rebase onto `9dfc968`, including the full-repo suite and the CI merge-signing gate.

## Test-suite assessment

**Adequate for this change.** The change is test infrastructure with no new observable behavior, so
the correct evidence is the existing suites staying green with unchanged assertions plus a reproduced
timing delta — both confirmed. No new test is warranted. A test asserting `GIT_CONFIG_GLOBAL ==
os.DevNull` would only test the harness.

**Pre-existing gap, out of scope for this task:** `internal/signing`'s own suite sits at 60.2%
statement coverage with `signing.go`'s resign apply/dry-run branches (163-187), `WillMintCommit`
(202) and `isAncestor` (269) all at 0% in-package. They are covered only end-to-end through
`internal/cli`'s subprocess tests. Real coverage is higher than any profile will show.

## Residual risk

Effectively none. No production file touched, so shipped binary behavior is unchanged. The only
behavioral surface is test hermeticity, which strictly improved: these suites no longer read the
host's global or system git config. A latent trap is that a future `t.Parallel()` added to any of
these packages will panic at `t.Setenv` — loud and immediate at test time, not a silent failure.

## Plan feedback

1. **The isolation thread is complete.** By capability enumeration, every repo-creating test site in
   the repo is now isolated. Do not open a follow-up for `internal/commitmsg` or `worktree-gate` —
   both are already done. Recommend closing the thread rather than carrying it forward.
2. **`ci.yml:111-119` is stale.** It documents `internal/cli` at 651s and warns about Go's 600s
   per-package default. The package now runs in 23.7s because of this and the prior isolation change.
   The `-timeout 20m` budget and the FB18 note about unbounded growth should be revisited in a
   separate task — not folded in here, since this branch must stay test-only.
3. **Document the subprocess-coverage caveat** before any coverage gate is added to CI. A naive
   `-coverpkg` threshold would misread `internal/signing`'s resign branches as uncovered and could
   push someone to delete or rewrite genuinely-tested code.
4. **No release tag needed**, as scoped: pure test infrastructure, no shipped-binary change. Landing
   on `main` for the next release to absorb is correct.
