# Wire git-tools' employee-email config to the new allow-list shape

## Task

Second, separate piece of work following ai-shared-lib's inversion of
`go/githooks`'s employee-email check from deny-list to allow-list polarity
(see that repo's `.task-reports/invert-email-domain-check-report.md`).
git-tools' own `git-tools.yaml` config schema wired the old
`employee_email_domains` and `employee_email_allowlist` keys into
`githooks.EmployeeEmailCheck{Domains, Allowlist}`. Update that wiring to the
new `AllowedDomains` shape.

## What changed

- `internal/cli/config.go`: `Config.EmployeeEmailDomains` and
  `Config.EmployeeEmailAllowlist` replaced by one field,
  `EmployeeEmailAllowedDomains []string` (`koanf:"employee_email_allowed_domains"`).
  Updated `defaultConfig()`'s seed map to match (one key instead of two, both
  defaulting to an empty slice). Doc comment rewritten: the check now always
  runs at the public tier. The config key only widens which domains it
  exempts beyond `githooks`' own hardcoded `example.com` default.
- `internal/cli/scan.go`: `employeeEmailCheck(cfg)` now just constructs
  `githooks.EmployeeEmailCheck{AllowedDomains: cfg.EmployeeEmailAllowedDomains}`.
  The old address-level `Allowlist`-building loop is gone, since that concept
  no longer exists on the githooks side.
- `internal/cli/integration_test.go`: replaced the two employee-email
  integration tests pinned to the old deny-list and off-by-default
  semantics:
  - `TestScanPrivacy_InternalEmailWarnsWithoutStrict` now configures
    `employee_email_allowed_domains` (a placeholder domain) and confirms an
    address at that domain does not warn while an address at another domain
    does (and fails `--strict`).
  - `TestScanPrivacy_EmployeeEmailCheckOffWithoutConfig` (old: proved the
    check was off by default) replaced with two tests:
    `TestScanPrivacy_EmployeeEmailCheckFlagsAnyDomainWithoutConfig` (an
    address at an unconfigured, non-example.com domain now flags by
    default) and
    `TestScanPrivacy_EmployeeEmailCheckAllowsExampleDomainWithoutConfig`
    (example.com stays exempt with no config at all, proving githooks'
    hardcoded default reaches this CLI's wiring unmodified).

## Acceptance

- Config schema updated to the new allow-list shape, replacing the old
  deny-list key(s) — met.
- Every internal reference to the old field names updated — met (verified
  via repo-wide grep after the change. Only historical `.task-reports/*`
  from a prior, unrelated task still mention the old key names, left alone
  as historical record).
- Tests updated to the new polarity, not left stale alongside new ones —
  met.

## Sanity result

Verified using a temporary local `go.mod` `replace` directive pointing at
the ai-shared-lib worktree carrying the corresponding `go/githooks` change
(removed before finalizing. Not part of the committed diff, see below):

```
gofmt -l .              → internal/cli/config.go only (fixed, now clean)
go build ./...          → ok
go vet ./...            → ok
go test ./... -count=1  → ok (all packages pass, including both new and
                            updated employee-email integration tests)
```

With the temporary `replace` removed (git-tools' `go.mod` restored to its
committed, pinned `go/githooks v0.6.1`), `go build ./...` fails as expected:

```
internal/cli/scan.go:275:37: unknown field AllowedDomains in struct literal of type githooks.EmployeeEmailCheck
```

This is expected and not a defect in this change. git-tools' `go.mod` pins a
released version of `go/githooks` that predates the field rename. This
commit's wiring is correct against the new API shape, but the repo will not
build until `go/githooks` ships a release carrying that rename and this
repo's `go.mod` is repinned to it.

## Assumptions & deviations

- Used a temporary local `replace` directive only to verify the wiring
  compiles and its tests pass. It is not part of the committed change. The
  committed `go.mod` and `go.sum` are unchanged from `main` (still pinned to
  `go/githooks v0.6.1`). Bumping that pin is out of this task's scope and
  belongs to a follow-up repin task, matching this repo's own established
  pattern for repinning its dependencies after an upstream release (see
  recent history, e.g. "repin git-tools v0.9.0 to v0.10.0").
- Chose `acme-corp.example` as the integration tests' placeholder allow-list
  domain (an RFC 2606 reserved second-level-style placeholder, following the
  existing tests' own convention of never naming a real organization's
  domain in this public-source repo's test corpus).

## Hand-off notes

- **This change cannot land usefully on its own**. It needs `go/githooks` to
  ship a release with the `AllowedDomains` rename, and a follow-up commit in
  this repo bumping the `go/githooks` require line in `go.mod` and `go.sum`
  to that release, before `go build` and `go test` will pass without a local
  `replace`. Track that repin as the next step. This commit is deliberately
  scoped to just the config-wiring source change, per the task's own
  instruction to keep it "clearly separated."
- Quality-reviewer: confirm the choice to fold the old address-level
  `EmployeeEmailAllowlist` config key away entirely (rather than keep it as
  dead config, or map it onto some other exemption) is intended. It has no
  githooks-side counterpart anymore under the new allow-list design.
