# B3 / B4 quality review

- Worktree: `/home/bits/Development/workspaces/psa-platform/git-tools/.claude/worktrees/b3-b4-privacy-tier-consumers`
- Branch: `fix/privacy-tier-consumers-and-config-path`
- Reviewed: `102cb69` (B3, githooks v0.5.0 tier rename), `7513f2a` (B4, config.go fixes)
- Fix commit added: `3f798ae` "Take the employee-email domains from config, not from this source" (`102cb69` and `7513f2a` untouched)

## Verdict

`FIX-APPLIED` — accepted after fixes. B3 carried a blocking privacy defect (an organization's real mail domain and eight real role addresses hardcoded into this CLI's own shipped source) plus a real-domain test fixture. Both are removed and replaced with a per-repo config surface. B4 is correct as written. One additional correctness gap found and fixed.

## Blocking finding (fixed): the relocated identity leak

`internal/cli/scan.go:33-47` (as of `102cb69`) declared a package-level `gitToolsEmployeeEmail` holding the organization's two real mail domains and eight real role addresses, wired into both `PrivacyOptions{EmployeeEmail: ...}` sites (`scan.go:137` in `newScanPrivacyCmd`, `scan.go:292` in `scanTree`). Line numbers in this section are as of `102cb69`; all others in this report are as of the final tree.

Two independent reasons this had to go:

1. **It is the same leak this effort exists to remove, relocated.** B1 (`ai-shared-lib` `c4de1f3`) deliberately delisted the org identity from `go/githooks`' public source and made the check opt-in. Re-adding those literals one layer down puts them back into shipped, public source — `git-tools` was published to the public `marketplace` repo in this same effort.
2. **It is wrong on its own terms.** `git-tools` is a general-purpose CLI. Any other user of it got one unrelated organization's domains baked in, with no way to supply their own. An org-specific value has exactly one correct home: the consuming repo's own config.

### The replacement config surface

Modeled directly on the existing `privacy_marker_exempt` / `secret_scan_exempt` mechanism — per-repo YAML keys with an empty default, no CLI flag (matching how the two existing exempt lists are surfaced).

| Element | Location |
|---|---|
| `EmployeeEmailDomains []string` \`koanf:"employee_email_domains"\` | `internal/cli/config.go:66` |
| `EmployeeEmailAllowlist []string` \`koanf:"employee_email_allowlist"\` | `internal/cli/config.go:73` |
| Empty defaults (check off unless configured) | `internal/cli/config.go:89-90` |
| `employeeEmailCheck(cfg *Config) githooks.EmployeeEmailCheck` converter | `internal/cli/scan.go:250-260` |
| Wired at `newScanPrivacyCmd` | `internal/cli/scan.go:121` |
| Wired at `scanTree` (merge/push/rebase/tag-create gate + `scan all`) | `internal/cli/scan.go:296` |

Shape rationale: `Domains` passes through verbatim (githooks escapes each entry with `regexp.QuoteMeta` and drops blank entries, so no consumer-side validation is needed or possible); `Allowlist` converts the YAML string list into the `map[string]bool` the library wants, and is only allocated when non-empty so the zero value stays a true "check off". No usage-error validation was added, unlike the glob exempt lists: a malformed glob there fails *open* (exempts the whole tree), whereas a typo'd domain or allowlist address here fails *closed* (simply never matches, or simply fails to exempt), so there is nothing to fail loudly about.

With no `git-tools.yaml` present — this repo's own state — the check is off and `git-tools` holds no organization's domains anywhere in its source.

### Test changes

- `internal/cli/integration_test.go:598-603` — `employeeEmailConfig`, a placeholder-domain (`example.com`) config fixture, replacing the real domain the test previously used as fixture data.
- `internal/cli/integration_test.go:609` — `TestScanPrivacy_InternalEmailWarnsWithoutStrict` now commits that config into the scratch repo and drives the check through it. Strengthened beyond a like-for-like port: the fixture doc holds both a flagged individual address and an allowlisted role address at the same domain, and the test asserts exactly one caveat, so it now proves the allowlist half of the new surface too, not just the domain half.
- `internal/cli/integration_test.go:633` — `TestScanPrivacy_EmployeeEmailCheckOffWithoutConfig`, new. Pins the check off with no config: a `person@example.com` mention scans `success/0`. This is the regression guard against the leak being reintroduced — any future hardcoded default domain fails this test.

### Org-name search result

`grep -rin datadog` over the whole tree (excluding `.git` and this report) now returns exactly three hits, all in `internal/cli/integration_test.go`:

| Line | What |
|---|---|
| 654 | Doc comment on `TestScanPrivacy_RetiredTierWireValues_AreUsageErrors` |
| 660 | Its `[]string{"datadog", "personal"}` table |
| 846 | The same table in the new `TestHooksInstall_RetiredTierWireValues_AreUsageErrors` |

All three are the retired *tier wire values* — enum strings a hard-cutover test must name to prove they are rejected — not identity or fixture data. Zero hits in non-test source, and no test uses the organization's real domain any longer (the one that did, the employee-email fixture, now uses `example.com`). See "Residual risk".

## Other review points

### 1. B3's hard cutover — confirmed genuine

- `TestScanPrivacy_RetiredTierWireValues_AreUsageErrors` (`integration_test.go:657`) runs the **built binary** with `--privacy-tier datadog` and `--privacy-tier personal` and requires `usage`/exit 50 for both. Real end-to-end evidence, not a unit assertion.
- No backward-compatibility handling exists anywhere. The only tier gate in the CLI is `githooks.PrivacyTier(cfg.PrivacyTier).Known()`, at `scan.go:108` (`scan privacy`), `scan.go:144` (`scan all`), and `scan.go:323` (`scanGate`, shared by merge/push/rebase/tag create) — plus `hooks.go:42`, added by this review. `Known()` is a closed map lookup in the library; a retired value has no path other than the usage error. `grep` finds no alias table, no deprecation branch, no rewrite of the old values.
- Shipped text is fully updated: `root.go:52` flag help, `scan.go:101` example, and every usage-error string names `public, confidential, private`.

### 2. The `owner:` forbidden-marker removal — confirmed deliberate and upstream

Verified in the pinned library's own source rather than taken on report:

- `ai-shared-lib/go/githooks/privacy.go:55-76` — `privacyTierConfigs` keys only on `privacy:`; no tier carries an `owner:` pattern, and `fmPairChecks` (`privacy.go:81-87`) has only the `privacy` pair.
- `ai-shared-lib/go/githooks/adversarial_test.go:322-336` — `TestScanPrivacyNoOwnerConceptReachable` asserts an `owner: confidential` frontmatter tag produces **no** finding at any tier. The absence is pinned by an intentional test, which is the strongest available signal that it is designed, not dropped.
- `ai-shared-lib` `c4de1f3` states the intent: "privacy tier and ownership are separate concepts, and this module owns only the former."

The consumer-side consequence is a genuine, unrecoverable capability loss (no `PrivacyOptions` field restores it) and correctly out of scope here. The implementer's fixture repair — `TestScanPrivacy_DetectsForbiddenMarker` now plants `privacy: internal` (`integration_test.go:589`) — is the right substitute: it is a real public-tier forbidden marker, so the test still proves marker detection rather than being weakened to pass.

### 3. B4's two fixes — both correct

**Directory-mismatch resolution.** `loadConfigFile` now joins the default filename against `repoDirForConfig(fs)` (`config.go:148`, helper at `config.go:171-181`). The helper resolves `--repo` flag → `GITTOOLS_REPO` env → `"."`, mirroring `loadConfig`'s own precedence, and correctly cannot use the full koanf resolution (that resolution depends on this function). An explicit `--config` path still resolves against the process cwd, which is right: an explicitly named path is the caller's own, not the target repo's. Proven twice — at unit level (`TestLoadConfig_DefaultResolvesAgainstRepoFlagTarget`) and end-to-end through `merge` (`TestScanGate_PrivacyMarkerExemptConfigLoadedFromRepoFlagTarget`, rewritten from the test that previously documented the bug, and now asserting the merge actually fast-forwards to the feature tip).

**Untracked/dirty-config warning.** `warnIfConfigTampered` (`config.go:195-211`) is safe in the direction that matters: it only ever *emits* a warning and never asserts cleanliness. Every failure mode — git missing, `sysops.Run` error, non-zero exit, path outside a repository — returns silently, so the worst case is a missed warning, never a false "clean" claim. It cannot block: no error is returned to the caller and `loadConfigFile` proceeds unconditionally. Four tests cover untracked, locally-modified, and clean-tracked (no output), plus the load-still-succeeds property in each.

Minor, not fixed: a staged-but-never-committed config reports as "locally modified (differs from the committed HEAD version)", which is literally true but reads oddly for a file that has no HEAD version. Not worth a branch. Also `sysops.Run` is called with `context.Background()` and no timeout; a wedged `git status` would hang the CLI rather than skip the warning. Left alone — `warnIfConfigTampered` is one local `git status` on one pathspec, and the rest of this file makes the same choice; worth a bounded context if a timeout is ever added to the config-load path generally.

Doc fix applied: `scan_gate_test.go:213-217`'s `writeConfig` comment still claimed auto-discovery happens from "the invoking process's own working directory", which B4 made false. Rewritten to name `--repo`'s target and to state why the helper leaves the file untracked (it exercises the warn-don't-block design).

### 4. Commit messages — conform

Both are in `main`'s release-note voice: imperative subject under ~72 chars, body explaining the defect and the reasoning, `--` for dashes, no bullet-list changelog. Neither carries an attribution trailer (`git log --format='%(trailers)'` is empty for both), matching all eight preceding commits on `main`. B3's closing paragraph correctly records the fleet-wide `hooks install` re-run this cutover forces.

## Additional finding (fixed): `hooks install` did not validate the tier

`internal/cli/hooks.go` passed `cfg.PrivacyTier` straight into `hooks.InstallOptions` with no `Known()` check, while every scan path validates. The hook script embeds the tier verbatim and passes it back to `scan all`, so a retired or misspelled value **installed successfully** and then failed *every subsequent commit* with a usage error instead of scanning — a broken guardrail, discovered at commit time rather than install time.

Pre-existing, but B3 turns it into a live hazard: B3's own commit message requires every already-installed repo to re-run `hooks install`, and any repo whose config or environment still carries `datadog`/`personal` would bake a permanently-failing hook.

- Fix: `internal/cli/hooks.go:35-42` refuses with the same `usage.cli.invalid_privacy_tier` code and message text the scan paths use, before anything is written.
- Test: `TestHooksInstall_RetiredTierWireValues_AreUsageErrors` (`internal/cli/integration_test.go:844`) covers `datadog`, `personal`, and `nonsense`, asserting `usage`/50 **and** that no hook script exists afterward.

## Fixes applied

| Change | Files |
|---|---|
| New `employee_email_domains` / `employee_email_allowlist` config keys, empty by default | `internal/cli/config.go` |
| Removed `gitToolsEmployeeEmail` and its hardcoded org values; added `employeeEmailCheck`; wired both scan-construction sites | `internal/cli/scan.go` |
| Real-domain fixture replaced with a placeholder driven through config; allowlist assertion added; new off-by-default regression test | `internal/cli/integration_test.go` |
| `hooks install` tier validation + test | `internal/cli/hooks.go`, `internal/cli/integration_test.go` |
| Stale `writeConfig` doc comment corrected | `internal/cli/scan_gate_test.go` |

All committed as `3f798ae` per the dispatch's instruction to fix as a new commit; `102cb69` and `7513f2a` were not amended. This report is left untracked in `.task-reports/` — the same untracked location the implementer's own (now-lost) report used; see "Plan feedback".

`git grep -in datadog -- ':!*_test.go'` on the committed tree returns zero hits.

## Re-verification

- `gofmt -l .` — clean.
- `go build ./...`, `go vet ./...` — clean.
- Focused: all `TestScanPrivacy*`, `TestLoadConfig*`, `TestScanGate_PrivacyMarkerExempt*`, `TestHooksInstall*` pass, including the three new/rewritten tests.
- Full: `go test -timeout 30m -count=1 ./...` — **green, exit 0**, all 10 packages with tests:

```
ok  internal/cli                1088.704s
ok  internal/gitexec             107.194s
ok  internal/hooks                 0.093s
ok  internal/result                0.007s
ok  internal/signing              17.971s
ok  internal/worktreeclean        39.029s
ok  worktree-gate/detect           6.618s
ok  worktree-gate/fixtures         0.007s
ok  worktree-gate/lifecycle      148.452s
```

Note for future runs: `go test`'s default 10m per-package timeout is **not** enough for `internal/cli` (1089s alone, since it rebuilds the CLI per test). A bare `go test ./...` fails on timeout, not on a real defect. CI already uses `-timeout 20m` (`.github/workflows/ci.yml:120`).

## Test-suite assessment

Adequate, and improved by this review. Strengths: the cutover is proven through the built binary rather than by unit assertion; B4's warning is covered in all three states plus the load-still-succeeds property; the rewritten directory-mismatch test asserts the merge actually lands.

Gaps closed here: the employee-email check had no off-by-default test (the hardcoded var meant that state was untestable), no allowlist assertion, and `hooks install` had no invalid-tier coverage at all.

Remaining gap, not closed: no test covers `GITTOOLS_REPO` as the source of `repoDirForConfig`'s answer — only the `--repo` flag branch (`config.go:177`) is exercised. Low risk (two lines, obvious), but it is an untested precedence branch.

## Residual risk

- **`datadog` / `personal` survive as string literals in `integration_test.go:654,660,846`.** They are the retired tier wire values, and a hard-cutover test cannot prove they are rejected without naming them (line 846 is my own new install-side test, which makes the same trade deliberately). They are not the org domain and not fixture identity data, and the public-tier privacy scan does not flag a bare company-name mention, so `git-tools` scans clean at `privacy:public`. If the effort's requirement is literally zero occurrences of the string, this test's evidence would have to be weakened — I judged the regression proof worth more than the two remaining mentions. Flagging for an explicit decision rather than deciding silently.
- **The employee-email check is now off for every consumer until a repo opts in.** That is the intended design, but it is a real detection reduction relative to pre-upgrade behavior for whichever repos relied on it. The fleet-wide follow-up B3 already names (re-run `hooks install`) should be extended to "add `employee_email_domains` to the `git-tools.yaml` of each repo that wants this check", or the check silently stays off.

## Plan feedback

1. **The implementer's report `.task-reports/B3-B4-report.md` does not exist** anywhere in the worktree or the wider workspace. It was presumably an untracked file lost with the interrupted attempt's discarded changes. This review was reconstructed from the two commit messages (which are unusually thorough) and from reading the code, the pinned library, and B1's commit — but the evidence chain was rebuilt, not verified against the implementer's own claims. `.task-reports/` is not gitignored and not tracked; if these artifacts are meant to survive an interruption, they need to be either committed or written outside the worktree.
2. **The leak class needs a standing guard, not per-task vigilance.** B3 removed the org identity from the shared library and reintroduced it in a consumer in the same effort, with all tests green. A CI check that fails on the organization's name in tracked source would catch the next occurrence; `TestScanPrivacy_EmployeeEmailCheckOffWithoutConfig` only guards this one code path.
3. **`hooks install`'s missing validation suggests a general audit.** Every command that persists a config value into generated artifacts should validate it the way the scan paths do. `hooks install` was the one instance found here; worth confirming no other verb writes an unvalidated setting into a file.
