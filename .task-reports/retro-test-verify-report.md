# Retroactive test-engineer verification: git-tools main / v1.3.0

Deliverable type: code. Verification form: adversarial probing against a
guard test, a built CLI, a differential release-binary check, and a full
regression run. All commands below were executed fresh in this session, not
inferred from prior reports.

## 1. D4 single-call-site lint

File: `internal/cli/tag_call_site_test.go`, test
`TestRawGitTagCallSite_ConfinedToTagGo`.

Guard design read first: it declares three real git-spawn shapes in
`gitSpawnShapes` --
`gitexec.RunGit(` (variadic tail), `runGit(` (variadic tail, worktree-gate's
private runner), `sysops.Run(` (git words as a `[]string{...}` literal
argument) -- and asserts, via `callsPerShape`, that each shape actually has at
least one call site in the tree (so a renamed/retired shape fails loudly
instead of silently checking nothing). This addresses the exact prior gap
named in the dispatch: an earlier version of this guard checked only one
shape.

Baseline: `go test ./internal/cli/ -run TestRawGitTagCallSite -v` -> PASS.

Adversarial probe, one per shape, each inserted as a call mimicking a real
spawn (not just the word "tag" in a string), confirmed to fail with the
correct file named, then reverted and reconfirmed passing:

- Shape `gitexec.RunGit(`: inserted `gitexec.RunGit(ctx, dir, "tag", "-l")`
  into `internal/cli/push.go` (`localRefSHA`). Result:
  `tag_call_site_test.go:109: raw \`git tag\` call site outside tag.go:
  .../internal/cli/push.go` -- FAIL, correct file named. Reverted with
  `git checkout -- internal/cli/push.go`; test PASS again.
- Shape `runGit(`: inserted `runGit(ctx, dir, "tag", "-l")` into
  `worktree-gate/lifecycle/gitutil.go` (`isDirty`). Result: FAIL naming
  `.../worktree-gate/lifecycle/gitutil.go`. Reverted; test PASS again.
- Shape `sysops.Run(` (slice-literal form): inserted
  `sysops.Run(ctx, "git", []string{"tag", "-l"}, sysops.Options{Dir: dir})`
  into `internal/cli/scan.go` (`lfsRouteChecker`). Result: FAIL naming
  `.../internal/cli/scan.go`. Reverted; test PASS again.

All three probes used a real invocation shape (proper call syntax matching
that shape's actual signature), not a bare string containing "tag", so this
also confirms the guard's arg-position parsing (`gitSubcommandWords`,
`sliceLiteralElements`, `splitTopLevelArgs`) correctly extracts the
subcommand word for both the variadic-tail and slice-literal shapes.

Working tree confirmed clean (`git status --porcelain`) after all three
reverts -- no probe code was left behind.

**Verdict: guard genuinely checks all three real spawn shapes and correctly
localizes a violation for each. PASS.**

## 2. D8 native privacy-scan migration

`go.mod`: `github.com/johnrichter/claude-shared-tooling/go/githooks v0.6.1` --
confirmed pinned version matches the dispatch's expectation.

Built fresh: `go build -o /tmp/git-tools-built ./cmd/git-tools` -> exit 0.

Adversarial probes run against the freshly built binary (isolated scratch
git repos under `/tmp`, not the worktree):

| Probe | Command | Result |
|---|---|---|
| Sentinel hostnames (`foo.internal.test`, `bar.internal.example`, `baz.internal.localhost`), tier `public --strict` | `scan privacy` | `exit_code:0`, `privacy_violations_found:0`, `privacy_warnings_found:0` -- no violation, confirming the RFC 6761 sentinel fix |
| Real internal hostname, no sentinel suffix (`jenkins-01.internal`), tier `public --strict` | `scan privacy` | `exit_code:30`, flags `rule: internal_identifier`, `message: "internal identifier — internal hostname"` -- DOES flag |
| AWS example key `` `AKIAIOSFODNN7` + `EXAMPLE` `` + Slack example token `` `xoxb-ab59` + `EXAMPLETOKEN` ``, tier `public --strict` | `scan privacy` and `scan secrets` | both `exit_code:0`, zero findings -- neither flags (exact-match exemptions hold) |
| Real-looking non-exempted secret (`` `AKIAABCD1234` + `EFGH5678` ``) | `scan secrets` (isolated) and `scan privacy` (combined dir) | flags `rule: aws_access_key_id` in both -- DOES flag |
| Tier coverage: `public`, `confidential`, `private`, each with and without `--strict` | `scan privacy --privacy-tier <tier> [--strict]` | all three tier names accepted (no usage error). Severity differs: `public` treats the internal hostname as a warning without `--strict` and promotes it to a blocking error with `--strict`; `confidential`/`private` never flag it at all (0 warnings, 0 violations) — internal hostnames are within-scope for those tiers by design. The AWS key violation is a hard error at every tier regardless of `--strict`. |

Isolated single-file repros (one file per scratch repo) were also run to
remove any doubt about cross-file interaction in the combined-directory
run; each isolated result matched the combined run's per-file finding
exactly (sentinel-only -> success/0 findings; internal-only -> 1 warning,
promoted to error under `--strict`; secret-only -> 1 secret finding).

**Verdict: sentinel-hostname fix holds, real internal hostnames still flag,
existing exemptions hold, a real secret still flags, and all three tier
names produce distinct, correctly-ordered severity behavior. PASS.**

## 3. Full regression suite

All run fresh from the worktree root, working tree confirmed clean
(`git status --porcelain`) before and after:

- `gofmt -l .` -> no output, exit 0. Clean.
- `go build ./...` -> exit 0.
- `go vet ./...` -> exit 0.
- `go test ./... -count=1 -v` -> exit 0, every package `ok`.

Per-package timings from that run:

| Package | Time |
|---|---|
| internal/cli | 24.413s |
| internal/commitmsg | 0.163s |
| internal/gitexec | 1.013s |
| internal/hooks | 0.048s |
| internal/result | 0.005s |
| internal/signing | 0.234s |
| internal/worktreeclean | 0.795s |
| worktree-gate/detect | 0.666s |
| worktree-gate/fixtures | 0.004s |
| worktree-gate/lifecycle | 4.328s |

`internal/cli` at 24.4s is consistent with the expected fast, isolated time
(dispatch's reference point: "around 23s, not 20 minutes"). No package shows
host-hook-dependent slow behavior. No regression found.

**Verdict: full suite green, isolation timings hold. PASS.**

## 4. The v1.3.0 release

- Tag exists: `gh release view v1.3.0 --repo johnrichter/git-tools` ->
  published 2026-08-30T16:26:04Z, non-draft, non-prerelease, 12 assets
  (checksums, contract-digests, per-platform tarballs for `git-tools` and
  `worktree-gate`).
- `git log -1 v1.3.0` -> `b375b8d Consume go/githooks v0.6.1: RFC 6761
  sentinel hostname false positive`. The tag is an ancestor of current
  `main`/HEAD (`git log --oneline v1.3.0..HEAD` shows only the later,
  unreleased D8-shim-migration commits sitting on top); `main` still
  contains the tag (`git branch --contains v1.3.0` includes `main`). So
  v1.3.0 does carry the sentinel-hostname fix as expected, and the D8
  check_privacy.py-shim work on top of it is correctly unreleased/pending.
- Downloaded real published binary:
  `gh release download v1.3.0 --repo johnrichter/git-tools --pattern
  "git-tools_1.3.0_linux_amd64.tar.gz" --pattern "checksums.txt"`.
  `sha256sum -c` against the published `checksums.txt` entry for that
  tarball -> `OK`.
- `go version -m ./git-tools | grep githooks` on the extracted binary ->
  `dep github.com/johnrichter/claude-shared-tooling/go/githooks v0.6.1` --
  confirms the shipped binary's embedded build info matches the source
  tree's pin, not just the tag's source text.
- Ran the sentinel-hostname probe against the actual released binary (not
  a local rebuild): `./git-tools scan privacy --repo <sentinel-only dir>
  --privacy-tier public --strict` -> `exit_code:0`, 0 violations, 0
  warnings. Also ran the real-internal-hostname probe against the same
  released binary -> `exit_code:30`, flags `internal_identifier` as
  expected. Both match the source-tree/local-build behavior from section 2
  exactly.

**Verdict: tag exists, checksum-verified download, embedded build info
confirms the v0.6.1 pin, and the shipped binary reproduces both the fix and
the still-active detection exactly as source does. PASS.**

## Summary

| Item | Verdict |
|---|---|
| D4 single-call-site guard, all 3 spawn shapes | PASS |
| D8 native privacy-scan migration (sentinel fix, exemptions, real flags, tier coverage) | PASS |
| Full regression (`gofmt`/`build`/`vet`/`test`), isolation timing | PASS |
| v1.3.0 release binary matches source | PASS |

No failures found. No flakes observed (guard probes and regression suite
were deterministic across the reverts/reruns performed). Working tree is
clean; no probe code, scratch repos, or build artifacts were left in the
worktree (scratch material lived under `/tmp` and was removed except a few
paths a local write-gate refused to let this session delete directly --
those are outside the repository and do not affect this worktree's state).

**Overall verdict: PASS.**
