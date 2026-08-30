# Retroactive test-engineer pass: `check_privacy.py` shim (6 repos)

Scope: `scripts/check_privacy.py` as merged, current `main` HEAD, in each of
marketplace, workspace, knowledge-private-datadog, knowledge-public-datadog,
ai-shared-lib-datadog, knowledge-private-personal. All verification below was
run fresh in this session, directly against each repo's primary checkout
(read-only) plus disposable `/tmp` scratch dirs — no prior quality-review
report was reused or trusted.

Binary used for every "real binary" step: `git-tools_1.3.0_linux_amd64`,
downloaded fresh via `gh release download v1.3.0 --repo johnrichter/git-tools`
and verified myself against that release's own `binary-checksums.txt` before
first use:
- archive `git-tools_1.3.0_linux_amd64.tar.gz` sha256 `d721d9922e91dcb66c458947ec5f28fd452a708bd7ef05c18f166272f1879d14` — matches `checksums.txt` row.
- extracted binary `git-tools` sha256 `1b48117682d9d15bdd3339898d90b54b35c86bac5b842a20cb9289c18a954dc6` — matches `binary-checksums.txt` row for `git-tools_1.3.0_linux_amd64`.
- This is the same digest all five repos' CI workflows pin (`expected_sha256` in each `ci.yml`).

## 1. Byte-identity across all six `scripts/check_privacy.py`

`md5sum` on all six, same session, same command:
```
113449ebf48b05836e27718997a38e75  marketplace/scripts/check_privacy.py
113449ebf48b05836e27718997a38e75  workspace/scripts/check_privacy.py
113449ebf48b05836e27718997a38e75  knowledge-private-datadog/scripts/check_privacy.py
113449ebf48b05836e27718997a38e75  knowledge-public-datadog/scripts/check_privacy.py
113449ebf48b05836e27718997a38e75  ai-shared-lib-datadog/scripts/check_privacy.py
113449ebf48b05836e27718997a38e75  knowledge-private-personal/scripts/check_privacy.py
```
**PASS** — all six byte-identical.

`tests/test_check_privacy.py` is also byte-identical across all six
(`md5sum` = `5d86a9044aca80bbcdeaad90c0f9782c` everywhere), so items 2's results
below apply uniformly.

## 2. Own rewritten test suite, run fresh, hermetic check

Ran each repo's `tests/test_check_privacy.py` directly (equivalent to
`python3 -m unittest tests.test_check_privacy -v`, the invocation 4 of 5
CI workflows use; `discover -s tests -k check_privacy` could not be used
verbatim here because the test file isn't in an importable package and
`-t`-scoped discovery from outside each repo's own cwd failed to import —
direct-path execution reaches the identical 26 test cases via the file's own
`unittest.main()`).

Result, all six repos, same run: **26 tests, 0 failures, 0 errors** (`OK`).

Hermeticity check: re-ran all six with `PATH=/usr/bin:/bin`, `GIT_TOOLS_BIN`
unset, and `HOME`/environment fully cleared (`env -i`), confirming first that
this strips every resolution path (`which git-tools` under that PATH: not
found). All six still produced **26/26 OK** under this restricted
environment — the suite mocks `subprocess.run`/`resolve_git_tools` throughout
and never shells out to a real binary. **PASS**, all six, both plain and
hermetic-restricted runs.

## 3. Shim end-to-end against each repo's real tree, real tier, `--strict`

Tier mapping used (from each repo's own `git-tools.yaml` `privacy_tier`,
translated through the shim's own `TIER_MAP` public↔public,
confidential↔datadog, private↔personal):

| repo | git-tools.yaml tier | shim `--tier` | result |
|---|---|---|---|
| marketplace | public | public | **exit 1** — 0 violations, 3 warnings, escalated by `--strict` |
| workspace | private | personal | **flaky crash in the real git-tools binary itself** — see below |
| knowledge-private-datadog | confidential | datadog | exit 0, clean |
| knowledge-public-datadog | public | public | exit 0, clean |
| ai-shared-lib-datadog | confidential | datadog | exit 0, clean |
| knowledge-private-personal | private | personal | exit 0, clean |

Four of six: clean exit 0, JSON envelope well-formed, `privacy_violations_found`
and `privacy_warnings_found` both 0. **PASS**.

**marketplace (exit 1, not a shim defect):** the 3 warnings are real content —
`plugins/governance-git/CHANGELOG.md`, `plugins/governance-git/README.md`,
and `.task-reports/governance-git-repin-v1.3.0-quality-review.md` all discuss
the RFC 6761 sentinel-hostname fix using a *backtick-quoted* example
(`` `foo[.]internal.test` ``). Per `plugins/governance-git/CHANGELOG.md`'s own
0.17.0 entry, git-tools' RFC 6761 fix only recognizes certain delimiters as
end-of-host, and "a backtick is not one of those delimiters, so the same
hostname written in markdown inline code is still flagged" — a residual gap
git-tools' own release notes already document. The shim's verdict derivation
is correct here (0 violations + 3 warnings + `--strict` → exit 1, matching
`ExitCodeDerivationTests` exactly). **Operational finding, not a shim bug:**
marketplace's own CI guardrail step (`Privacy scan (public tier, authoritative)`,
`--strict`) would fail today against a real, digest-verified git-tools v1.3.0
binary, because of this known-but-unfixed backtick residual. Flagging for the
repo owner / next git-tools repin, since it sits outside this shim's contract.

**workspace: real, reproducible-but-flaky SIGSEGV in the git-tools binary
itself.** Running the exact same digest-verified binary (sha256
`1b48117682d9d15bdd3339898d90b54b35c86bac5b842a20cb9289c18a954dc6`) against
the exact same command (`git-tools scan privacy --repo <workspace> --privacy-tier
private[, --strict]`) is non-deterministic:

- 11 total runs observed (mix of direct binary invocation and via the shim,
  with and without `--strict`, with an explicit `GIT_TOOLS_BIN` and with the
  ambient-PATH binary): **7 crashed, 4 completed cleanly** — roughly 60-70%
  flake rate on this one repo's tree.
- Crash signature (two variants seen): `fatal error: unexpected signal during
  runtime execution` / `SIGSEGV` inside `sync.(*poolChain).popTail` reached
  from `regexp.newBitState` / `regexp.(*Regexp).backtrack`, called from
  `githooks.matchesSecretPattern` inside `githooks.ScanPrivacy`'s file-walk
  callback (`go/githooks@v0.6.1/secrets.go:85`, `privacy.go:308`); and a
  second variant, `fatal error: fault`, `unexpected fault address`, same
  region. Both point at memory corruption in the Go runtime's regexp
  backtracking bitstate pool while it scans workspace's tree content —
  consistent with a data race (shared `*regexp.Regexp`/pool state reused
  unsafely across the scanner's file-walk), not a deterministic bad-input
  crash: an isolated copy of the exact same tree with `.git/` stripped out
  scanned clean every time, and re-adding `.git/` back reproduces flakiness
  again, but not deterministically — same binary, same tree, different
  outcome run to run.
- Not reproduced on any of the other five repos (10+ combined runs across
  marketplace, knowledge-private-datadog, knowledge-public-datadog,
  ai-shared-lib-datadog, knowledge-private-personal — 0 crashes). workspace is
  the largest of the six trees (400 tracked files) and the only one that
  reliably widens the race window enough to trigger this in-session.
- **Shim's own degradation on this crash is correct and matches its
  contract:** when git-tools crashes, its stdout is a Go panic dump, not
  JSON. The shim's `json.loads(result.stdout)` raises, is caught by the
  existing `except (JSONDecodeError, KeyError, TypeError)`, and it prints
  `FAIL — could not parse git-tools scan privacy's result` to stderr and
  returns 1 — no traceback escapes the shim, exit code is 1 either way. This
  is exactly `UnparseableStdoutTests`' contract, exercised by a real, not
  mocked, malformed-stdout case.
- **Verdict for item 3: PASS for the shim's own contract (crash degrades to
  a clean exit-1 FAIL as designed), FAIL for "a sane result" from
  git-tools itself against workspace** — a crashing scanner is not a sane
  result no matter how gracefully the caller absorbs it. This is a git-tools
  binary defect (flaky SIGSEGV under concurrent regexp scanning on a
  larger tree), reproducible independent of the shim, and should be filed
  against git-tools/go-githooks, not against this shim.

## 4. Robustness fixes reproduced live (real environment, not mocked)

**Invalid `GIT_TOOLS_BIN` (nonexistent path), all fallbacks blocked** — run
live in marketplace and ai-shared-lib-datadog with `env -i PATH=/usr/bin:/bin
HOME=/tmp GIT_TOOLS_BIN=/nonexistent/git-tools`:
```
FAIL — git-tools binary not found (checked GIT_TOOLS_BIN, /tmp/.claude/plugins/data/governance-git-jr-claude-plugins/bin/git-tools, and $PATH)
```
exit code 1 both repos, no traceback either. **PASS**.

**Fallback chain, all three tiers exercised live** (marketplace):
- override present but invalid, no known-local-install, no PATH match → exit 1
  clean (above).
- override absent, known-local-install present (`$HOME/.claude/plugins/data/
  governance-git-jr-claude-plugins/bin/git-tools` populated with the
  verified binary), PATH empty → resolved and ran clean (exit 0).
- override absent, known-local-install absent, PATH populated with the
  verified binary → resolved and ran clean (exit 0).
- override present and valid, *both* other tiers also populated → override
  wins (`resolve_git_tools()` returned the override path directly, confirmed
  by direct call, not just indirectly via exit code).

All four fallback-chain live cases: **PASS**, matches
`resolve_git_tools`'s documented precedence and the mocked
`BinaryResolutionTests` suite's assertions, now confirmed unmocked.

## 5. CI provisioning step, five repos (all but knowledge-private-personal)

All five (`marketplace`, `workspace`, `knowledge-private-datadog`,
`knowledge-public-datadog`, `ai-shared-lib-datadog`) provision identically:
download the pinned asset, `sha256sum` it, compare against
`expected_sha256=1b48117682d9d15bdd3339898d90b54b35c86bac5b842a20cb9289c18a954dc6`,
`exit 1` on mismatch *before* `chmod +x` or setting `GIT_TOOLS_BIN` — the
binary is never made executable or referenced until after the digest check
passes. **PASS**, all five, digest-before-use confirmed by reading the step
body in each file.

Digest correctness: re-verified fresh in this session (section above) that
`1b48117682d9d15bdd3339898d90b54b35c86bac5b842a20cb9289c18a954dc6` is the
**extracted binary's** own hash from the release's `binary-checksums.txt`,
distinct from the **archive's** hash (`d721d9922e91dcb66c458947ec5f28fd452a708bd7ef05c18f166272f1879d14`,
`checksums.txt`). All five workflows pin the correct (binary, not archive)
digest. **PASS**.

Step ordering (provisioning before the guardrail step that consumes
`GIT_TOOLS_BIN`): confirmed correct in all five by reading step order —
marketplace and knowledge-public-datadog run `setup-python` → no-raw-binary
scan → provision → privacy scan; knowledge-private-datadog runs checkout →
no-raw-binary scan → provision → privacy scan; workspace runs checkout →
setup-python → no-raw-binary scan → provision → privacy scan; ai-shared-lib-
datadog runs checkout → provision → privacy scan → no-raw-binary scan. In
every case the provisioning step precedes the privacy-scan step that reads
`$GITHUB_ENV`'s `GIT_TOOLS_BIN`. **PASS**, all five.

YAML validity: `yaml.safe_load()` parsed all five without error;
`actionlint` (v1.7.12) ran against all five with **zero findings**. **PASS**,
all five, both syntactic and semantic (actionlint) validation.

knowledge-private-personal correctly carries no `.github/workflows/ci.yml`
(confirmed absent) — consistent with the task's own enumeration of "five
repos with a CI workflow."

## 6. No operator-specific absolute paths

`grep -rn "/home/bits"` across all six `scripts/check_privacy.py`, all six
`tests/test_check_privacy.py`, all five `.github/workflows/ci.yml`, and all
six `git-tools.yaml`: **zero matches**. **PASS**.

## Summary

| item | result |
|---|---|
| 1. byte-identical shim across 6 repos | PASS |
| 2. rewritten test suite, fresh run, hermetic | PASS (26/26 x 6 repos, plain + PATH-restricted) |
| 3. shim e2e, real tree, real tier, `--strict` | PASS x5 repos; workspace: shim degrades correctly, but git-tools itself crashes flakily (~60-70% of runs) — see finding below |
| 4. robustness fixes, live (not mocked) | PASS — invalid-override clean-exit-1 and 3-tier fallback chain all reproduced live in 2+ repos |
| 5. CI provisioning (5 repos): digest-before-use, correct digest, correct order, valid YAML | PASS, all 5 |
| 6. no operator-specific absolute paths | PASS, all 6 |

## Findings requiring follow-up (not shim defects)

1. **git-tools v1.3.0 flaky crash** (high severity, file against git-tools /
   go/githooks): scanning workspace's real tracked tree with `git-tools scan
   privacy --privacy-tier private` SIGSEGVs / fatal-errors intermittently
   (~60-70% of observed runs), consistent with a data race in the regexp
   backtracking/secret-pattern-matching path (`go/githooks@v0.6.1/secrets.go`,
   `sync.Pool` corruption). Reproducible with the exact CI-pinned, digest-
   verified binary; not reproduced against the other five (smaller) repos in
   10+ combined runs. The shim itself degrades this correctly (clean exit 1,
   no traceback), so this does not block the shim's own acceptance, but it
   means workspace's own CI guardrail step is currently flaky in a way that
   will intermittently fail CI runs for reasons unrelated to real content.
2. **marketplace's own `--strict`/public-tier CI guardrail would currently
   fail** against a real binary: `plugins/governance-git/CHANGELOG.md`,
   `README.md`, and `.task-reports/governance-git-repin-v1.3.0-quality-
   review.md` all trip the documented backtick-inline-code residual gap in
   git-tools' RFC 6761 sentinel-hostname fix (git-tools' own CHANGELOG entry
   for this exact gap: "a backtick is not one of those delimiters, so the
   same hostname written in markdown inline code is still flagged"). Not a
   shim defect — the shim's verdict derivation is correct — but worth
   surfacing since it means marketplace's live CI is one real run away from
   failing on this until that gap is closed or those three files are
   reworded/exempted.

## Overall verdict

**PASS** for the shim itself (`scripts/check_privacy.py`) — byte-identical
everywhere, unit-hermetic and green everywhere, resolution fallback chain
and invalid-override robustness fix both hold up live, CI provisioning is
digest-correct/ordered/valid in all five workflows that have one, and no
operator-specific paths leaked into any shipped file.

**FAIL (findings, not shim)** on the two follow-ups above: git-tools
v1.3.0's own flaky crash on workspace's tree, and marketplace's real
`--strict` CI guardrail currently failing on a known-documented residual
scanner gap. Neither is inside this shim's contract to fix, but both are
live, reproducible, and will surface the next time either repo's real CI
runs with a real binary.
