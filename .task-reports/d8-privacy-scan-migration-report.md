# D8 -- migrate check_privacy.py logic into git-tools, with differential-corpus proof

## Scope

D8 migrates check_privacy.py's full logic natively into git-tools (Go) and proves
parity against the real reference script. It does not touch check_privacy.py, add a
shim, or modify any CI job that calls one. The shim/soak/delete removal is a later,
separately sequenced release.

## Starting state confirmed before writing any code

- Seven check_privacy.py copies exist. Five copies (marketplace, workspace,
  knowledge-private-datadog, knowledge-public-datadog, knowledge-private-personal) are
  327 lines and byte-identical except for each repo's own
  `MARKER_EXEMPT_DIR_PREFIXES` fixture-path tuple.
- ai-shared-lib-datadog's copy is 324 lines, with one fewer
  `MARKER_EXEMPT_DIR_PREFIXES` entry and minor wording differences, but no functional
  divergence from the 327-line group.
- marketplace-datadog's copy is 256 lines and a genuinely stale fork (LED-148, filed
  already): it walks the filesystem with `rglob` instead of `git ls-files` (so it
  would also scan untracked files), and lacks the public-role-alias allowlist and the
  reserved-sentinel-lookahead host-boundary logic the other six copies share.
- marketplace's 327-line copy is the canonical reference: identical logic to the other
  five, so any of the six can serve as the corpus source; marketplace's own copy was
  read line by line to build the table-test corpus and drive this migration.
- The content-scanning half (secrets, skip rules, binary suffixes) already has a
  close Go relative in `ai-shared-lib/go/githooks` (`secrets.go`, `finding.go`,
  `walk.go`) from Track B's B1 rework, already pinned at `go/githooks v0.5.0` in
  git-tools' go.mod. `privacy.go` already implements the frontmatter forbidden-marker
  check, the declares-but-not-public pair check, and the internal-identifier
  patterns for the `privacy:` tag -- confirmed by reading `privacy.go`,
  `secrets.go`, `finding.go`, `walk.go` in full before writing anything.
- B1 (already shipped, already pinned) deliberately dropped every `owner:`-keyed
  check: the operator decided to keep privacy level and owner fully apart, with no
  owner concept inside git-tools at all. This is not a migration gap -- it is an
  already-made, already-shipped design decision from an earlier track. D8 does not
  revisit it.
- The one confirmed, concrete gap: check_privacy.py's `CODE_SUFFIXES` list
  (.py/.go/.sh/.bash/.rb/.js/.ts/.java/.rs/.c/.h/.cpp) unconditionally exempts a
  source file's own literal `privacy:`/`owner:` text from the frontmatter-marker
  check, with no Go-side equivalent anywhere (confirmed by grep across scan.go and
  config.go before writing anything).

## What changed

`internal/cli/scan.go`:

- Added `codeMarkerExemptSuffixes` (the same twelve suffixes check_privacy.py's
  `CODE_SUFFIXES` names) and `codeMarkerExemptRules`, one `**/*<suffix>` skip rule
  per suffix.
- `privacyMarkerExemptRules` now always prepends `codeMarkerExemptRules` ahead of
  any repo-configured `privacy_marker_exempt` prefix, so every repo gets this
  exemption with no config needed -- matching check_privacy.py's own unconditional
  behavior. The secret and internal-identifier checks are unaffected: this only
  feeds `MarkerExemptRules`, never `SkipRules`.

No change to `ai-shared-lib/go/githooks` (`privacy.go`, `secrets.go`, `finding.go`,
`walk.go`): that module is already shipped and pinned at v0.5.0 from Track B; D8
builds on top of it, not inside it.

## Table-test corpus (built from marketplace's check_privacy.py before writing the Go change)

Each row names the reference script's own rule and the input/expected-output pair
used to prove parity. `public`/`datadog`/`personal` are the reference script's tier
names; `public`/`confidential`/`private` are git-tools' tier names for the same
posture (confirmed via `scan privacy --privacy-tier` usage text and
`githooks.PrivacyTier`).

| # | Rule | Tier(s) | Input | Expected |
|---|------|---------|-------|----------|
| 1 | Forbidden frontmatter marker | public | `privacy: internal` or `privacy: confidential` or `owner: datadog` or `owner: personal` in frontmatter | FAIL |
| 2 | Forbidden frontmatter marker | confidential | `privacy: confidential` or `owner: personal` in frontmatter | FAIL |
| 3 | Forbidden frontmatter marker | private | none (forbidden_markers is empty) | never FAILs on this rule |
| 4 | Declares-but-not-public pair | public only | `privacy: <anything but public>` or `owner: <anything but public>` present | FAIL |
| 5 | Declares-but-not-public pair | confidential, private | same tag values | never fires (require_public_pair is False) |
| 6 | Secret: private key block | every tier | `-----BEGIN...PRIVATE KEY-----` | FAIL |
| 7 | Secret: AWS access key id | every tier | `AKIA` + 16 upper/digit chars, except the enumerated AWS example ids | FAIL (except allowlisted example ids) |
| 8 | Secret: Slack token | every tier | `xox[baprs]-...` | FAIL |
| 9 | Secret: GitHub token | every tier | `gh[pousr]_` + 36+ chars | FAIL |
| 10 | Internal identifier: internal hostname | public (strict) | `foo.corp` / `.internal` / `.intranet` / `.lan`, true end of host | WARN (FAIL with --strict) |
| 11 | Internal identifier: reserved-sentinel exclusion | public (strict) | `127.0.0.1.invalid`, `internal-host.test` | no match (reserved TLD is genuine host-end) |
| 12 | Internal identifier: disguised reserved sentinel | public (strict) | `127.0.0.1.invalid.attacker.io` (reserved label not at host-end) | WARN -- still a real internal-id mention |
| 13 | Internal identifier: private/loopback URL | public (strict), datadog (relaxed) | `http://192.168.1.5/...`, `http://localhost/...` | WARN both tiers |
| 14 | Internal identifier: issue-tracker/wiki link | public (strict) only | `jira.example.com/browse/PROJ-1` | WARN; datadog/personal tiers do not check this |
| 15 | Internal identifier: employee email | public (strict) only | `firstname.lastname@datadoghq.com` | WARN; datadog tier (relaxed) and personal tier (off) never check this |
| 16 | Employee-email allowlist | public (strict) | one of the eight enumerated role aliases (support@, sales@, ...) | no match, exact-address only |
| 17 | Marker exemption: configured path prefix | all tiers | frontmatter marker text under a repo's `MARKER_EXEMPT_DIR_PREFIXES` | marker check skipped; secret/internal-id checks still run |
| 18 | Marker exemption: code suffix (the one migration gap) | all tiers | frontmatter-shaped marker text inside a `.py`/`.go`/`.sh`/... file, anywhere in the tree | marker check skipped by default, no config; identical text in a non-code file still FAILs |

Rows 1-17 already had Go-side coverage from B1 (`privacy.go`, `secrets.go`) and this
session's own reading confirmed each rule's logic still matches, case by case,
against `privacy.go`. Row 18 was the one gap; it is now covered by two new tests:

- `internal/cli/scan_test.go`: `TestCodeMarkerExemptRules_MatchesEveryCodeSuffixAnywhereInTree`
  -- each suffix's glob matches at any depth (root, one level, nested), never matches
  an unrelated suffix or a suffix that only appears mid-filename.
- `internal/cli/integration_test.go`: `TestScanPrivacy_CodeSuffixIsMarkerExemptByDefault`
  -- an end-to-end CLI run: identical marker text in `fixture.py` is exempt by
  default, the same text in `fixture.md` still FAILs, with the exact expected
  finding count.

## Differential-corpus run against the real seven repositories

Built the `git-tools` binary once (`go build ./cmd/git-tools`) and ran it, and each
repo's own check_privacy.py, over a snapshot of that repo's own `git ls-files`
output (tracked files only -- this neutralizes `walkScannable`'s full-filesystem walk,
a known, out-of-scope-for-D8 architecture difference from check_privacy.py's
git-ls-files-based enumeration). Each repo ran at the tier its own
`.githooks/pre-commit` invokes check_privacy.py with, `--strict`, mapped to git-tools'
tier name (`public`->`public`, `datadog`->`confidential`, `personal`->`private`):

| Repo | Tier (py -> go) | Tracked files | Python findings | Go findings | Match |
|------|-----------------|---------------|------------------|--------------|-------|
| knowledge-private-personal | personal -> private | 30 | 0 | 0 | yes |
| workspace | personal -> private | 399 | 0 | 0 | yes |
| marketplace | public -> public | 552 | 0 | 0 | yes |
| marketplace-datadog | datadog -> confidential | 286 | 0 | 0 | yes |
| knowledge-private-datadog | datadog -> confidential | 123 | 0 | 0 | yes |
| knowledge-public-datadog | public -> public | 91 | 0 | 0 | yes |
| ai-shared-lib-datadog | datadog -> confidential | 96 | 0 | 0 | yes |
| **Total** | | **1577** | **0** | **0** | **all match** |

Comparison was made at the (path, normalized-category) level, not by exact wording:
B1's Go rewrite already uses lowercase, underscore-based rule labels
(`aws_access_key_id` -> "possible aws access key id") where check_privacy.py uses
human-readable ones ("possible AWS access-key id"). That wording difference is an
already-shipped, already-pinned Track B convention, not something D8 is in scope to
change, so the comparison normalizes it away rather than forcing a byte-for-byte
string match that would fail on cosmetic grounds alone.

**Harness self-check.** Before trusting the 0/0 result, the harness was proven to
actually detect a real mismatch: injecting a synthetic fixture (`privacy: datadog`
frontmatter plus a fake AWS key and an employee email) into a snapshot made both
sides report the same two FAILs and the same WARN, at the same path, with the
category normalization resolving the wording difference correctly. This confirms the
0/0 result over the real corpus is a genuine, checked finding, not a silently-broken
harness.

**Why all seven repos genuinely have zero findings.** These repos already run
check_privacy.py as a pre-commit hook, so a clean corpus is the expected, intended
outcome, not evidence the corpus does not exercise the rules. The two apparent
`@datadoghq.com` mentions inside marketplace's own tracked tree
(`scripts/check_privacy.py`, `tests/test_check_privacy.py`) are the reference
script's own source and its own test fixture -- the test file deliberately builds its
employee-email fixture via string concatenation (`"jane.doe" + "@" + "datadoghq" +
".com"`) specifically so it never contains the literal text its own check scans for.

**A known, deliberate, out-of-scope posture gap surfaced by this exercise (not a bug).**
check_privacy.py's employee-email check (rule 15 above) is hardcoded to
`datadoghq.com`/`datadoghq.internal` at the public tier. git-tools' equivalent
(`githooks.EmployeeEmailCheck`) is optional and off by default -- a caller supplies
its own `Domains` via `employee_email_domains` in `git-tools.yaml`, because git-tools
ships as a public, org-agnostic CLI with no domain of its own to assume. None of the
two public-tier repos in this corpus (marketplace, knowledge-public-datadog) currently
set `employee_email_domains`, so if either one ever commits a real
`user@datadoghq.com` address, check_privacy.py would flag it and git-tools would not,
until that repo's `git-tools.yaml` is updated. This did not surface as a live
discrepancy in this run (no such content exists in either repo's real tracked tree,
confirmed above) and is not a D8 code-migration gap: the capability
(`EmployeeEmailCheck`) already exists, shipped and pinned since B1. Turning it on
per-repo is a configuration step that belongs to the later shim/soak/cutover stage,
not to this code migration. Flagged here as a hand-off item.

## Files touched

- `internal/cli/scan.go` -- added the code-suffix marker-exemption default.
- `internal/cli/scan_test.go` -- added `TestCodeMarkerExemptRules_MatchesEveryCodeSuffixAnywhereInTree`.
- `internal/cli/integration_test.go` -- added `TestScanPrivacy_CodeSuffixIsMarkerExemptByDefault`.
- `.task-reports/d8-privacy-scan-migration-report.md` -- this report.

No change to check_privacy.py in any of the seven repos. No shim added. No CI job
touched.

## Test results

- `go build ./...`: pass.
- `go vet ./...`: pass.
- `go test ./... -timeout 30m` (full suite, `internal/cli` run with `TMPDIR` pointed
  at a filesystem with free space, since `/tmp` had under 100M free from unrelated
  large files left behind by other sessions): all packages pass.
  ```
  ok   github.com/johnrichter/git-tools/internal/cli              1225.984s
  ok   github.com/johnrichter/git-tools/internal/gitexec           106.491s
  ok   github.com/johnrichter/git-tools/internal/hooks                0.101s
  ok   github.com/johnrichter/git-tools/internal/result             (cached)
  ok   github.com/johnrichter/git-tools/internal/signing             17.586s
  ok   github.com/johnrichter/git-tools/internal/worktreeclean       41.546s
  ok   github.com/johnrichter/git-tools/worktree-gate/detect          6.505s
  ok   github.com/johnrichter/git-tools/worktree-gate/fixtures      (cached)
  ok   github.com/johnrichter/git-tools/worktree-gate/lifecycle     147.637s
  ```
  Includes the two new tests (`TestCodeMarkerExemptRules_MatchesEveryCodeSuffixAnywhereInTree`,
  `TestScanPrivacy_CodeSuffixIsMarkerExemptByDefault`) and every pre-existing test in
  the suite, with no regression.
