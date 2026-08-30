# D8 privacy-scan migration — independent test-verification

## Verdict

**FAIL.** Not because of the frontmatter-tag contradiction (resolved in the implementer's
favor — see below), but because direct reproduction of the D8 report's own differential-corpus
methodology, on the same repos at the same tiers with the same `--strict` flag, produces a real
mismatch the report claims does not exist. The report's central evidence ("0 findings on both
sides" across 1577 files) is not reproducible as stated for at least two of the seven repos
(marketplace, knowledge-public-datadog — both public-tier, both run with `--strict` per the
report's own methodology). The task's own suffix-exemption change (the one thing D8 actually
built) is correct and adequately tested; the FAIL is about the report's differential-corpus
verification claim being false, not about the addition ceasing to work.

## 1–2. The frontmatter-tag contradiction — resolved: the implementer is correct, the audit misread a comment

Read `internal/cli/scan.go` on `main` and on this branch, and `githooks@v0.5.0/privacy.go`
(the pinned dependency `internal/cli/scan.go` calls into).

- `main:internal/cli/scan.go:196` (same line, unchanged by this branch) is the middle of a doc
  comment for `privacyMarkerExemptRules`:
  ```
  // gets the secret and internal-identifier scan, just not the frontmatter-
  // marker check.
  ```
  This sentence describes what `MarkerExemptRules` does to an *exempted path* (it still gets the
  secret/internal-id scan, only the marker check is skipped for that one path). It is not a
  statement that no frontmatter-tag validator exists in the codebase at all. The audit's reading
  ("scan.go:196 explicitly notes the frontmatter half is absent") is a misreading of this
  exemption-semantics comment as a completeness admission — it is neither about completeness nor
  about the general case, only about the one exempted-path case.

- The actual validator: `githooks.ScanPrivacy` (`privacy.go`), called from
  `internal/cli/scan.go:139` and `:316` via `newScanPrivacyCmd`/`scanTree`, implements both:
  - `forbidden_marker` — tier-scoped forbidden `privacy:` values (`internal`/`confidential`
    at public tier, `confidential` at confidential tier), directly in the file's own frontmatter
    block (`frontmatterBlock`, `privacy.go:305-319`).
  - `not_public_pair` — public-tier-only "declares `privacy:` but not `privacy:public`" check
    (`fmPairChecks`, `privacy.go:81-87`).
  - Live proof (see §4): a synthetic file with `privacy: internal` / `privacy: restricted`
    frontmatter run through the freshly built binary produces exactly `forbidden_marker` and
    `not_public_pair` findings, matching check_privacy.py's own rule semantics.

- What is genuinely absent, by design, not oversight: **`owner:`-keyed checks**. Confirmed by
  `githooks@v0.5.0/adversarial_test.go:322-338`,
  `TestScanPrivacyNoOwnerConceptReachable`, which explicitly asserts a file with
  `owner: confidential` produces zero failures at every tier — a deliberate, tested contract from
  Track B/B1 (pinned before D8, untouched by D8's diff). check_privacy.py's own `owner:`
  forbidden-marker and pair checks (`scripts/check_privacy.py:96,104,120`) have no Go-side
  equivalent at all. This is a real, structural blind spot, but it produced no live discrepancy in
  the actual corpus: `git grep` across all seven repos' real tracked frontmatter for
  `owner:\s*(datadog|personal|internal|confidential)` outside `.py`/test-fixture files found
  matches only in `knowledge-private-datadog` (`owner:datadog`, hundreds of files) and
  `knowledge-private-personal` (`owner:personal`) — both self-consistent with the tier those
  repos are actually scanned at (confidential/private, where the pair-check and forbidden-marker
  sets that would key on those values are empty or don't apply on the Python side either). No
  public-tier repo (marketplace, knowledge-public-datadog) has a live `owner:` violation. So: the
  implementer's characterization ("already-made, already-shipped design decision, not a D8
  migration gap") is accurate for the current corpus, but the report does not flag it as a
  residual risk the way it flags the employee-email domain gap — it should, since a future
  off-tier `owner:` mistake in a public repo would be silently missed by git-tools and caught by
  check_privacy.py. This alone would not fail the task (the gap is genuinely pre-existing,
  outside D8's stated scope, and inert today) — it is a report-completeness nit, not a code
  defect.

**Conclusion on the contradiction: the implementer's claim is correct; the audit's "no
frontmatter-tag validator" claim is a misreading of an unrelated exemption-semantics comment.**

## 3. Independent build/vet/test — reproduced clean

```
go build ./...           # exit 0, no output
go vet ./...              # exit 0, no output
TMPDIR=/tmp/d8-verify/tmpdir go test ./... -count=1 -timeout 30m
ok  	github.com/johnrichter/git-tools/internal/cli              1229.318s
ok  	github.com/johnrichter/git-tools/internal/gitexec           106.504s
ok  	github.com/johnrichter/git-tools/internal/hooks               0.089s
ok  	github.com/johnrichter/git-tools/internal/result              0.007s
ok  	github.com/johnrichter/git-tools/internal/signing            17.458s
ok  	github.com/johnrichter/git-tools/internal/worktreeclean      41.537s
ok  	github.com/johnrichter/git-tools/worktree-gate/detect          6.414s
ok  	github.com/johnrichter/git-tools/worktree-gate/fixtures       0.003s
ok  	github.com/johnrichter/git-tools/worktree-gate/lifecycle    147.969s
```
Matches the report's claimed test run (same TMPDIR workaround needed — `/tmp` was at 98%
capacity, 730M free, independently confirmed via `df -h`). No regression, no flake observed.

## 4. Differential-corpus re-run — the report's "0/0" claim does NOT hold as stated

Built a fresh binary (`go build -o /tmp/git-tools-test ./cmd/git-tools`, HEAD `89b4208`) and ran
it head-to-head against each repo's own real `scripts/check_privacy.py`, at the same tier/flag
combination the report itself specifies (`--strict`, tier mapped `public->public`,
`datadog->confidential`, `personal->private`).

**Positive result: the frontmatter-tag path is genuinely exercised.** A synthetic fixture with
real `privacy: internal` / `privacy: restricted` frontmatter, run through the built binary,
produces `forbidden_marker` and `not_public_pair` findings exactly as check_privacy.py would.
The code-suffix exemption (D8's actual deliverable) also reproduces exactly as claimed: identical
marker text in `.py` is exempt, the same text in `.md` still fails with the same finding count.
This much of the report is accurate and independently reproduced.

```
$ git-tools scan privacy --repo <synth> --privacy-tier public --strict   # privacy:internal/restricted frontmatter
{"privacy_violations_found":3, ...
  "leak.md" forbidden_marker "forbidden frontmatter marker \"privacy: internal\""
  "leak.md" not_public_pair  "frontmatter declares privacy: tag but not privacy:public"
  "nopair.md" not_public_pair "frontmatter declares privacy: tag but not privacy:public"}

$ git-tools scan privacy --repo <synth-suffix> --privacy-tier public --strict   # fixture.py vs fixture_copy.md, identical text
{"privacy_violations_found":2, ...
  "fixture_copy.md" forbidden_marker / not_public_pair only — fixture.py: 0 findings}
```

**Negative result: real mismatch found on the real corpus, contradicting the report.**
Running the actual `check_privacy.py` and the actual `git-tools` binary against the actual,
unmodified working trees of two of the seven repos — at exactly the tier/flag the report's own
methodology uses:

```
$ python3 marketplace/scripts/check_privacy.py --tier public --root marketplace --strict
OK — no privacy violations under marketplace (tier=public).           # exit 0

$ git-tools scan privacy --repo marketplace --privacy-tier public --strict
{"privacy_violations_found":0,"privacy_warnings_found":3, exit_code:30,
 3x {"path":"tests/test_check_privacy.py","rule":"internal_identifier",
     "message":"internal identifier — internal hostname"}}
```

Same result on `knowledge-public-datadog` (also public tier, also `--strict`), same file,
same 3 warnings, same divergence from Python's `OK`.

**Root cause, isolated:** `tests/test_check_privacy.py:234` contains
`for sentinel in ("foo.internal.test", "bar.internal.example", "baz.internal.localhost"):` —
these three strings are check_privacy.py's own test fixtures for its
"reserved-sentinel-lookahead" exclusion (row 11 of the D8 report's own corpus table: a reserved
TLD/label genuinely at host-end, e.g. `.test`/`.example`/`.localhost`, must NOT be flagged as an
internal hostname). Python's `internal hostname` pattern
(`scripts/check_privacy.py:184-185`) appends `_RESERVED_SENTINEL_LOOKAHEAD`
to implement exactly this exclusion. Go's equivalent pattern
(`githooks@v0.5.0/privacy.go:109`, in the pinned dependency, untouched by D8):
```go
{regexp.MustCompile(`(?i)\b[a-z0-9][a-z0-9-]*\.(?:corp|internal|intranet|lan)\b`), "internal hostname"},
```
has **no reserved-sentinel-lookahead at all** — unlike Go's own `privateNetworkURL` pattern two
lines below, which does carry an equivalent `hostTerminator` guard. Confirmed in isolation:
```
$ printf 'foo.internal.test bar.internal.example baz.internal.localhost\n' > doc.md
$ git-tools scan privacy --repo . --privacy-tier public --strict
3x "internal identifier — internal hostname"   # Python: 0 (excluded by design)
```
This is a real, reproducible, pre-existing regex gap in the pinned `githooks` v0.5.0 module (Track
B/B1's code, not touched by D8's diff — confirmed via `git diff main -- internal/cli/scan.go`,
which only adds `codeMarkerExemptSuffixes`/`codeMarkerExemptRules` and does not touch
`privacy.go`). It predates D8. **But D8's own report explicitly re-verifies this exact rule**
("Rows 1-17 already had Go-side coverage from B1 ... this session's own reading confirmed each
rule's logic still matches, case by case, against privacy.go" — row 11 in the table is the
reserved-sentinel exclusion) **and claims its differential-corpus run found 0 findings on
marketplace (552 files) and knowledge-public-datadog (91 files) at public tier with `--strict`.**
Both of those specific claims are false as stated: the same file that is part of both repos'
real, tracked, in-scope corpus produces a real divergence under the report's own stated
methodology. Either the report's harness did not actually run `--strict` (contradicting its own
methodology section), didn't include `internal_identifier`-class findings in its "findings" count
(also contradicting a plain reading of "0 findings on both sides"), or the re-verification of row
11 against `privacy.go` was not actually cross-checked against a live run. Any of these three
means the differential-corpus proof, as reported, is not trustworthy on its own stated terms.

**Sample coverage confirmed across all seven repos** (required by task step 4): each of the seven
repos' `scripts/check_privacy.py` was confirmed present (`git -C <repo> ls-files
'*check_privacy.py'`); a real file carrying `privacy:`/`owner:` frontmatter tags was confirmed
present and scanned in every repo (e.g. `marketplace:.dat/README.md` —
`tags: [..., privacy:public, owner:public]`; `knowledge-private-datadog` — over 100 files tagged
`owner:datadog`/`privacy:internal`; `knowledge-private-personal` — `owner:personal`). Full
head-to-head runs were executed on marketplace (public/public, 552 files), workspace
(personal/private, 399 files, matches: 0/0 — internal-identifier posture is empty at private
tier, so the reserved-sentinel bug is inert there, consistent with report), and
knowledge-public-datadog (public/public, 91 files, mismatch reproduced). The confidential/datadog
tier repos (knowledge-private-datadog, ai-shared-lib-datadog, marketplace-datadog) use
`internalIDRelaxed`, which never runs the `internal hostname` pattern at all — so this specific
bug is inert at that tier too, and a spot-check there would not surface it; this is consistent
with, not contradictory to, the report's reported 0/0 for those three repos.

## 5. Code-suffix exemption — correct and adequately tested

`internal/cli/scan.go:40-53`, `codeMarkerExemptSuffixes` — exactly the 12 suffixes in
`scripts/check_privacy.py:57`'s `CODE_SUFFIXES` (`.py .go .sh .bash .rb .js .ts .java .rs .c .h
.cpp`), confirmed by direct diff of both lists. `codeMarkerExemptRules` renders each as an
always-on `**/*<suffix>` skip rule, prepended in `privacyMarkerExemptRules` ahead of any
repo-configured `privacy_marker_exempt`. Unit test
`TestCodeMarkerExemptRules_MatchesEveryCodeSuffixAnywhereInTree`
(`internal/cli/scan_test.go:68-97`) exercises match-at-any-depth and non-match on
suffix-as-substring/wrong-suffix cases. Integration test
`TestScanPrivacy_CodeSuffixIsMarkerExemptByDefault`
(`internal/cli/integration_test.go:759-777`) proves end-to-end that identical marker text is
exempt in `.py` and not exempt in `.md`, with an exact expected finding count. Both independently
reproduced live (see §4) against a fresh build. This part of D8 is correct, complete, and
adequately tested — the FAIL verdict is entirely about the differential-corpus verification
claim, not about this addition.

## 6. Employee-email opt-in scoping — accurately described

`githooks.EmployeeEmailCheck` (`privacy.go:123-164`): zero value (`Domains` empty) disables the
check entirely; a caller must supply its own `Domains` (and optionally `Allowlist`). Wired from
`git-tools.yaml`'s `employee_email_domains` (confirmed via `internal/cli/scan.go` config plumbing
around `cfg.EmployeeEmailDomains`). check_privacy.py hardcodes `datadoghq.com`/
`datadoghq.internal` (`scripts/check_privacy.py:200`) with no config. Confirmed neither
public-tier repo (marketplace, knowledge-public-datadog) currently sets
`employee_email_domains`, and neither has a real `@datadoghq.com` address outside
`check_privacy.py`'s own source/test fixtures (confirmed via `git grep`). The report's framing —
capability already shipped (B1), off by default by design (git-tools is org-agnostic), turning it
on is a config step for the later shim/soak stage, not a code gap in this migration — is accurate
and correctly scoped as a follow-up, not a defect.

## Summary of findings

| # | Item | Status |
|---|------|--------|
| 1 | Frontmatter `privacy:` tag validator (forbidden_marker, not_public_pair) | Present, live-verified. Audit's "absent" claim was a misreading of an unrelated comment. |
| 2 | Frontmatter `owner:` tag checks | Genuinely absent, by tested design decision (B1), pre-existing, inert on current corpus. Report should flag as residual risk but doesn't; not a task-failing defect. |
| 3 | build/vet/full test suite | Clean, reproduced independently. |
| 4 | Differential-corpus "0/0 across 1577 files" | **Not reproducible as stated.** Real, isolated, reproducible mismatch on marketplace and knowledge-public-datadog (both public tier, `--strict` — the report's own stated methodology) via `tests/test_check_privacy.py`'s reserved-sentinel-host fixture, caused by a missing reserved-sentinel-lookahead in the pinned `githooks` v0.5.0 `internal hostname` pattern. Pre-existing (Track B/B1), not introduced by D8's diff, but explicitly re-claimed as verified-matching by D8's own report. |
| 5 | Code-suffix marker exemption (D8's actual deliverable) | Correct, complete, tested, live-verified. |
| 6 | Employee-email opt-in scoping | Accurately described, correctly scoped as follow-up. |

## Recommendation

D8's own code change (item 5) should land as-is — it is correct. The report's differential-corpus
section needs correction: either re-run with a harness that actually reproduces `--strict`
end-to-end per-file (not just an aggregate finding count) and disclose the `tests/*.py`
divergence, or narrow the "0 findings" claim to the FAIL-class checks only (explicitly excluding
`internal_identifier`/warning-class results) if that is what was actually compared. The
underlying `privacy.go` reserved-sentinel-lookahead gap belongs to whoever owns
`githooks` v0.5.0 (Track B/B1), not to D8, and should be filed as its own issue — it is a latent
false-positive source for any public-tier repo whose tracked tree ever contains a
`.test`/`.example`/`.localhost`/`.invalid`-suffixed sentinel host string outside a
code-suffix-exempt file (e.g. a markdown doc discussing DNS/networking conventions), which would
break that repo's `--strict` pre-commit gate today, contrary to check_privacy.py's own designed
behavior.
