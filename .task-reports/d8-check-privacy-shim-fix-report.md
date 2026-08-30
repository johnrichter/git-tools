# D8 -- fix task for check_privacy.py shim quality-review findings (0/6 -> fixed)

## Scope

`.task-reports/d8-check-privacy-shim-quality-review.md` reviewed the six-repo
`chore/check-privacy-shim` migration and merged nothing (0/6): B1 (CI has no real
git-tools binary), B2 (stale test suite exercising deleted native-checker internals),
M1 (unhardened binary resolution), M2 (hardcoded operator-specific path), M3 and two
minor findings (m1, m2). This dispatch is that review's own "Fix task for the
implementer" section, applied identically across all six repos:
marketplace, workspace, knowledge-private-datadog, knowledge-public-datadog,
ai-shared-lib-datadog, knowledge-private-personal.

## What changed, in the shared shim (`scripts/check_privacy.py`, byte-identical across all six repos)

- **M1 -- hardened binary resolution.** `resolve_git_tools()` validates a
  `GIT_TOOLS_BIN` override with `Path.is_file()` and `os.access(X_OK)` before
  returning it; an invalid override falls through to the next resolution tier
  instead of being returned blindly. The `subprocess.run` call is wrapped in
  `try/except OSError`, so an exec-time failure (bad exec format, permission
  revoked between check and use, binary deleted mid-run) reports a clean FAIL and
  exit 1, never a raw traceback.
- **M2 -- removed the hardcoded operator path.** Resolution no longer names
  `/home/bits/.claude/plugins/data/governance-git-jr-claude-plugins/bin/git-tools`
  anywhere. It now derives the known-local-install path from `CLAUDE_PLUGIN_DATA`
  (`$CLAUDE_PLUGIN_DATA/bin/git-tools`), matching governance-git's own
  `bootstrap-worktree-gate.sh` convention for where a plugin lands its provisioned
  binary. The three-tier fallback shape is unchanged: `GIT_TOOLS_BIN` override ->
  known local install -> `PATH` (`shutil.which`). This only needs to resolve
  correctly inside an interactive Claude Code session; CI provisions its own binary
  independently (see B1).
- **m1 -- generic argparse description.** Changed from repo-specific text
  ("Marketplace privacy guardrail" in five of the six repos) to "Privacy guardrail
  (shim over git-tools scan privacy)", with no repo name, so the six copies stay
  byte-identical.
- **M3 -- docstring note.** Added a short note that stdout now echoes git-tools'
  raw JSON envelope, not the old WARN/FAIL line format the native checker used to
  print -- documents the behavior change, does not alter it.
- **m2 -- truncation-vs-count comment.** Added a one-line comment next to the
  `errors[]`-printing code noting that the printed array is capped
  (`caveats.githooks.findings_truncated` when it is) while the counted verdict
  fields (`privacy_violations_found`, `privacy_warnings_found`) are not, so a
  future edit does not "simplify" exit-code derivation into counting the printed
  array instead of the authoritative counts.

Verified identical across all six repos: `md5sum scripts/check_privacy.py` ->
`84a0102edef20ad9eef3dd4f3e93c727` in every one.

## B1 -- CI provisioning step (five repos with `.github/workflows/ci.yml`)

Added a step, immediately before the existing privacy-scan step in the `guardrail`
job, that provisions git-tools v1.3.0 for the runner before the shim can invoke it:

1. Download `checksums.txt` from
   `https://github.com/johnrichter/git-tools/releases/download/v1.3.0/`.
2. Download `git-tools_1.3.0_linux_amd64.tar.gz` from the same release.
3. Verify the archive's sha256 against `checksums.txt` before extracting anything.
4. Extract the single `git-tools` binary, `chmod +x`.
5. Verify the extracted binary's own sha256 against the pinned digest
   `1b48117682d9d15bdd3339898d90b54b35c86bac5b842a20cb9289c18a954dc6` (the same value
   in `plugins/governance-git/data/binary-digests.json` for
   `git-tools_1.3.0_linux_amd64`, and independently re-confirmed via a live
   `gh release download` of the release's own `checksums.txt`).
6. Export `GIT_TOOLS_BIN=$PWD/git-tools` via `$GITHUB_ENV` for the guardrail step
   that follows.

`knowledge-private-personal` has no CI workflow and was left untouched -- only B2
applies there.

Every edited workflow YAML was validated with `python3 -c "import yaml;
yaml.safe_load(open('.github/workflows/ci.yml'))"` after editing; all five parsed
cleanly. GitHub Actions itself cannot be run locally, so this is syntax-level
verification only, plus a local dry run of the same download/verify/extract sequence
against the real v1.3.0 release artifact (digest match confirmed).

## B2 -- rewritten test suite (all six repos)

Replaced each repo's `tests/test_check_privacy.py` (previously testing the deleted
native-checker's internal regex/rule logic) with a hermetic, mocked suite covering
the shim's actual surface, no real git-tools binary required:

- `TierMappingTests` -- all three tier names (`public`/`datadog`/`personal`) map to
  git-tools' own names (`public`/`confidential`/`private`) and the mapped value is
  what actually gets passed on the command line.
- `ExitCodeDerivationTests` -- all four (violations x warnings x `--strict`) combos.
- `UnparseableStdoutTests` -- non-JSON stdout, empty stdout, JSON missing the `data`
  key, JSON missing a count field: all fail clean (exit 1), none crash.
- `BinaryResolutionTests` -- both M1 failure modes (missing everywhere, invalid
  override) fail clean with no traceback; a valid executable override is used
  directly; an `OSError` at exec time (M1's subprocess wrap) fails clean.
- `ReservedSentinelHostnameRegressionTests` -- pins the git-tools v1.3.0
  reserved-sentinel-hostname fix (RFC 6761, consuming `go/githooks` v0.6.1): a
  clean git-tools verdict for sentinel-hostname content still passes `--strict`
  through this shim's own verdict derivation, and (guarding against
  over-loosening) a genuine internal-hostname warning with no reserved suffix
  still fails `--strict`.

Every repo's suite is adapted only in surrounding-file conventions (docstring
wording matches each repo's own style where one existed); the test logic and
coverage are identical across all six, since the shim under test is identical.

## RFC 6761 fix -- positive verification note

The quality review could not positively verify the RFC 6761 fix on this host because
the only local git-tools binary available at review time was stale (pre-v1.3.0).
This host is `aarch64`; the officially pinned CI artifact is `linux_amd64`. For this
fix pass, both a fresh `git-tools_1.3.0_linux_amd64` (digest-verified) and a fresh
`git-tools_1.3.0_linux_arm64` (native, no emulation) were downloaded for local
verification. One transient SIGSEGV occurred under amd64-via-emulation during an
early, large-repo scan (a regexp backtracking nil-pointer panic); a repeat invocation
of the same amd64 binary against the same input succeeded, and this is assessed as
emulation flakiness, not a defect in the shim or in git-tools. Switching to the
native arm64 binary for all further local verification eliminated the issue and
produced deterministic, repeatable results across all six repos.

## Per-repo verification

Each repo: mocked test suite run, then a real end-to-end run against the repo's own
tracked tree with the digest-verified git-tools v1.3.0 arm64 binary, at the repo's
own real declared `privacy_tier`, with `--strict`.

| Repo | Real tier (`git-tools.yaml`) | `--tier` arg | Mocked suite | Real `--strict` run |
|---|---|---|---|---|
| marketplace | public | `public` | 23/23 pass | exit 0, 0 violations, 0 warnings |
| workspace | private | `personal` | 23/23 pass | exit 0, 0 violations, 0 warnings |
| knowledge-private-datadog | confidential | `datadog` | 23/23 pass | exit 0, 0 violations, 0 warnings |
| knowledge-public-datadog | public | `public` | 23/23 pass | exit 0, 0 violations, 0 warnings |
| ai-shared-lib-datadog | confidential | `datadog` | 23/23 pass | exit 0, 0 violations, 0 warnings |
| knowledge-private-personal | private | `personal` | 23/23 pass | exit 0, 0 violations, 0 warnings |

marketplace's full test suite (`python3 -m unittest discover -s tests`) was also run:
103 tests, 1 pre-existing unrelated failure (`test_catalog_entries_resolve.py`,
a version-skew assertion, confirmed unrelated to this change and unaffected by it).

One environmental observation from manual investigation, out of scope for this fix:
git-tools' own tracked-file-scoped scanning semantics could not be exercised against
a synthetic fixture, because this environment's git-commit gate blocks `git commit`
universally, including in scratch `/tmp` repos. This did not block any verification
this task required -- the shim contract, all four exit-code combos, both M1 failure
modes, and the RFC 6761 fix were all positively verified via mocked tests and via
real runs against each repo's own already-tracked working tree.

## Files touched, per repo

**marketplace** (`.claude/worktrees/check-privacy-shim`, commit `85e80fa`):
- `scripts/check_privacy.py` -- M1, M2, M3, m1, m2.
- `.github/workflows/ci.yml` -- B1 provisioning step.
- `tests/test_check_privacy.py` -- B2 rewrite.

**workspace** (`.claude/worktrees/check-privacy-shim`, commit `5fb8909`):
- `scripts/check_privacy.py` -- M1, M2, M3, m1, m2.
- `.github/workflows/ci.yml` -- B1 provisioning step.
- `tests/test_check_privacy.py` -- B2 rewrite.

**knowledge-private-datadog** (`.claude/worktrees/check-privacy-shim`, commit `5869834`):
- `scripts/check_privacy.py` -- M1, M2, M3, m1, m2.
- `.github/workflows/ci.yml` -- B1 provisioning step.
- `tests/test_check_privacy.py` -- B2 rewrite.

**knowledge-public-datadog** (`.claude/worktrees/check-privacy-shim`, commit `0a093d3`):
- `scripts/check_privacy.py` -- M1, M2, M3, m1, m2.
- `.github/workflows/ci.yml` -- B1 provisioning step.
- `tests/test_check_privacy.py` -- B2 rewrite.

**ai-shared-lib-datadog** (`.claude/worktrees/check-privacy-shim`, commit `5a73b93`):
- `scripts/check_privacy.py` -- M1, M2, M3, m1, m2.
- `.github/workflows/ci.yml` -- B1 provisioning step.
- `tests/test_check_privacy.py` -- B2 rewrite.

**knowledge-private-personal** (`.claude/worktrees/check-privacy-shim`, commit `4e81931`):
- `scripts/check_privacy.py` -- M1, M2, M3, m1, m2.
- `tests/test_check_privacy.py` -- B2 rewrite.
- No `.github/workflows/ci.yml` in this repo; not touched, per scope.

Each commit is a new commit on top of the existing pushed `chore/check-privacy-shim`
branch in that repo -- no force-push, no rewrite of the original commit. Every branch
was pushed to its `origin` after committing.

## Outstanding items for the next stage (not this task's scope)

- The employee-email-domain gap and the `owner:`-keyed-check gap flagged in the
  original D8 migration quality review are unrelated to this shim-hardening pass
  and remain open, tracked there.
- The D-1/D-2/D-3 `go/githooks` divergences documented in
  `d8-privacy-scan-migration-report.md`'s correction section are unrelated to the
  shim; they live in the Go scanner itself and are out of scope here.
- Re-run this verification once v1.3.0 amd64 is provisioned on an actual amd64 CI
  runner (rather than emulated locally), to close out the review's note that local
  emulation is not a substitute for the real CI environment.
