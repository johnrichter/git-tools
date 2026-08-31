// End-to-end proof that githooks.ScanPIIFinancial's findings genuinely flow
// through the CLI, not just through addCategoryCounts' own unit tests: a
// real `scan all` invocation over a repo carrying PII/financial-shaped
// fixtures reports non-zero pii_found and financial_found.
package cli_test

import "testing"

// fixtureLeakSSN and fixtureLeakCreditCard are structurally valid,
// checksum-passing test values matching githooks' own piifinancial_test.go
// fixture convention (fixtureValidSSN, fixtureRealVisaShape) - fabricated,
// never a real person's SSN or a real-vendor-shaped card number.
// Fragment-assembled so this source line never carries an unbroken
// sensitive-looking literal.
const (
	fixtureLeakSSN        = "123-45-" + "6789"
	fixtureLeakCreditCard = "412345678901234" + "9"
)

// TestScanAll_ReportsNonZeroPIIAndFinancialCounts proves scanTree's newly
// wired githooks.ScanPIIFinancial call reaches the CLI's own output: a
// committed file carrying an SSN-shaped and a credit-card-shaped fixture
// value makes `scan all` refuse with both pii_found and financial_found
// reported as non-zero — not merely reachable through a hand-built
// []githooks.Finding fed straight to addCategoryCounts in isolation.
func TestScanAll_ReportsNonZeroPIIAndFinancialCounts(t *testing.T) {
	bin := buildCLI(t)
	dir := initRepo(t)
	commitFile(t, dir, "leak.txt", "ssn: "+fixtureLeakSSN+"\ncard: "+fixtureLeakCreditCard+"\n", "add fixture leak")

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
		case "ssn":
			sawSSN = true
		case "credit_card_number":
			sawCard = true
		}
	}
	if !sawSSN {
		t.Fatalf("no reported error names leak.txt with rule ssn: %+v", r.Errors)
	}
	if !sawCard {
		t.Fatalf("no reported error names leak.txt with rule credit_card_number: %+v", r.Errors)
	}
}
