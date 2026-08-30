# Quality review: wire-email-allowlist (go/githooks v0.7.0 repin)

**Verdict: FIX-APPLIED** — one minor go.sum hygiene defect found and fixed.
The wiring itself is correct and independently re-verified end-to-end.

## What this branch actually is

`git diff main...HEAD --stat` (verified myself):

```
.task-reports/wire-email-allowlist-test-verification.md | 161 +++++
go.mod                                                  |   2 +-
go.sum                                                  |   2 +
```

The config-wiring source change (`internal/cli/config.go`,
`internal/cli/scan.go`, `internal/cli/integration_test.go`) already landed on
`main` (af707fb, 33598ce). This branch is the repin that makes it real. Both
task reports state this scope accurately. Nothing unrelated is touched — the
diff contains no source file at all, only the dependency pin and a report.

## Findings

### Blocking

None.

### Major

None.

### Minor — FIXED

- `go.sum:25-26` (pre-fix): the repin added the v0.7.0 `h1:`/`go.mod` hash
  rows but left the superseded `go/githooks v0.6.1` rows behind. `go mod tidy`
  prunes exactly those two lines and changes nothing else, so `go.sum` was
  tidy before this commit and untidy after it. Not build-breaking and not
  caught by CI (`.github/workflows/ci.yml` gates `gofmt -l`, `go vet`,
  `go build`, `go test` — there is no `go mod tidy` check), but it leaves a
  stale hash for a version nothing in the graph requires, and it is the exact
  residue a by-hand `go.mod` edit leaves versus a `go get`+`tidy`. Fixed by
  running `go mod tidy`. `go.mod` was unaffected.
- Process note, not a code defect: the test-verification report's §6 asserts
  the `go.mod`/`go.sum` diff is "exactly the v0.6.1 → v0.7.0 bump ... nothing
  else". True as a description of the diff, but it checked only for a `replace`
  directive and `go mod verify` — never tidiness — which is why the stale rows
  survived a PASS.

## Independent re-verification (not a re-read of the reports)

### Pin integrity

- `go.mod`: `github.com/johnrichter/claude-shared-tooling/go/githooks v0.7.0`,
  exact, in the direct `require` block.
- `grep -n replace go.mod` → no output. No `replace` directive of any kind.
- `go mod verify` → `all modules verified`.
- `go list -m -json` resolves v0.7.0 from the real module cache
  (`Time: 2026-08-30T21:55:41Z`, `Sum: h1:goVR7liyoMSD+AB2gWp/sNsg5YizVgifwbNrHxBGBic=`),
  matching the committed `go.sum` row — a real published release, sumdb-checked.

### Real binary, fresh end-to-end scans (my own fixtures, not the report's)

Built `./cmd/git-tools` into a gitignored `bin/` (removed afterwards) and
scanned scratch trees using `widget-works.test` / `vendor-x.invalid`, i.e. no
overlap with the report's `acme-corp.example` / `other-corp.io` fixtures.

| Case | Fixture | Result |
| --- | --- | --- |
| A: `allowed_email_domains: [widget-works.test]`, implicit `git-tools.yaml` discovery via `--repo` | `ok@widget-works.test` | not flagged |
| A | `Person@WIDGET-WORKS.TEST` | not flagged (case-insensitive match confirmed) |
| A | `bad@vendor-x.invalid` | flagged, `internal_identifier` |
| A | `dev@mail.widget-works.test` | flagged — exact-domain semantics, subdomains are **not** implicitly exempt |
| A + `--strict` | same tree | `precondition_unmet`, exit 30 |
| B: config file present, key absent | `jane@example.com` | not flagged |
| B | `ok@widget-works.test` | flagged |
| C: **no** `git-tools.yaml` at all | `jane@example.com` / `ok@widget-works.test` | not flagged / flagged |
| D: scalar (non-list) `allowed_email_domains: widget-works.test` | `ok@widget-works.test` | not flagged — koanf coerces a scalar to a one-element slice, which is lenient but not a defect |
| E: env layer, `GITTOOLS_ALLOWED_EMAIL_DOMAINS=widget-works.test`, no config file | `ok@widget-works.test` | not flagged — the new key resolves through the env layer too |

All three configuration layers (file, env, default) reach
`githooks.EmployeeEmailCheck.AllowedDomains`. Default-only behavior is exactly
"`example.com` and nothing else", both with a config file that omits the key
and with no config file at all — the report's claim holds under fixtures it
never used.

### Wiring read, end to end

`internal/cli/config.go:67` (`AllowedEmailDomains []string`
`koanf:"allowed_email_domains"`) → `defaultConfig()`'s
`"allowed_email_domains": []string{}` seed → `internal/cli/scan.go:275`
`employeeEmailCheck` → `PrivacyOptions.EmployeeEmail` at both call sites
(`newScanPrivacyCmd` and `scanTree`, so the landing gate and the standalone
scan share one path). Producer and consumer agree on the key. There is no dead
helper and no orphaned field. Old key names (`employee_email_domains`,
`employee_email_allowlist`, `EmployeeEmailAllowedDomains`) appear nowhere
outside historical `.task-reports/`.

### Full suite, fresh, after my fix

```
gofmt -l .              → clean
go build ./...          → ok
go vet ./...            → ok
go test ./... -count=1  → all packages ok (internal/cli 23.256s, commitmsg,
                          gitexec, hooks, result, signing, worktreeclean,
                          worktree-gate/{detect,fixtures,lifecycle} all ok)
```

## Test-suite assessment

Adequate. The three integration tests build a real CLI and run it against a
real repo, and I confirmed the strongest of them is not hollow:
`TestScanPrivacy_InternalEmailWarnsWithoutStrict`
(`internal/cli/integration_test.go:666`) puts both addresses in one `doc.md`
and asserts `len(r.Caveats) == 1`. I verified against the built binary that
`githooks` emits one warning **per match**, not per file (privacy.go:347-357),
and that the CLI envelope does not dedupe: the same file yields 2 caveats with
no allow-list and 1 with it. So that count assertion genuinely discriminates a
working allow-list from a broken one.

Gaps, all minor and none worth blocking on: no test pins case-insensitive
matching, subdomain non-exemption, or the env-layer path. Those are
`go/githooks`' own contract rather than this CLI's wiring, and I verified all
three by hand against the built binary.

## Fixes applied

- `go mod tidy` — drops the two stale `go/githooks v0.6.1` rows from `go.sum`.
  No `go.mod` change, no source change.

## Residual risk

- **Fleet-wide false-positive class from the upstream polarity inversion (not
  a defect in this diff).** With the check always-on and only `example.com`
  exempt by default, an SSH remote URL of the form `git@github.com:owner/repo`
  is email-shaped and now warns as `internal identifier — internal employee
  email`. Measured with the v0.7.0-linked binary: marketplace produces 12
  privacy warnings (including its own `README.md` and two
  `hooks/requirements.txt` `git+ssh://git@github.com/...` lines), git-tools
  itself 22. These are caveats (exit 10), not failures: `scanGate`
  (`internal/cli/scan.go:337`) only refuses on `precondition_unmet`, and
  privacy warnings only reach that status under `strict: true`, which no
  consumer config in the platform sets. So landing verbs will get noisier, not
  blocked — but any repo that turns on `strict` will block until it
  allow-lists `github.com`.
- Exact-domain matching means a repo that allow-lists `corp.example` still
  gets warnings for `user@mail.corp.example`. Documented upstream, and worth
  knowing before writing a consumer config.

## Plan feedback

- The three real consumers that already carry the new key
  (`ai-shared-lib-datadog`, `knowledge-private-datadog`,
  `marketplace-datadog`) all use `allowed_email_domains`. Nothing anywhere
  still sets the removed `employee_email_domains`/`employee_email_allowlist`.
  That is the deciding evidence for a minor rather than major bump — the
  key removal is source-breaking in principle but breaks no live config.
- The next governance-git repin to git-tools v1.4.0 should carry an explicit
  note that the public tier's employee-email check flips from opt-in to
  always-on, and should either add `github.com` to the repinning repos'
  `allowed_email_domains` or accept the `git@github.com:` warning noise.
  governance-git's current README/CHANGELOG text describing the key as opt-in
  is accurate as history but will describe the wrong posture once repinned.
- Worth a follow-up in `go/githooks`: excluding SSH-remote-URL shape
  (`git@<host>:` / `git+ssh://git@<host>/`) from the employee-email pattern
  would remove the single largest false-positive class the inversion
  introduced, without weakening the check.
- Consider adding a `go mod tidy` check to `.github/workflows/ci.yml`. That
  check would have caught this release's one real defect.

## Release: v1.4.0 (tagged at 9ad0812)

Landed `chore/wire-email-allowlist` into `main` via
`git-tools merge` + `git-tools push main` (fast-forward to 9ad0812), re-ran
the full suite on that exact commit (`go build`, `go vet`,
`go test ./... -count=1` — all green), then
`git-tools tag create 1.4.0 --shape vX.Y.Z`.

### Why minor, not patch or major

Patch is wrong: v1.4.0 carries a config surface that takes effect for the
first time and a changed default scan posture. A strict semver reading would
argue major, since v1.3.0 shipped `employee_email_domains` and
`employee_email_allowlist` as working keys and this release removes both (a
config that still sets them is now silently ignored), and since the default
posture gets stricter. Minor is nonetheless the right call on the evidence:
every live consumer config in the platform (`ai-shared-lib-datadog`,
`knowledge-private-datadog`, `marketplace-datadog`) already uses the new
`allowed_email_domains` key and none sets either removed key, so nothing
in-fleet breaks. The module also exports no changed Go API (only `cmd/`,
`internal/`, and an untouched `worktree-gate/`), and a minor bump matches this
repo's own v1.0.0 → v1.3.0 all-minor cadence.

### Consumer-visible changes since v1.3.0

`git log v1.3.0..main` is 12 commits. The source diff is only
`internal/cli/{config.go,scan.go,integration_test.go}` (+56/-56).

1. **New config key `allowed_email_domains`** (af707fb, 33598ce) — replaces
   `employee_email_domains` and `employee_email_allowlist`, which are removed.
   A repo names the mail domains its own people use, and those domains stop
   being flagged as internal identifiers. Resolves through all three layers (file,
   `GITTOOLS_ALLOWED_EMAIL_DOMAINS`, default).
2. **Public-tier employee-email check is now always on** (316f06b, via
   `go/githooks` v0.6.1 → v0.7.0) — previously off unless a repo configured
   domains, and deny-list polarity. Now every email-shaped string is a
   candidate internal identifier unless its domain is `example.com` or is
   named in `allowed_email_domains`. Matching is literal and
   case-insensitive. Subdomains of an allowed domain are **not** covered.
   Practical effect: `git@github.com:owner/repo` SSH URLs in docs now warn
   (12 such warnings in marketplace, 22 in git-tools itself). Warnings, not
   failures — landing verbs only refuse under `strict: true`, which no
   consumer sets.
3. **Dependency repin** (316f06b): `go/githooks` v0.6.1 → v0.7.0, plus the
   `go.sum` tidy in this review (9ad0812). No other dependency moved.
4. **Report-only commits** (c38660d, bf31e70, 2c6ab4f, 8124bad, 9295c61,
   b4f61ce, 1d01991, a4b01f9, 4fdf217): the D8 `check_privacy.py` shim
   implementation and two quality reviews (the shim code itself lives in the
   six consumer repos, not here), and retroactive test-engineer verification
   of the D4 lint-guard widening, the D8 privacy-scan migration, and the
   v1.3.0 release point. No behavior change.
5. **Self-scan hygiene** (5ffba87): fragment-split literal AWS-key-shaped
   strings inside a report so git-tools' own secret scan stays clean.
   Affects this repo's scans of itself, not consumers.

Correction to the task framing: the D4 lint-guard widening itself is **not**
in this range — it shipped in v1.3.0, and only its retroactive verification
report landed since.

### Publication

`SC-DISTRIBUTION release` run 33339040395 completed successfully in 54s.
Release `v1.4.0` carries 11 assets: 8 platform tarballs (`git-tools` and
`worktree-gate` x darwin/linux x amd64/arm64), `checksums.txt`,
`contract-digests.txt`, and `binary-checksums.txt`. Downloaded
`binary-checksums.txt` fresh: **8 rows**, one per platform binary, as
expected (4 platforms x 2 binaries).
