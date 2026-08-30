# Test verification: wire-email-allowlist, against real go/githooks v0.7.0

## Scope

This branch's only own commit relative to `main` is 316f06b, repinning
`go/githooks` from v0.6.1 to v0.7.0 in `go.mod`/`go.sum`. The config-wiring
source change itself (`internal/cli/config.go`, `internal/cli/scan.go`,
the rename from `employee_email_allowed_domains` to `allowed_email_domains`)
already landed on `main` in earlier commits (33598ce and ancestors) and is
therefore identical between `main` and `HEAD` — confirmed by an empty
`git diff main...HEAD -- internal/cli/config.go internal/cli/scan.go`. What
this task verifies is: does that already-merged wiring actually work, for
real, now that a real released dependency carrying `AllowedDomains` exists
(this is the first time it's been tested against anything but a local
`replace` directive)?

## 1. Diff review

```
git diff main...HEAD -- internal/cli/config.go internal/cli/scan.go
```
→ empty. `git diff main...HEAD --stat` shows only `go.mod` (+1/-1) and
`go.sum` (+2) changed by this branch. PASS — matches the stated scope
(repin only).

## 2. Old config key name eliminated

```
grep -rn "employee_email_allowed_domains|EmployeeEmailAllowedDomains|employee_email_domains|EmployeeEmailDomains|EmployeeEmailAllowlist|employee_email_allowlist" . | grep -v ".task-reports/"
```
→ zero hits. Current key everywhere is `allowed_email_domains` /
`AllowedEmailDomains` (`internal/cli/config.go:67`,
`internal/cli/scan.go:275`, `internal/cli/integration_test.go:660`). PASS.

## 3. Real CLI binary, real end-to-end scans

Build:
```
go build -o /tmp/git-tools-wired ./cmd/git-tools
```
→ succeeds (previously failed with the v0.6.1 pin per the original report:
`unknown field AllowedDomains in struct literal of type
githooks.EmployeeEmailCheck`). PASS.

### 3a. Configured domain exempted, other domain flagged

Scratch dir `/tmp/git-tools-e2e-scratch` (untracked, no `.git`, outside any
tracked repo — plain directory scan target):

`git-tools.yaml`:
```yaml
allowed_email_domains:
  - acme-corp.example
```
`allowed.txt`: `Contact jane@acme-corp.example for internal matters.`
`notallowed.txt`: `Contact bob@other-corp.io for external matters.`

```
/tmp/git-tools-wired scan privacy --privacy-tier public \
  --repo /tmp/git-tools-e2e-scratch --strict \
  --config /tmp/git-tools-e2e-scratch/git-tools.yaml
```
Output (exit 30):
```json
{"command":["git-tools","scan","privacy"],
 "data":{"privacy_violations_found":0,"privacy_warnings_found":1,...},
 "errors":[{"code":"precondition_unmet.privacy_violation",
   "context":{"path":"notallowed.txt","rule":"internal_identifier"},
   "message":"internal identifier — internal employee email", ...}],
 "exit_code":30,"status":"precondition_unmet"}
```
Only `notallowed.txt` (bob@other-corp.io) is flagged; `allowed.txt`
(jane@acme-corp.example, the configured domain) does not appear. PASS —
matches acceptance exactly: configured domain exempted, other domain
flagged, `--strict` turns the warning into a failing exit code.

### 3b. Default with no `allowed_email_domains` key: only example.com exempt

Scratch dir `/tmp/git-tools-e2e-scratch-noconfig`, `git-tools.yaml: {}`
(no `allowed_email_domains` key at all).

- `acme.txt` (`jane@acme-corp.example`) present, no `example.txt`:
  ```
  /tmp/git-tools-wired scan privacy --privacy-tier public \
    --repo /tmp/git-tools-e2e-scratch-noconfig --strict
  ```
  → exit 30, one violation on `acme.txt`, rule `internal_identifier`.
  Confirms `acme-corp.example` is NOT exempt by default (it only becomes
  exempt when named in `allowed_email_domains`).

- Same config, `acme.txt` removed, only `example.txt`
  (`jane@example.com`) present:
  ```
  /tmp/git-tools-wired scan privacy --privacy-tier public \
    --repo /tmp/git-tools-e2e-scratch-noconfig --strict
  ```
  → exit 0, `{"privacy_violations_found":0,"privacy_warnings_found":0,...,
  "status":"success"}`. Confirms `example.com` (githooks' own hardcoded
  default) stays exempt with zero config. PASS.

Scratch directories removed after verification
(`rm -rf /tmp/git-tools-e2e-scratch /tmp/git-tools-e2e-scratch-noconfig`).

## 4. Existing integration tests, against the real dependency

```
go test ./internal/cli/... -run "TestScanPrivacy_.*Email.*" -v -count=1
```
```
=== RUN   TestScanPrivacy_InternalEmailWarnsWithoutStrict
--- PASS: TestScanPrivacy_InternalEmailWarnsWithoutStrict (0.58s)
=== RUN   TestScanPrivacy_EmployeeEmailCheckFlagsAnyDomainWithoutConfig
--- PASS: TestScanPrivacy_EmployeeEmailCheckFlagsAnyDomainWithoutConfig (0.05s)
=== RUN   TestScanPrivacy_EmployeeEmailCheckAllowsExampleDomainWithoutConfig
--- PASS: TestScanPrivacy_EmployeeEmailCheckAllowsExampleDomainWithoutConfig (0.05s)
PASS
ok  	github.com/johnrichter/git-tools/internal/cli	0.687s
```
All three pass against `go/githooks v0.7.0` with no `replace` directive —
first time this has run against a real release. PASS.

## 5. Full suite fresh

```
gofmt -l .        → (no output, clean)
go build ./...    → ok (exit 0)
go vet ./...      → ok (exit 0)
go test ./... -count=1
  internal/cli            ok  23.400s   (baseline ~24s — consistent)
  internal/commitmsg      ok  0.152s
  internal/gitexec        ok  0.984s
  internal/hooks          ok  0.047s
  internal/result         ok  0.005s
  internal/signing        ok  0.221s
  internal/worktreeclean  ok  0.764s
  worktree-gate/detect    ok  0.651s
  worktree-gate/fixtures  ok  0.003s
  worktree-gate/lifecycle ok  4.734s
```
Full `-v` run: wall time 23.744s total, no FAIL anywhere. PASS — no
dramatic deviation from baseline.

## 6. go.sum / go.mod hygiene

```
go mod verify        → "all modules verified"
grep -n "^replace" go.mod   → no output (no replace directive)
```
`go.mod`/`go.sum` diff vs `main` is exactly the githooks v0.6.1 → v0.7.0
bump (require line + two go.sum hash lines), nothing else. PASS — no
stray local-replace scaffolding survived into the commit.

## Verdict

All six checks PASS. The wiring that was previously verified only against
a temporary local `go.mod` replace directive now builds, passes its
integration tests, and behaves correctly end-to-end (via the real built
binary against real scan content) against the real, released
`go/githooks v0.7.0` dependency, with no leftover scaffolding.

**PASS**
