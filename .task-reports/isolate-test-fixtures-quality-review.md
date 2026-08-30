# Quality review: isolate test fixtures from host git hooks/config

## Verdict: FIX-APPLIED (accepted after four fixes, re-verified)

The isolation mechanism is correct, consistently applied, and idiomatic. `t.Setenv` reaches
every git invocation in the touched packages, the signing-key fix is genuine and minimal,
and `/dev/null` matches this repo's own OS-support conventions. Four defects were found and
fixed in this review, one of which (a lost coverage path) was proven by mutation probe rather
than asserted.

## 1. Mechanism review: correct, and the reach claim holds

**Env reach verified from the production side, not just the test side.** The reports assert
that `t.Setenv` propagates through `runCLI`'s subprocess. That only holds if nothing in the
production path rebuilds the child environment. Verified directly:

```
grep -rn 'cmd\.Env|\.Env = |Env:' --include='*.go' . | grep -v '_test.go'   -> no matches
```

No production code sets `cmd.Env`, so every `git` subprocess -- fixture-side (`runGit`,
`cgit`, `runGitT`, `runGitCmd`) and CLI-side (the built `git-tools` binary's own git calls) --
inherits the test process's environment. The mechanism is sound end-to-end.

**`t.Setenv` is safe here.** `t.Setenv` panics if the test has called `t.Parallel()`.
Repo-wide search: `grep -rn 't\.Parallel()' --include='*.go' .` -> zero matches. No parallel
test exists, so process-level env mutation cannot race. This is a real constraint the change
now depends on; it holds today and is worth stating so a future `t.Parallel()` addition is
recognized as a conflict rather than a mystery panic.

**`os.DevNull` over a hardcoded literal** is the right call and is what was used at all six
new sites -- portable by construction, no OS assumption baked into the source.

## 2. Missed fixture-creating helper: found one, in scope, fixed

Exhaustive repo-wide enumeration of test files that invoke real git
(`grep -rln 'exec.Command("git"' --include='*_test.go' .`), cross-checked against
`GIT_CONFIG_GLOBAL` presence per file:

| test file | creates own repo | isolated |
|---|---|---|
| `internal/cli/integration_test.go` | yes (`initRepo`) | yes (this change) |
| `internal/cli/worktree_test.go` | yes (`cleanupFixture`) | yes (this change) |
| `internal/cli/led118_verification_test.go` | yes | yes (pre-existing) |
| `internal/cli/merge_commit_message_hook_test.go` | yes | yes (pre-existing) |
| `internal/cli/config_test.go` | yes (`gitRepoForConfigTest`) | **NO -- MISSED, now fixed** |
| `internal/cli/branch_test.go`, `push_test.go`, `tag_test.go` | no (use `initRepo`/`signingRepo`) | transitively yes |
| `internal/commitmsg/commitmsg_test.go` | yes | yes (pre-existing) |
| `worktree-gate/lifecycle/helpers_test.go` | yes (`newScratchRepo`) | yes (this change) |
| `worktree-gate/detect/decide_sc15_...sdet_test.go` | yes (inline) | yes (this change) |
| `internal/gitexec/gitexec_test.go`, `gitignore_test.go`, `probe_adversarial_test.go` | yes | no -- out of scope |
| `internal/hooks/hooks_test.go` | yes | no -- out of scope |
| `internal/signing/signing_test.go` | yes | no -- out of scope |
| `internal/worktreeclean/worktreeclean_test.go` | yes | no -- out of scope |

`worktree-gate` is complete: only two files in the whole subtree touch real git, and both are
isolated.

`internal/cli` was **not** complete. `gitRepoForConfigTest`
(`internal/cli/config_test.go:16`) creates its own scratch repo with its own inline `run`
closure -- it never goes through `initRepo`, so neither the implementer's nor the
test-engineer's grep (both keyed on the named helpers) surfaced it. It is inside the task's
declared scope (`internal/cli`), so it is a genuine miss, not a scope question.

Not cosmetic: `internal/cli` dropped a further **39.5s -> 23.1s** once fixed, because four
fixtures each ran the host hook on their commit. It also had a second latent host dependency
the other fixtures had already guarded against -- it never set `core.excludesfile ""`, so a
host global gitignore rule could hide the untracked config file that
`TestLoadConfig_WarnsOnUntrackedConfig` and `TestLoadConfig_WarnsOnLocallyModifiedConfig`
plant on purpose. Nulling global config closes both at once.

## 3. Signing-key fix: genuinely correct and minimal

`configureSigningKey` (`internal/cli/merge_test.go`) is a clean extraction of `signingRepo`'s
key setup, and the split is drawn at exactly the right line: key material without
`commit.gpgsign`. That is the minimum needed by `sign`/`resign`, whose entire contract is
turning an unsigned commit into a signed one -- a fixture that pre-signed its commits would
make those two tests assert nothing. Behavior of `signingRepo` is preserved; the only ordering
change is that the `ssh-keygen` `LookPath` probe now runs after `initRepo` instead of before,
which is immaterial (both paths `t.Fatalf`).

The `initRepo` -> `signingRepo` switches in `tag_test.go` / `scan_gate_test.go` are slightly
broader than strictly minimal -- `signingRepo` also turns on `commit.gpgsign`, so those
fixtures' base commits are now signed where before they were not. Verified inert rather than
assumed: `tag create` verifies the **tag** it mints (`internal/cli/tag.go:189`,
`git tag -v <tagName>`) and never inspects HEAD's commit signature. No finding; noted only so
the scenario drift is on record.

## 4. Portability finding: `/dev/null` is consistent with this repo's own OS support

**Checked, not assumed.** `git-tools` does not claim Windows support anywhere:

- `.github/workflows/release.yml:128` builds exactly `linux/amd64 linux/arm64 darwin/amd64
  darwin/arm64`. No `windows/*` cell.
- Production code already assumes POSIX shell: `internal/hooks/hooks.go:47` quotes hook
  wrappers as "one POSIX sh word".
- No `windows` reference in `README.md`, any workflow, or any source file.

So the POSIX-like `/dev/null` assumption is **not** a finding -- it matches the repo's
declared platform set. It is also weaker than it looks: all six new sites use `os.DevNull`,
which compiles to `NUL` on Windows, so the change would not be the thing that broke a
hypothetical Windows port.

Minor, pre-existing, not fixed (untouched by this change): `internal/cli/led118_verification_test.go:134`
is the one place in the repo that hardcodes the string literal `"/dev/null"` instead of
`os.DevNull`, inside an explicit `append(os.Environ(), ...)` env slice. Consistency nit only;
it predates this branch.

Git version floor: `GIT_CONFIG_GLOBAL` / `GIT_CONFIG_SYSTEM` need git >= 2.32 (2021-06). The
repo declares no minimum git version anywhere, and three pre-existing test files already
relied on these variables, so this change introduces no new floor. Host git is 2.55.0.

## 5. Findings

### Blocking
None.

### Major

**M1. `worktree-gate/lifecycle/complete_test.go` -- the switch to `signableScratchRepo`
silently dropped a covered code path.** Four `TestComplete_*` tests moved from
`newScratchRepo` (commits unsigned) to `signableScratchRepo` (`commit.gpgsign=true`, commits
pre-signed). But `signing.Gate` skips a fully-signed range
(`internal/signing/signing.go:163-187`), so the gate's apply-resign branch that these tests
previously drove is no longer reached at all from `lifecycle`.

Proven by mutation probe, not inferred. A `panic()` was temporarily inserted ahead of
`internal/signing/signing.go:177` (`applied, err := repo.Resign(...)`) and the package run
three ways:

| tree | probe hits in `worktree-gate/lifecycle` |
|---|---|
| pre-change tests (`main`'s `complete_test.go` + `helpers_test.go`) | 1 -- path was covered |
| as submitted on this branch | 0 -- **coverage lost** |
| after fix F4 below | 1 -- coverage restored |

The underlying gate/resign machinery is still covered elsewhere: the same probe run against
the whole repo failed 5 `internal/cli` merge tests (`TestMerge_UnsignedSource_IsResignedBeforeLanding`
and four octopus/conflict cases). So the machinery was never at risk -- what was lost was
`Complete`'s *own* call into it with an unsigned source, which is precisely the SC-B1 behavior
`complete.go:51-61` documents. Fixed.

The comments shipped with those four tests also stated the wrong reason: "Complete's merge
signs unsigned source commits before landing them, so this fixture needs a signing key" --
true of the old fixture, false of the new one, whose commits arrive already signed. With
pre-signed sources the real reason a key is needed is that `WillMintCommit` -> `prober.Available`
must succeed to sign the merge commit `Complete` mints (`complete.go:128-144`). Comments
corrected to say that.

### Minor

**m1. `internal/cli/config_test.go:16` -- fixture-creating helper missed by the change.**
See section 2. Fixed.

**m2. `worktree-gate/detect/decide_sc15_branchdelete_ordering_sdet_test.go` -- isolation was
set before the `go build` of the repo under test.** `go build` stamps VCS info by running git
against the real checkout; verified, not assumed: `go version -m` on the built binary reports
`vcs=git`, `vcs.revision`, `vcs.modified`. Nulling global config for that build removes any
global-only allowance the build's git calls may need -- canonically `safe.directory`, which
git refuses to honor from repo-local config, so global is the only place it can live. This
host has no `safe.directory` entries (`git config --global --get-all safe.directory` -> exit
1), so the failure mode is **not reproducible here and is not asserted as a current break**;
it is a latent, environment-specific fragility, most plausible in a container or CI checkout
owned by a different uid. Every other site in the change already has the build-then-isolate
ordering (`internal/cli` tests call `buildCLI(t)` before `initRepo(t)`), so this was also the
one inconsistency with the change's own dominant pattern. Fixed by moving the two lines below
the build, which additionally makes the intent explicit: the isolation is for the fixture, not
for the toolchain.

**m3. `internal/cli/integration_test.go` -- `initRepo`'s doc comment went stale.** It read "A
test of the merge verb uses signingRepo instead", which this change made doubly wrong:
`signingRepo` now backs nine tag/scan-gate tests, and a second fixture (`configureSigningKey`)
now layers over `initRepo` as well. Rewritten to name both layering helpers and the reason
each exists. Fixed.

**m4. `internal/cli/led118_verification_test.go:134` -- hardcodes `"/dev/null"` where every
other site uses `os.DevNull`.** Pre-existing, untouched by this branch, left alone as
out of scope. Noted for a future sweep.

## 6. Fixes applied

| # | file | change |
|---|---|---|
| F1 | `internal/cli/config_test.go` | isolate `gitRepoForConfigTest` (m1) |
| F2 | `worktree-gate/detect/decide_sc15_branchdelete_ordering_sdet_test.go` | move isolation below the `go build` (m2) |
| F3 | `internal/cli/integration_test.go` | refresh `initRepo`'s stale doc comment (m3) |
| F4 | `worktree-gate/lifecycle/helpers_test.go`, `complete_test.go` | restore lost resign coverage (M1) |

F4 detail: extracted `configureSigningKeyT` out of `signableScratchRepo` in
`helpers_test.go` -- key material without `commit.gpgsign` -- exactly mirroring the
`signingRepo` / `configureSigningKey` split this change introduced in `internal/cli`.
`signableScratchRepo` is now `newScratchRepo` + `configureSigningKeyT` + `commit.gpgsign=true`,
behavior unchanged. `TestComplete_MergesBackAndCleansUp` switched to
`newScratchRepo` + `configureSigningKeyT`, restoring the unsigned-source scenario with a local
ephemeral key instead of the host's. The other three tests stay on `signableScratchRepo` with
corrected comments -- their assertions are about dirty refusal, conflict, and branch
retention, where source signature state is incidental.

No production file was modified. `internal/signing/signing.go` was mutated twice as a probe
and restored byte-identically both times (`git diff HEAD -- internal/signing/` -> empty).

## 7. Re-verification (fresh, after all fixes)

```
gofmt -l internal/ worktree-gate/   -> no output
go build ./...                      -> clean
go vet ./...                        -> clean
go test ./... -count=1              -> all 10 test packages ok, 0 failures, 1:46.64 wall
```

Per-package: `internal/cli` 23.100s, `internal/gitexec` 106.159s, `internal/worktreeclean`
41.349s, `internal/signing` 17.370s, `worktree-gate/lifecycle` 4.029s, `worktree-gate/detect`
0.656s, rest sub-second.

Stability: `internal/cli` measured 23.162s / 23.163s / 23.100s across three independent fresh
runs. `worktree-gate/lifecycle` 3.966s / 4.065s / 4.208s / 4.029s. No flake.

`internal/cli` net effect of this branch plus F1: ~18-20 min historical baseline -> **23.1s**.

## 8. Test-suite assessment

Adequate, with the M1 gap now closed. The change is fixture infrastructure, so the existing
suites are the correct instrument and no new test was warranted -- with one exception the
test-engineer did not catch: an infrastructure change that alters *fixture state* (unsigned ->
signed commits) can silently retire a code path while every test stays green. Green-suite
evidence cannot see that; a mutation probe can. Recommend the mutation-probe technique
whenever a fixture's observable state changes, not only when tests fail.

Gap the test-engineer should close in future work: both reports enumerated fixture helpers by
name (`initRepo`, `newScratchRepo`, ...) rather than by capability ("creates a git repo"). That
is what let `gitRepoForConfigTest` through -- it creates a repo with an inline closure and
matches no helper name. The capability-shaped query is
`grep -rln 'exec.Command("git"' --include='*_test.go' .`, cross-checked against
`GIT_CONFIG_GLOBAL` per file.

## 9. Residual risk

- **Process-global env**: isolation is process-level, so it depends on the repo having no
  `t.Parallel()` test (verified: zero). Adding one to a fixture-using package would panic in
  `t.Setenv`. Acceptable and idiomatic for Go test fixtures, but it is a real coupling.
- **Signed base commits** in the nine tag/scan-gate tests switched to `signingRepo`. Verified
  inert for `tag create` (verifies the tag, not the commit). Would need re-checking if a
  future verb starts inspecting HEAD's signature.
- **Five packages still unisolated** (section 10). Their tests remain host-dependent and slow;
  correctness is unaffected on this host.
- **This branch is behind `main`** by two commits (`3d30a2d` go/githooks v0.6.0 consumption,
  `37086eb` its review), including a `go.mod`/`go.sum` bump. All verification above ran on
  this branch's tree, not on the merge result. Rebase or merge `main` and re-run before
  landing. Not fixed here: the dispatch forbids merging.

## 10. Plan feedback

**Follow-up task, real and sized.** Five test files outside this task's scope create scratch
git repos with the identical unisolated-global-config gap. Measured package times from the
final run, all on the now-isolated host:

| package | time | fixture |
|---|---|---|
| `internal/gitexec` | 106.2s | `scratchRepo` (`gitexec_test.go:29`), plus two inline inits in `gitignore_test.go:158,334`, plus `probe_adversarial_test.go` |
| `internal/worktreeclean` | 41.3s | `cgit`-based fixture (`worktreeclean_test.go:221`) |
| `internal/signing` | 17.4s | `gitCmd`-based fixture (`signing_test.go:27`) |
| `internal/hooks` | 0.1s | `hooks_test.go:14` -- hygiene only, no time to win |

Those three slow packages are 165s of serial work and now dominate the suite: `internal/cli`,
the package this task fixed, is down to 23.1s. The same two-line pattern applies to each. Two
cautions carried forward from this review: (a) `internal/signing`'s own tests do **not**
currently reach `Gate`'s apply-resign branch (confirmed `ok` under the mutation probe), so
that package's fixtures may have latent host-key dependencies of their own once isolated --
budget for the same class of surfaced dependency the implementer hit here; (b) enumerate
fixtures by capability, not by helper name.

**Process note for the orchestrator.** Two prior roles both verified this change against a
name-keyed list of fixture helpers and both missed the same call site. When a task's
acceptance is "every fixture of kind X is treated", the enumeration query belongs in the task
statement itself, expressed by capability, so implementer and verifier cannot inherit the same
blind spot.
