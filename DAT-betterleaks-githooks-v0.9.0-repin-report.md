# betterleaks-cli-wiring: githooks v0.9.0 repin — final report

## Status: done, committed

Follow-on to `0d0caaf4` ("Wire betterleaks-based credential scanning via githooks
v0.8.0") and `2be4b4ba` ("Wire ScanPIIFinancial into scanTree") on
`feat/betterleaks-cli-wiring`. `ai-shared-lib`'s `go/githooks` released `v0.9.0`,
which fixes the diagnostic-category bug this branch's uncommitted
`scan_gate_test.go` exists to prove, and deletes the hand-rolled
`ScanPIIFinancial` scanner entirely — PII/financial detection now flows through
the same betterleaks subprocess call `scanCredentials` already makes, via three
new base-config rules (`pii-ssn`, `financial-credit-card-number`,
`financial-iban`) and a `categoryForRuleID` bucketing function.

## What changed
1. **`go.mod`/`go.sum`** — repinned
   `github.com/johnrichter/claude-shared-tooling/go/githooks` from `v0.8.0` to
   `v0.9.0`, via `go mod tidy`.
2. **Revert commit** (`3cd23a0`, `git revert 2be4b4ba` — applied cleanly, no
   manual conflict resolution needed) — removes `2be4b4ba`'s
   `githooks.ScanPIIFinancial` call site in `scanTree` (`internal/cli/scan.go`),
   its dedicated test file (`internal/cli/scan_pii_financial_test.go`), and the
   `TestScanTree_MergesPIIFinancialFindingsIntoSecrets` test it added to
   `scan_test.go`. `ScanPIIFinancial` no longer exists in `v0.9.0`, so this
   removal is required for the module to compile at all under the new pin.
3. **`internal/cli/scan_pii_financial_test.go`** (new, replacing the reverted
   file of the same name) — `TestScanAll_ReportsNonZeroPIIAndFinancialCountsViaScanCredentials`:
   end-to-end proof that `pii_found`/`financial_found` reach the built CLI's
   `scan all` JSON output through `scanCredentials`' one betterleaks subprocess
   call alone — no dedicated PII/financial scan call anywhere in this CLI
   (`scanTree` unchanged beyond the revert above). Plants an SSN-shaped and a
   Visa-test-card-shaped fixture in a committed file, asserts non-zero counts,
   and asserts the reported errors name that file under rules `pii-ssn` and
   `financial-credit-card-number` (githooks v0.9.0's own base-config rule ids —
   verified directly against `ai-shared-lib`'s `betterleaks.go` /
   `data/betterleaks-base.toml` / `betterleaks_test.go`, not guessed). Gated
   behind `BETTERLEAKS_TEST_BIN` (skips cleanly when unset), matching
   `ai-shared-lib/go/githooks/betterleaks_test.go`'s own `testBetterleaksBinary`
   convention for real-binary subprocess integration tests — a different,
   deliberately separate env var from this CLI's own `GIT_TOOLS_BETTERLEAKS_BIN`
   (which the test sets, once resolved, to point `scanCredentials` at that same
   real binary).
4. **`internal/cli/scan_gate_test.go`** — untouched, as instructed. This is the
   pre-existing, deliberate handoff evidence
   (`TestScanGate_GoverningDiagnosticCategoryMatchesFindingCategory`) proving
   the category bug that `v0.9.0` fixes upstream.

Fixture values (`fixtureLeakSSN = "123-45-6789"`,
`fixtureLeakCreditCard = "4111111111111111"`) are fragment-assembled,
checksum-valid test values matching `ai-shared-lib`'s own
`fixtureBaseConfigSSN`/`fixtureBaseConfigVisaTestCard` fixtures exactly (an
SSN-shaped string with a valid area/group/serial, and the industry-standard,
publicly documented Visa test card number) — never a real person's SSN or a
real-vendor-shaped credential.

## Acceptance
1. `go.mod` bumped to `v0.9.0`, `go mod tidy` run — done, clean (`go.sum` updated,
   no other dependency drift).
2. `2be4b4ba` removed via `git revert` — done. The revert applied cleanly with no
   conflicts (checked first, per the task's instruction), so no by-hand removal
   was needed. Committed as its own commit (`3cd23a0`), not squashed into
   anything else.
3. PII/financial coverage confirmed via `scanCredentials` alone, with the new
   end-to-end test proving it — done. No new `git-tools` production code was
   needed for the category coverage itself (only the test).
4. Uncommitted `scan_gate_test.go` handoff evidence left untouched, and now
   passes against `v0.9.0` — **done, confirmed PASS** (was failing against
   `v0.8.0`; see Sanity result below for the exact run).
5. Full suite clean — `go build ./...`, `go vet ./...`, `gofmt -l` on every
   changed file, `go test ./...` — done, all clean (see below).
6. Committed as new commits on `feat/betterleaks-cli-wiring`, on top of existing
   history, signed — done (see commit SHAs below).

## Sanity result (TMPDIR/GOTMPDIR=/instance_storage)
- `go build ./...` — clean, no output.
- `go vet ./...` — clean, no output.
- `gofmt -l` on every changed/new `.go` file (`internal/cli/scan_gate_test.go`,
  `internal/cli/scan_pii_financial_test.go`, plus `scan.go`/`scan_test.go`
  touched by the revert) — clean, no output.
- `go test ./...` — every package passes, `internal/cli` ~25s.
- `go test ./internal/cli/... -run TestScanGate_GoverningDiagnosticCategoryMatchesFindingCategory -v`
  — **`--- PASS`** (fails against `v0.8.0`, per its own doc comment; passes now
  that `envelope.go`'s `BuildHookResult` copies `Finding.Category` into each
  diagnostic's `Context`).
- `go test ./internal/cli/... -run TestScanAll_ReportsNonZeroPIIAndFinancialCountsViaScanCredentials -v`
  — `--- SKIP` in this sandbox: no real `betterleaks` binary is provisioned here
  (`BETTERLEAKS_TEST_BIN` unset, and no binary found via `command -v betterleaks`
  either), so the test correctly self-skips rather than failing. Its rule-id and
  category-mapping assertions were verified by direct source inspection of
  `ai-shared-lib`'s `go/githooks/betterleaks.go` (`categoryForRuleID`),
  `data/betterleaks-base.toml` (the three new rule blocks), and its own
  `betterleaks_test.go` (`TestScanCredentialsBaseConfigPIIFinancialRulesFireAndCategorize`),
  not guessed — but this test has not been observed to actually PASS against a
  real binary in this environment. Whoever has `BETTERLEAKS_TEST_BIN` provisioned
  (CI, or a workstation with the binary installed) should run it once to close
  that gap.

## Assumptions & deviations
- Interpreted the task's "gate it behind `BETTERLEAKS_TEST_BIN`... check
  `GIT_TOOLS_BETTERLEAKS_BIN`'s own existing test-gating convention in this file
  first, and follow it, don't invent a new one" as: use the real convention that
  actually exists (`ai-shared-lib`'s `BETTERLEAKS_TEST_BIN`-gated
  `testBetterleaksBinary` skip helper, confirmed by reading
  `go/githooks/betterleaks_test.go`), not a literal env var of that exact name
  invented fresh in this file — `BETTERLEAKS_TEST_BIN` does not appear anywhere
  in `git-tools` prior to this change. `GIT_TOOLS_BETTERLEAKS_BIN` is this CLI's
  own, separate env var naming where `scanCredentials` shells out to; the new
  test reads a real binary path from `BETTERLEAKS_TEST_BIN` and then sets
  `GIT_TOOLS_BETTERLEAKS_BIN` to it, so the CLI subprocess it drives actually
  uses that binary.
- Split the work into two commits (dependency bump + revert as one, the
  reverted-file's replacement/new test as a second) rather than one, since the
  revert commit's message should describe only what it reverts, not the new
  test riding along in the same diff. Both sit cleanly on top of the existing
  branch history, nothing amended or rewritten.

## Hand-off notes
- Test-engineer: `TestScanAll_ReportsNonZeroPIIAndFinancialCountsViaScanCredentials`
  needs a real `betterleaks` binary via `BETTERLEAKS_TEST_BIN` to actually
  execute (it self-skips otherwise) — confirm it passes wherever that binary is
  provisioned. Also worth adversarially checking a near-miss fixture (checksum-
  invalid card / out-of-range SSN) does NOT trip `pii_found`/`financial_found`,
  mirroring `ai-shared-lib`'s own near-miss coverage.
- Quality-reviewer: `TestScanGate_GoverningDiagnosticCategoryMatchesFindingCategory`
  is unmodified handoff evidence from a prior test-engineer pass — confirm it
  is not touched by this task's diff (it isn't) and that it now passes (it does,
  see Sanity result).
