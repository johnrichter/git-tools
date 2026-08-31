// End-to-end proof that PII/financial detection flows through
// scanCredentials alone, with no dedicated PII/financial scan call anywhere
// in this CLI: a real `scan all` invocation over a repo carrying PII- and
// financial-shaped fixtures reports non-zero pii_found and financial_found,
// driven entirely by githooks' base-config betterleaks rules
// (pii-ssn, financial-credit-card-number) and its categoryForRuleID
// categorization.
package cli_test

import (
	"os"
	"testing"
)

// fixtureLeakSSN and fixtureLeakCreditCard are structurally valid,
// checksum-passing test values: an SSN-shaped string with a valid
// area/group/serial, and the industry-standard, publicly documented Visa
// test card number - fabricated, never a real person's SSN or a
// real-vendor-shaped credential. Fragment-assembled so this source line
// never carries an unbroken sensitive-looking literal.
const (
	fixtureLeakSSN        = "123-45-" + "6789"
	fixtureLeakCreditCard = "41111111111111" + "11"
)

// fixtureNearMissInvalidAreaSSN and fixtureNearMissLuhnInvalidCard are
// checksum-invalid near misses: an SSN with an invalid (000) area number, and
// a credit-card number one digit off a Luhn-valid one. Mirrors the exact
// fixture shapes go/githooks' own
// TestScanCredentialsBaseConfigPIIFinancialRulesFireAndCategorize uses to
// prove its pii-ssn/financial-credit-card-number rules don't over-match —
// applied here at the git-tools CLI level, through the same scanCredentials
// call the valid-fixture test above exercises.
const (
	fixtureNearMissInvalidAreaSSN  = "000-45-" + "6789"
	fixtureNearMissLuhnInvalidCard = "4111111111111" + "12"
)

// testBetterleaksBinary returns the path to a real betterleaks binary for
// this subprocess-level integration test, taken from the BETTERLEAKS_TEST_BIN
// environment variable — the same provisioning convention githooks' own
// subprocess integration tests use. It skips cleanly when no binary is
// provisioned, e.g. a bare checkout with no network access, rather than
// failing the whole suite.
func testBetterleaksBinary(t *testing.T) string {
	t.Helper()
	bin := os.Getenv("BETTERLEAKS_TEST_BIN")
	if bin == "" {
		t.Skip("BETTERLEAKS_TEST_BIN not set; skipping betterleaks subprocess integration test")
	}
	return bin
}

// TestScanAll_ReportsNonZeroPIIAndFinancialCountsViaScanCredentials proves
// pii_found and financial_found reach the CLI's own output through
// scanCredentials' one betterleaks subprocess call — the same call
// scanTree already makes for credentials — rather than through any second,
// dedicated PII/financial scan call.
func TestScanAll_ReportsNonZeroPIIAndFinancialCountsViaScanCredentials(t *testing.T) {
	bin := buildCLI(t)
	betterleaksBin := testBetterleaksBinary(t)
	dir := initRepo(t)
	commitFile(t, dir, "leak.txt", "ssn: "+fixtureLeakSSN+"\ncard: "+fixtureLeakCreditCard+"\n", "add fixture leak")
	t.Setenv("GIT_TOOLS_BETTERLEAKS_BIN", betterleaksBin)

	r, exit := runCLI(t, bin, "--repo", dir, "scan", "all")
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("status=%s exit=%d, want precondition_unmet/30: %+v", r.Status, exit, r)
	}

	pii, _ := r.Data["pii_found"].(float64)
	financial, _ := r.Data["financial_found"].(float64)
	if pii == 0 {
		t.Errorf("r.Data[%q] = %v, want non-zero: leak.txt carries an SSN-shaped fixture", "pii_found", r.Data["pii_found"])
	}
	if financial == 0 {
		t.Errorf("r.Data[%q] = %v, want non-zero: leak.txt carries a credit-card-shaped fixture", "financial_found", r.Data["financial_found"])
	}

	var sawSSN, sawCard bool
	for _, e := range r.Errors {
		context, _ := e["context"].(map[string]any)
		if path, _ := context["path"].(string); path != "leak.txt" {
			continue
		}
		switch context["rule"] {
		case "pii-ssn":
			sawSSN = true
		case "financial-credit-card-number":
			sawCard = true
		}
	}
	if !sawSSN {
		t.Fatalf("no reported error names leak.txt with rule pii-ssn: %+v", r.Errors)
	}
	if !sawCard {
		t.Fatalf("no reported error names leak.txt with rule financial-credit-card-number: %+v", r.Errors)
	}
}

// TestScanAll_DoesNotFlagChecksumInvalidNearMissPIIFinancial proves the
// pii-ssn/financial-credit-card-number rules reaching the CLI through
// scanCredentials don't over-match on checksum-invalid near misses: an SSN
// with an invalid area number and a card number one digit off Luhn-valid
// must never surface as a finding, even though a genuine valid fixture
// committed in the same repo does. Without this, a naive pattern-only rule
// (or a scanner-side bug swallowing betterleaks' own checksum validation)
// would silently flag anything digit-shaped as PII/financial.
func TestScanAll_DoesNotFlagChecksumInvalidNearMissPIIFinancial(t *testing.T) {
	bin := buildCLI(t)
	betterleaksBin := testBetterleaksBinary(t)
	dir := initRepo(t)
	commitFile(t, dir, "leak.txt", "ssn: "+fixtureLeakSSN+"\ncard: "+fixtureLeakCreditCard+"\n", "add fixture leak")
	commitFile(t, dir, "near_miss.txt",
		"ssn: "+fixtureNearMissInvalidAreaSSN+"\ncard: "+fixtureNearMissLuhnInvalidCard+"\n",
		"add near-miss fixture")
	t.Setenv("GIT_TOOLS_BETTERLEAKS_BIN", betterleaksBin)

	r, exit := runCLI(t, bin, "--repo", dir, "scan", "all")
	if r.Status != "precondition_unmet" || exit != 30 {
		t.Fatalf("status=%s exit=%d, want precondition_unmet/30 (leak.txt alone must still refuse): %+v", r.Status, exit, r)
	}

	for _, e := range r.Errors {
		context, _ := e["context"].(map[string]any)
		if path, _ := context["path"].(string); path == "near_miss.txt" {
			t.Errorf("near_miss.txt must never be flagged (checksum-invalid PII/financial near miss), but got: %+v", e)
		}
	}
}
