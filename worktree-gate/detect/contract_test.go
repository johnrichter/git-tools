package detect

// Byte-equality checks against the marketplace plugin's canonical copies of
// two data artifacts this package mirrors: the cwd-resolution golden corpus
// (SC-CWD-RESOLVER-CONTRACT) and the tracking-doc basename set
// (SC-TRACKINGDOCS-CONTRACT). Both are opt-in via an environment variable
// naming the canonical file directly and skip, rather than fail, when unset:
// this package's build and its own test suite must never depend on the
// marketplace repo being checked out alongside git-tools. An environment
// that does have both repos (this workspace, or a cross-repo CI job) turns
// the check on by setting the variable.

import (
	"os"
	"testing"
)

func TestCwdCorpus_ByteEqualsMarketplaceCanonicalCopy(t *testing.T) {
	ref := os.Getenv("MARKETPLACE_CWD_CORPUS")
	if ref == "" {
		t.Skip("MARKETPLACE_CWD_CORPUS not set; skipping the cross-repo byte-equality check (SC-CWD-RESOLVER-CONTRACT)")
	}
	assertByteEqual(t, "testdata/cwd-corpus.json", ref)
}

func TestTrackingDocs_ByteEqualsMarketplaceCanonicalCopy(t *testing.T) {
	ref := os.Getenv("MARKETPLACE_TRACKINGDOCS")
	if ref == "" {
		t.Skip("MARKETPLACE_TRACKINGDOCS not set; skipping the cross-repo byte-equality check (SC-TRACKINGDOCS-CONTRACT)")
	}
	assertByteEqual(t, "trackingdocs.json", ref)
}

func assertByteEqual(t *testing.T, local, ref string) {
	t.Helper()
	got, err := os.ReadFile(local)
	if err != nil {
		t.Fatalf("%s: %v", local, err)
	}
	want, err := os.ReadFile(ref)
	if err != nil {
		t.Fatalf("%s: %v", ref, err)
	}
	if string(got) != string(want) {
		t.Errorf("%s does not byte-equal %s", local, ref)
	}
}
