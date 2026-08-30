# Quality review: consume `go/githooks` v0.6.0

Repo `git-tools`, branch `chore/consume-githooks-v0.6.0`, commit `3d30a2d`,
reviewed 2026-08-30. No frontmatter, matching every other report in
`.task-reports/` — these are ephemeral task artifacts, not KB pages, and the
repo schema's `context` class does not apply to them.

## Verdict

**ACCEPT.** No code changed by this review. The only file this review adds is
this report, under the dispatch's own output contract.

A two-line dependency bump consuming an upstream tag whose entire delta is one
exemption-table generalization plus its tests. No API change, no consumer-side
code change required, full suite green, and four independent live smoke cases
confirm both the new exemption and the non-weakening of detection.

## Scope of review

Per dispatch, this is the **consumption bump in `git-tools` only**. The
upstream change in `ai-shared-lib`'s `go/githooks` module was already
implemented, tested, and quality-reviewed before the v0.6.0 tag was cut; this
review does not re-adjudicate it. Upstream source is read here only to confirm
the tag contains what the bump claims and carries no API break.

## Check 1 — diff touches only `go.mod` and `go.sum`

`git diff --stat main...HEAD`:

```
 go.mod | 2 +-
 go.sum | 4 ++--
 2 files changed, 3 insertions(+), 3 deletions(-)
```

The full diff is the version bump and its two matching `go.sum` lines:

```
-	github.com/johnrichter/claude-shared-tooling/go/githooks v0.5.0
+	github.com/johnrichter/claude-shared-tooling/go/githooks v0.6.0
```

```
-github.com/johnrichter/claude-shared-tooling/go/githooks v0.5.0 h1:lV4nwy/JoPMXMXu8ukaoBSWqREu4gRboG1pLVt/woD4=
-github.com/johnrichter/claude-shared-tooling/go/githooks v0.5.0/go.mod h1:X4ewQa0z9RS1WnPi8Vw252irMmfT8NDDeOTQ5Ur0nhY=
+github.com/johnrichter/claude-shared-tooling/go/githooks v0.6.0 h1:0jNK+O8TXju5fNii5lKwDijTZS72rApQngVO5gjwKvQ=
+github.com/johnrichter/claude-shared-tooling/go/githooks v0.6.0/go.mod h1:X4ewQa0z9RS1WnPi8Vw252irMmfT8NDDeOTQ5Ur0nhY=
```

Two observations, both confirming rather than qualifying the bump:

- The `/go.mod` hash is **unchanged** across the two tags
  (`h1:X4ewQa0z9RS1WnPi8Vw252irMmfT8NDDeOTQ5Ur0nhY=`). The module's own
  `go.mod` did not change, so v0.6.0 adds no transitive dependency and shifts
  no minimum versions. Correct and expected for a pure-source patch.
- No other pinned module moved. `git`, `clikit`, `fsx`, `sysops`, `logkit` and
  all third-party pins are untouched, so this is not a `go mod tidy` sweep
  wearing a bump's label.

`go mod verify` -> `all modules verified` (exit 0), so the downloaded v0.6.0
content matches the recorded hash.

Working tree is clean; no stray artifacts committed on the branch.

### Tag content confirmed, no API break

`diff -rq` between the two module-cache trees reports exactly two files
differing, and nothing else:

```
Files .../githooks@v0.5.0/sanity_test.go and .../githooks@v0.6.0/sanity_test.go differ
Files .../githooks@v0.5.0/secrets.go   and .../githooks@v0.6.0/secrets.go   differ
```

The `secrets.go` delta replaces a single-pattern special case with a
label-keyed exemption table:

```go
// v0.5.0
if p.label != labelAWSAccessKeyID {
	return p.re.MatchString(text)
}
...
if !awsExampleAccessKeyIDs[match] {

// v0.6.0
exempt, ok := exactExemptions[p.label]
if !ok {
	return p.re.MatchString(text)
}
...
if !exempt[match] {
```

`matchesSecretPattern`, `labelSlackToken`, `slackExampleTokens` and
`exactExemptions` are all unexported. The exported surface `ScanSecrets`
consumes — signature, `Finding.Rule` string values, envelope shape — is
unchanged. That is why the bump needs no consumer-side code change, and it is
the right reason: not "nothing broke by luck", but "no exported contract
moved".

Both exempted values remain fragment-assembled in the upstream source
(`"xoxb-ab59" + "EXAMPLETOKEN"`), so the module's own source cannot trip a
pre-fix scanner scanning it.

## Check 2 — `go build ./...` and `go vet ./...` clean

Re-run with true exit codes captured directly, not through a pipe:

```
BUILD_EXIT=0
VET_EXIT=0
MODVERIFY_EXIT=0   # "all modules verified"
```

No diagnostics on stdout or stderr from either tool.

## Check 3 — full test suite green

Own run on this worktree at commit `3d30a2d`, Go 1.27.0 linux/arm64:

`go test ./... -count=1 -timeout 30m`

```
?   	github.com/johnrichter/git-tools/cmd/git-tools	[no test files]
ok  	github.com/johnrichter/git-tools/internal/cli	1206.178s
ok  	github.com/johnrichter/git-tools/internal/commitmsg	0.195s
ok  	github.com/johnrichter/git-tools/internal/gitexec	106.442s
ok  	github.com/johnrichter/git-tools/internal/hooks	0.105s
ok  	github.com/johnrichter/git-tools/internal/result	0.006s
ok  	github.com/johnrichter/git-tools/internal/signing	17.569s
ok  	github.com/johnrichter/git-tools/internal/worktreeclean	41.621s
ok  	github.com/johnrichter/git-tools/worktree-gate/detect	6.558s
?   	github.com/johnrichter/git-tools/worktree-gate/detect/cmd/worktree-gate	[no test files]
ok  	github.com/johnrichter/git-tools/worktree-gate/fixtures	0.006s
ok  	github.com/johnrichter/git-tools/worktree-gate/lifecycle	147.747s
TEST_EXIT=0
```

All 9 test-bearing packages pass. No skips reported, no failures, no timeout.
Wall time ~1526s (~25 min), dominated by `internal/cli` at ~20 min as the
dispatch anticipated. Log retained at
`/tmp/qr-githooks-v060-full-test.log`.

## Check 4 — independent live smoke test

Built a throwaway binary from this worktree (`go build -o /tmp/qr-smoke-v060
./cmd/...`, exit 0) and ran `scan secrets` against four throwaway
`git init`'d fixture trees. Run separately from and after the orchestrator's
own verification. Both Slack fixture values are fragment-assembled at fixture
creation so no whole token literal is ever written to a reviewed file.

Token-shaped values are written below in fragment-assembled form
(`"prefix" + "suffix"`) for the same reason the upstream module fragments its
own allowlist literals: this report is itself a tracked, scanned file, and a
whole literal here would either trip a scanner that predates the exemption or,
for case B's value, trip the exemption-independent Slack rule and refuse the
commit that ships this review.

| Case | Fixture content | Expected | Observed | Exit |
|---|---|---|---|---|
| A | documented placeholder `"xoxb-ab59" + "EXAMPLETOKEN"` in `doc.md` | 0 findings | `secrets_found:0`, `status:success` | 0 |
| B | different real-shaped `"xoxb-ab59" + "QRSMOKE7T3KXZ"` in `leak.txt` | flagged | `secrets_found:1`, `slack_token`, `precondition_unmet.secret_detected` | 30 |
| C | pre-existing AWS placeholder `"AKIAIOSFODNN7" + "EXAMPLE"` | 0 findings | `secrets_found:0`, `status:success` | 0 |
| D | placeholder + appended chars, `"...EXAMPLETOKEN" + "DEADBEEF"` | flagged | `secrets_found:1`, `slack_token`, `precondition_unmet.secret_detected` | 30 |

Cases A and B are the dispatch's required pair. Cases C and D are two extra
cheap cases this review added because they are the actual risk surface of this
particular refactor, and neither costs more than one command:

- **C** is the regression surface of the generalization itself. Replacing the
  `p.label != labelAWSAccessKeyID` guard with a map lookup could have dropped
  the pre-existing AWS exemption if the table were miskeyed. It did not.
- **D** confirms the new exemption is exact-match, not a substring or
  prefix weakening. The Slack regex has no trailing `\b`, so a longer token
  beginning with the placeholder must still flag; greedy matching consumes the
  whole token run, so the compared match is not the exempt string. Confirmed
  live at the CLI, not only in the upstream unit test.

Case B's flag also confirms the finding still carries the full triage envelope
(`context.path`, `context.rule`, `triage.instruction`), so downstream harnesses
keying on those fields see no change.

Throwaway binary and all four fixture directories deleted; absence confirmed by
`ls` returning `No such file or directory` for both paths.

## Check 5 — verdict and findings

**ACCEPT.** No findings at any severity.

- **Blocking:** none.
- **Major:** none.
- **Minor:** none.

Reviewed against the standing lenses, proportionate to a two-line bump:

- **Correctness** — bump is exactly as described; upstream delta is confined to
  two files with no exported-surface change; behavior confirmed live in both
  the permit and the deny direction.
- **Security** — this is a detection *exemption*, so the security question is
  whether it widens a hole. It does not. The exemption is a single exact string
  in a map, and cases B and D confirm neither a same-shape different token nor
  a superstring of the placeholder escapes detection. The exempted value is a
  third-party detection tool's own published rule-definition example, confirmed
  by the operator as not a real credential.
- **Design** — the label-keyed `exactExemptions` table is the right
  generalization: it removes a special case rather than adding a second one,
  and the next pattern needing an exemption adds a map entry, not a branch.
- **Idiom / maintainability** — upstream concern, already reviewed; nothing in
  `git-tools` needed to adapt.
- **Acceptance** — the bump consumes v0.6.0 and the new exemption is live in a
  binary built from this branch.
- **Wiring** — no new artifact, producer, consumer, or shared-directory
  enumerator is introduced, so this lens is not engaged. The consumption path
  is verified end-to-end regardless: the exemption reaches the CLI's JSON
  envelope, proven by case A returning `secrets_found:0` through a real binary
  rather than a unit test.

## Test-suite assessment

Adequate, with the substantive coverage correctly placed upstream where the
logic lives. v0.6.0's `sanity_test.go` adds three cases — placeholder exempt,
same-shape different token still flagged, placeholder-plus-suffix still flagged
— and retains the AWS near-miss case. That is the right adversarial set for an
exact-match exemption.

`git-tools` adds no test of its own, which is correct for this bump: a consumer
test asserting an upstream allowlist's contents would duplicate the upstream
suite and pin `git-tools` to a table it does not own. The consumer-side
guarantee that matters is "the bump builds, the suite stays green, and the
behavior reaches the CLI envelope", and that is covered by checks 2-4. No gap
for the test-engineer to close.

## Residual risk

Low, and none of it introduced by this bump.

- The exemption is only as sound as the claim that the exempted Slack value is
  a published documentation example rather than a credential. That claim was
  operator-confirmed upstream and is out of scope here; this review verified
  only that the exemption is exact and non-widening.
- Every exact-match allowlist entry is, by construction, a value the scanner
  will never flag anywhere in any consumer tree. The blast radius is one string,
  bounded and auditable in source.
- Pre-existing divergences between `git-tools`' Go privacy scan and the legacy
  `check_privacy.py` are documented in
  `.task-reports/d8-privacy-scan-migration-quality-review.md` and are unrelated
  to and unchanged by this bump.

## Plan feedback

- No spec or plan correction needed. The task was correctly scoped as a
  consumption bump and correctly excluded re-review of the upstream change.
- One note for release sequencing, not a defect: this bump is behaviorally
  inert for `git-tools`' own tracked tree, whose source and config carry no
  `xox`-prefixed value. Its value is for consumers whose scanned corpora ingest
  the third-party tool's rule-definition file. Worth stating in the CHANGELOG
  entry so a reader does not expect a visible change when scanning `git-tools`
  itself.
