# D8 privacy-scan migration — quality review

Reviewed: branch `chore/d8-privacy-scan-migration`, implementation commit `89b4208`,
test-verification commit `2b7eea9`. Reference reading: `internal/cli/scan.go`,
`githooks@v0.5.0/{privacy.go,secrets.go,envelope.go}`,
`marketplace/scripts/check_privacy.py`, `marketplace-datadog/scripts/check_privacy.py`.

> Host and URL examples below carry one bracketed dot (`foo[.]internal.test`), and the
> private-address examples omit the `http://` scheme, so this page does not itself trip
> the scanner it is about — the same discipline `tests/test_check_privacy.py` uses on its
> own fixtures. Read them as the literal strings with the brackets removed and the scheme
> restored.

## Verdict

**ACCEPT WITH FIXES.**

The code D8 shipped is correct. The report D8 shipped was not, and is now corrected.
The test-engineer's FAIL was justified on its stated grounds; the grounds were a false
report claim, not a code defect. With the report corrected the deliverable is honest and
the real scope is characterized, so the FAIL is resolved without weakening any test or
forcing a green.

Two things changed from the test-engineer's picture, both found in this pass:

- The reserved-sentinel gap is **one of three** divergences, not one. Two more exist.
- The reserved-sentinel gap is a **false-positive** problem, not a strictness
  regression. Neither of the two laxer-direction divergences lets real sensitive
  content through. Full reasoning under "Severity".

## 1. D8's own code change — correct, confirmed independently

`internal/cli/scan.go:33-53` adds `codeMarkerExemptSuffixes` (the same twelve suffixes
as `check_privacy.py:57`'s `CODE_SUFFIXES`, diffed element by element) and renders them
as always-on `**/*<suffix>` marker-exempt rules, prepended in
`privacyMarkerExemptRules` ahead of any repo-configured `privacy_marker_exempt`.

Confirmed live against a fresh build: identical frontmatter marker text in a `.py` file
produces zero findings; the same text in an `.md` file still fails at the exact expected
count. The exemption feeds only `MarkerExemptRules`, never `SkipRules` — verified by
reading the `ScanPrivacy` call site (`scan.go:139-144`, `:316-321`) and
`PrivacyOptions`' own contract, so secret and internal-identifier scanning on code files
is untouched. This is the one genuine migration gap D8 set out to close, and it is
closed correctly, at the right layer, with adequate tests.

## 2. The differential-corpus claim — FALSE, reproduced and now characterized

I rebuilt the harness from scratch rather than reusing either prior one, snapshotted each
repo's own `git ls-files` output into a throwaway index-only git repo, and ran **that
snapshot's own copy** of `check_privacy.py` against it (so the script's self-path
exclusion, `check_privacy.py:247-248`, behaves exactly as it does in production)
head-to-head with a fresh `git-tools` binary, at each repo's real tier with `--strict`.

I then rewrote the harness a second time for commit as review evidence
(`.task-reports/d8-differential-corpus-harness.py`) and re-ran it end to end. Both
independently written harnesses produced identical divergences.

Honest result (committed-harness run):

| Repo | Tier (py -> go) | Files | Python | Go | Match |
|------|-----------------|-------|--------|----|-------|
| marketplace | public -> public | 552 | 0 | 1 | **no** |
| workspace | personal -> private | 400 | 0 | 0 | yes |
| knowledge-public-datadog | public -> public | 91 | 0 | 1 | **no** |
| knowledge-private-datadog | datadog -> confidential | 123 | 0 | 0 | yes |
| knowledge-private-personal | personal -> private | 30 | 0 | 0 | yes |
| marketplace-datadog | datadog -> confidential | 286 | 6 | 5 | **no** |
| ai-shared-lib-datadog | datadog -> confidential | 96 | 0 | 0 | yes |
| **Total** | | **1578** | **6** | **7** | **4 of 7** |

The file counts reproduce the report's own enumeration: 1577 on my first run, 1578 here
because one commit landed in workspace between the two runs. The findings do not
reproduce: the report claimed 0/0 and all-match; the truth is 4 of 7 match.

**One honest limit, disclosed rather than papered over.** marketplace-datadog's two
`corpus/chunks.jsonl` files (612 MB and 34 MB) exceed 50 findings each, so even a
single-file run truncates and their category sets may be incomplete. The harness prints
this. Every other file across all seven repos was compared with no truncation, and the
one divergence attributed to those two files (D-2) was confirmed independently by
extracting the matched value directly from the file.

### The three divergences

All three live in the pinned `go/githooks v0.5.0` dependency. None is in anything D8's
diff touches (`git diff main -- internal/cli/scan.go` adds only
`codeMarkerExemptSuffixes`/`codeMarkerExemptRules`).

**D-1 — `internal hostname` lacks the reserved-sentinel lookahead. Go stricter.**

- Python (`check_privacy.py:146-148,184-185`) appends `_RESERVED_SENTINEL_LOOKAHEAD`,
  excluding a host whose final label is a reserved documentation TLD.
- Go (`githooks@v0.5.0/privacy.go:109`) has no such lookahead:
  ```go
  {regexp.MustCompile(`(?i)\b[a-z0-9][a-z0-9-]*\.(?:corp|internal|intranet|lan)\b`), "internal hostname"},
  ```
- Reproduced in isolation: `foo[.]internal.test`, `bar[.]internal.example`,
  `baz[.]internal.localhost`, `qux[.]internal.invalid` → Go warns on all four, Python on
  none.
- Live on the real corpus via `tests/test_check_privacy.py`'s own sentinel fixture, in
  both public-tier repos. Marker-exemption does not help: `.py` is marker-exempt, and
  the internal-identifier check has no path exemption at all, by design
  (`PrivacyOptions` doc, `privacy.go:224-227`).

**D-2 — AWS example-key allowlist exists only on the Go side. Go laxer, justified.**

- Go exempts by exact match (`secrets.go:46-47,59`): `awsExampleAccessKeyIDs` holds
  `AKIAIOSFODNN7EXAMPLE`.
- Python has **no** AWS allowlist. `SECRET_PATTERNS` is applied with no value exemption
  (`check_privacy.py:283-285`), so every `AKIA[0-9A-Z]{16}` fails. The D8 report's row 7
  describes Python as having the same allowlist; it does not, in either the canonical
  327-line copy or marketplace-datadog's stale fork.
- The only such value anywhere in 1578 files is `AKIAIOSFODNN7EXAMPLE` — the canonical
  AWS documentation example key, extracted and confirmed unique. It provably cannot be a
  credential. Go's behavior is the better one.

**D-3 — `privateNetworkURL` guards with a consuming terminator, not a negative
lookahead. Go laxer, documented as intentional, synthetic shapes only.**

- Python: `...(?!\.(?:invalid|test|example…|localhost)(?=terminator))(?::\d+)?\b` — a
  negative lookahead only, so a private-range token that is merely the leading label of
  a longer host still matches.
- Go (`privacy.go:95-100`): `hostTerminator` is a *positive, consuming* group, so a
  following `.` kills the match.
- Reproduced: `http://127[.]0.0.1.invalid.attacker.io/x` and
  `http://192[.]168.1.5.attacker.io/x` → Python warns, Go does not.
- Go's own comment (`privacy.go:89-94`) states this is deliberate: the resolvable host
  in those strings is a public DNS name, not a private address. **Zero occurrences
  across all 1578 real files** — surfaced only on hand-built fixtures.

Directional fixture sweep, 20 cases across all four internal-identifier rules: 14 match,
4 diverge as D-1, 2 as D-3. Every genuine internal-host, private-address, loopback,
tracker-link, port-bearing and end-of-line shape matched on both sides.

### Why the original harness said 0/0 — two mechanisms, both now named

1. **Warnings were never counted.** marketplace's real public-tier `--strict` result is
   `privacy_violations_found: 0, privacy_warnings_found: 3`, exit 30. A harness reading
   only the violation count sees "0" while a live FAIL sits in the warning count.
   Python's exit was 0, so both sides agreed on the wrong number. This is the
   test-engineer's second hypothesis, now confirmed as the most probable mechanism.
2. **`errors[]` is capped at 50.** `githooks.EmitHookResult` truncates to
   `maxDiagnostics = 50` (`envelope.go:14,67-69`) and discloses it through a
   `caveats.githooks.findings_truncated` caveat. **My own first harness pass had this
   bug** and reported a fourth, phantom divergence on marketplace-datadog's docs corpus
   (34 MB, 113 warnings, 49 emitted). Honoring the caveat and re-running per file
   resolved it as a harness artifact. Worth stating plainly: this defect class is easy
   to reproduce, and any future differential harness must handle both mechanisms or it
   will report a false parity result again.

## 3. Severity, and the scope decision

### Severity: real, but not a security regression

The task set the test explicitly: is git-tools **less strict** than the reference in a
way that could let real sensitive content through?

- **D-1 is the opposite direction.** Go over-flags. Nothing escapes; the cost is noise.
- **D-2's only real-world instance is a published AWS documentation key.** Exact-match,
  enumerated, no wildcard, no domain-wide form. Nothing sensitive escapes.
- **D-3 misses nothing real.** In `http://192[.]168.1.5.attacker.io/x` the host that
  actually resolves is `attacker.io`. The RFC-1918-shaped leading label is not an
  address. Every genuine private-address form (`192.168.1.5/panel`,
  `10.0.0.7`, `127.0.0.1:8126/`, `localhost:8080/`) matched
  identically on both sides. Zero occurrences on the real corpus.

So: no strictness regression, no path by which real sensitive content passes git-tools
and would have been caught by `check_privacy.py`. This is the lower-priority
"differently-strict" case, matching the precedent this session already set for FB2 and
FB12 — pre-existing, out-of-diff-scope defects in an already-tagged sibling module.

D-1 does carry real operational urgency, but as a **cutover blocker**, not a security
issue: the moment git-tools' scan replaces `check_privacy.py`, both public-tier repos'
`--strict` gates start refusing legitimately-clean content. That has to be fixed before
the shim/soak/cutover stage, and it is cheap to fix. It is not a reason to fail D8.

### Scope: filed as feedback, not fixed here — and not fixable here

Beyond the severity judgment, a git-tools-side fix is **structurally impossible**:
`internalIDStrict`, `internalIDRelaxed`, `hostTerminator`, `markerPattern` and
`awsExampleAccessKeyIDs` are all unexported in `githooks`, and `PrivacyOptions` exposes
only `SkipRules`, `MarkerExemptRules`, `SecretExemptRules` and `EmployeeEmail` — no
internal-identifier or secret-pattern injection point (verified by grepping the module's
whole non-test surface). The only two routes are:

- edit `ai-shared-lib`'s `go/githooks` and repin — a different repo, explicitly out of
  bounds from this worktree; or
- have git-tools re-implement the internal-identifier patterns and post-filter
  `githooks`' findings — a fork of the shared scanner, far outside D8's file surface, and
  the wrong architectural move given B1 deliberately centralized these patterns.

Decision: **file as feedback.** The one-line change belongs in `ai-shared-lib` with its
own tests, followed by a `go/githooks` release and a repin across consumers.

### Feedback entry the orchestrator should file

One entry. Do not hand-edit `feedback.json`; `id` and `criticality` are derived by the
subcommand.

```
"$DAT_TOOLS" feedback add "$PROJ/feedback.json" \
  --source-task "git-tools-gate-and-privacy-rework:D8-quality-review" \
  --impact 3 --urgency 4 \
  --title "githooks v0.5.0's privacy patterns diverge from check_privacy.py in three places, one of which blocks the public-tier cutover" \
  --feedback "A rebuilt differential corpus over all seven repos' real tracked trees (1578 files, each repo's own tier, --strict) matches on 4 of 7, not 7 of 7 as D8's report claimed. Three causes, all pre-existing in the pinned go/githooks v0.5.0 and none in D8's diff. (1) privacy.go:109's 'internal hostname' pattern has no reserved-sentinel lookahead, so it flags foo[.]internal.test, bar[.]internal.example, baz[.]internal.localhost and qux[.]internal.invalid, which check_privacy.py:184-185 deliberately excludes via _RESERVED_SENTINEL_LOOKAHEAD. This is live today on both public-tier repos through tests/test_check_privacy.py's own sentinel fixture: git-tools --strict reports 3 warnings and exits 30 where check_privacy.py exits 0. (2) secrets.go:46's awsExampleAccessKeyIDs exempts AKIAIOSFODNN7EXAMPLE by exact match; check_privacy.py has no AWS allowlist at all and fails on it. (3) privacy.go:95's hostTerminator is a positive consuming group where Python uses a negative reserved-sentinel lookahead, so Go does not flag http://127[.]0.0.1.invalid.attacker.io/x or http://192[.]168.1.5.attacker.io/x. Only (1) surfaces on real content. Neither (2) nor (3) lets real sensitive content through, and privacy.go:89-94 documents (3) as intentional. Separately, githooks.EmitHookResult caps errors[] at 50 (envelope.go:14) and discloses it via caveats.githooks.findings_truncated; a harness that reads errors[] without honoring that caveat, or that counts only privacy_violations_found and ignores privacy_warnings_found, produces a false parity result. Both mechanisms were reproduced this pass." \
  --proposed-solution "In ai-shared-lib's go/githooks, append the reserved-sentinel lookahead to the internal-hostname pattern so it matches check_privacy.py:184-185, decide explicitly whether to keep divergences (2) and (3) and record the decision in the pattern comments, cut a go/githooks release, and repin git-tools. Add a characterization test asserting each reserved-sentinel host is NOT flagged, plus one asserting a genuine internal host still is, so the pair cannot regress. In git-tools, add a differential-harness regression test that counts privacy_warnings_found as well as privacy_violations_found and honors the findings_truncated caveat." \
  --why-it-matters "Divergence (1) blocks the shim/soak/cutover stage: once git-tools' scan replaces check_privacy.py, both public-tier repos' --strict pre-commit gates refuse legitimately-clean content, and any markdown page discussing DNS or networking conventions would trip the same false positive. The harness defects matter independently: they are how a differential-parity proof reported 0/0 across 1577 files while three real divergences were present, and they will do it again on the next parity check unless the next harness handles both."
```

Rationale for `--impact 3 --urgency 4`: impact 3 matches the FB2/FB3 band for a
pre-existing defect in a sibling module that blocks a real gate without being a security
hole; urgency 4 is above that band because D-1 stands directly in the cutover's path and
must be closed before the shim stage, unlike FB2/FB12 which can wait.

### Second feedback entry: git-tools has FB15's gap too

Found incidentally while checking whether this review's own reports would trip the gate.
`git-tools` ships **no `git-tools.yaml`**, so it defaults to `privacy_tier: public`
(`internal/cli/config.go:84`). Its own `main` therefore already fails its own
`--strict` privacy gate: `internal/cli/scan_gate_test.go:692` carries
`build[.]corp/status` as a legitimate test fixture, which is a genuine internal-hostname
shape that **both** scanners flag correctly — so this is not D-1, and not something the
githooks fix would resolve. `.go` files are marker-exempt but never
internal-identifier-exempt, by design (`privacy.go:224-227`). Confirmed by scanning this
worktree with the freshly built binary: 28 warnings, exit 30, of which the code finding
is pre-existing on `main`.

This is the same gap FB15 already records for `ai-shared-lib` and
`ai-shared-lib-datadog`. The orchestrator should either widen FB15 to name `git-tools`
as a third instance, or add the entry below. The same entry is the natural home for the
`TMPDIR` trap in the findings list — four tests fail whenever `TMPDIR` is inside a git
working tree, which is exactly where a low-disk host pushes you.

```
"$DAT_TOOLS" feedback add "$PROJ/feedback.json" \
  --source-task "git-tools-gate-and-privacy-rework:D8-quality-review" \
  --impact 2 --urgency 3 \
  --title "git-tools itself lacks a git-tools.yaml, so its own main fails its own --strict privacy gate" \
  --feedback "git-tools ships no git-tools.yaml and so defaults to privacy_tier: public (internal/cli/config.go:84). internal/cli/scan_gate_test.go:692 carries 'build[.]corp/status' (bracketed here so this entry does not itself trip the scanner) as a legitimate test fixture; that is a real internal-hostname shape both git-tools and check_privacy.py flag correctly, so a --strict scan of git-tools' own tree exits 30 on main. .go files are marker-exempt but never internal-identifier-exempt, by design, so the code-suffix exemption does not and should not cover it. Same gap FB15 records for ai-shared-lib and ai-shared-lib-datadog." \
  --proposed-solution "Add a git-tools.yaml declaring the correct tier for git-tools' own content, or add a secret/internal-id exemption for the test-fixture path, or rewrite the fixture host so it is not a live internal-hostname shape. Prefer the fixture rewrite: it keeps the gate at full strength instead of carving a path out of it." \
  --why-it-matters "Every sanctioned git-tools merge or push against its own repo needs a hand-passed override today, and the one repo that ships this gate cannot pass it — which undercuts the gate's credibility with the fleet it is being rolled out to."
```

## 4. Test-suite assessment

Adequate **for what D8 built**; a real gap remains for what D8 claimed.

- `TestCodeMarkerExemptRules_MatchesEveryCodeSuffixAnywhereInTree`
  (`internal/cli/scan_test.go:62-97`) — covers all twelve suffixes at three depths plus
  two negative shapes (suffix-as-substring, wrong suffix). Correct and sufficient for a
  glob-rendering unit.
- `TestScanPrivacy_CodeSuffixIsMarkerExemptByDefault`
  (`internal/cli/integration_test.go:752-777`) — real end-to-end CLI run, asserts the
  exact finding count so a spurious extra finding cannot hide. Correct.
- **Gap:** nothing in the suite pins parity against `check_privacy.py`. The entire parity
  claim rested on a one-off manual harness that was never committed and was wrong. A
  committed characterization test over a small fixture corpus, asserting both scanners'
  category sets agree, would have caught all three divergences and would catch the next
  one. Recommended as part of the follow-up task, not bolted on here — it needs the
  githooks fix first, or it would have to assert the buggy behavior.

No test was weakened, deleted, or relaxed in this pass.

## 5. Findings by severity

**Blocking (resolved in this pass)**

- `.task-reports/d8-privacy-scan-migration-report.md:102-137` (original numbering) — the
  differential-corpus section asserts a verification result that is not true and not
  reproducible. Corrected by an appended, clearly-marked correction section; the original
  text is left intact as the record of what was claimed.
- Same file, corpus-table rows 7, 11, 12 — each describes a rule as matching on both
  sides when it does not. Corrected in the same section.

**Major (out of this task's scope, filed)**

- `githooks@v0.5.0/privacy.go:109` — missing reserved-sentinel lookahead (D-1). Blocks
  the cutover for both public-tier repos.

**Minor (out of scope, folded into the same feedback entry)**

- `githooks@v0.5.0/privacy.go:95` — consuming `hostTerminator` vs. negative lookahead
  (D-3). Documented as intentional; needs an explicit keep-or-align decision, not a
  silent divergence.
- `githooks@v0.5.0/secrets.go:46` — AWS example-key allowlist exists only on the Go side
  (D-2). Go's behavior is better; the report's row 7 just describes it wrongly.
- `githooks@v0.5.0/envelope.go:14` — `maxDiagnostics = 50` truncation is correctly
  disclosed by caveat, but is a standing trap for any consumer building a finding set
  from `errors[]`.
- No `git-tools.yaml` in this repo, so `main` already fails its own `--strict` privacy
  gate on `internal/cli/scan_gate_test.go:692`. Pre-existing, FB15's gap, second
  feedback entry above.
- Four tests fail if `TMPDIR` points inside a git working tree, because each asserts a
  "not a git working tree" precondition that a temp dir nested in a repo cannot satisfy:
  `internal/cli/integration_test.go:159` and `:1017`, `internal/cli/tag_test.go:365`,
  `internal/gitexec/gitignore_test.go:141`. Found the hard way this pass — `/tmp` had
  741 MB free, so `TMPDIR` was pointed into the worktree and all four failed; the same
  four pass immediately with `TMPDIR` outside any repo. Pre-existing and unrelated to D8,
  but a real trap: every agent working this task has needed a `TMPDIR` workaround, and
  the obvious placement is the one that breaks the suite. Each test should create its own
  scratch directory outside any repository, or skip with a clear message when it detects
  it cannot get one, rather than failing with a misleading assertion.

## 6. Files touched by this review

- `.task-reports/d8-privacy-scan-migration-report.md` — added a correction notice at the
  head and a "Correction: what the differential corpus actually shows" section at the
  foot. No original line rewritten or removed.
- `.task-reports/d8-privacy-scan-migration-quality-review.md` — this report.
- `.task-reports/d8-differential-corpus-harness.py` — the harness behind the corrected
  result, committed so the number is re-runnable instead of narrated. Stdlib only, no
  venv. Handles both harness hazards named above; discloses residual truncation rather
  than swallowing it. This is the one addition beyond report text, and it exists because
  the uncommitted-harness pattern is exactly what produced the false claim.

No production code changed. No test changed. `.qr-tmp/` held the built binary, the repo
snapshots and a scratch `TMPDIR`; it is removed and never committed.

## 7. Re-verification

Fresh, on this worktree, after every edit above:

```
go build ./...                                              # exit 0, no output
go vet ./...                                                # exit 0, no output
TMPDIR=/tmp/d8-verify/qr-tmpdir go test ./... -count=1 -timeout 40m
?    github.com/johnrichter/git-tools/cmd/git-tools               [no test files]
ok   github.com/johnrichter/git-tools/internal/cli               1230.236s
ok   github.com/johnrichter/git-tools/internal/gitexec            106.668s
ok   github.com/johnrichter/git-tools/internal/hooks                0.089s
ok   github.com/johnrichter/git-tools/internal/result               0.010s
ok   github.com/johnrichter/git-tools/internal/signing             17.434s
ok   github.com/johnrichter/git-tools/internal/worktreeclean        41.488s
ok   github.com/johnrichter/git-tools/worktree-gate/detect           6.424s
?    github.com/johnrichter/git-tools/worktree-gate/detect/cmd/worktree-gate  [no test files]
ok   github.com/johnrichter/git-tools/worktree-gate/fixtures         0.007s
ok   github.com/johnrichter/git-tools/worktree-gate/lifecycle      148.196s
```

Every package passes, including both of D8's new tests. Reported as-run: an earlier
attempt with `TMPDIR` inside this worktree failed four tests, all of them "not a git
working tree" assertions that a temp dir nested in a repo cannot satisfy. Diagnosed by
re-running exactly those four with `TMPDIR` outside any repository — all four pass in
52s. Not a regression, and not caused by anything on this branch; recorded as a finding
because it is a real trap on a low-disk host.

Differential corpus, committed harness, fresh binary: 4 of 7 repos match, 3 diverge for
the three characterized causes above. That is the honest result, and it is the one now
recorded in the implementer's report.

## 8. Residual risk

- **D-1 will fail both public-tier gates at cutover.** Known, characterized, filed.
  Accepting D8 does not ship this risk — the repos still run `check_privacy.py`; the risk
  lands only if the cutover proceeds before the githooks fix.
- **D-3's direction is unresolved by decision, not by accident.** Two defensible designs
  disagree. Somebody has to choose; until then git-tools and the reference script differ
  on a shape neither has ever seen in real content.
- **`owner:`-keyed checks remain absent** by B1's tested design decision
  (`adversarial_test.go:322-338`). Inert on today's corpus. A future off-tier `owner:`
  value in a public repo would be caught by `check_privacy.py` and missed by git-tools.
- **`employee_email_domains` is unset in both public-tier repos**, so a real
  `user@datadoghq[.]com` address would be caught by `check_privacy.py` and missed by
  git-tools. Accurately described in the original report; a config step for the cutover
  stage.

## 9. Plan feedback

- **The plan's parity gate is unenforceable as written.** "Prove parity with a
  differential corpus" produced a false PASS because the harness was ad hoc, uncommitted,
  and unreviewed. Any future task carrying a parity acceptance criterion should require
  the harness itself as a committed artifact, reviewable and re-runnable, rather than a
  narrative claim about a run nobody else can repeat.
- **`marketplace-datadog`'s stale 256-line fork is a live confound** (LED-148, already
  filed). It is the only copy that lacks the reserved-sentinel logic and the role-alias
  allowlist, so it is the worst possible reference for a parity check — yet it is the
  reference for the one repo where D-2 surfaced. Any parity gate should name the
  canonical 327-line copy explicitly.
- **Sequence the githooks fix before the shim/soak/cutover stage.** D-1 is a hard
  precondition for the cutover, not a parallel cleanup item.
