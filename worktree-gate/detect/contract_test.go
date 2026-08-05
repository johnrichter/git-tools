package detect

// Digest verification for the four data artifacts this package's contracts
// pin: the cwd-resolution corpus (SC-CWD-RESOLVER-CONTRACT), the
// tracking-doc basename set (SC-TRACKINGDOCS-CONTRACT), the bash-connector
// set, and SC11's banned-name list (the same list scripts/surface-hygiene.sh
// scans against). Each digest below is the sha256 recorded for the
// git-tools tag the artifact ships under -- update the digest in the same
// commit as any change to the artifact it names, or this test catches the
// drift.
//
// Self-contained by construction: every input is a file this repo already
// ships, so there is no environment variable, sibling checkout, or
// discovered path that could be missing -- nothing here can skip.

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"testing"
)

var pinnedDigests = []struct {
	path   string
	sha256 string
}{
	{"testdata/cwd-corpus.json", "e4c9f07f2e323b7559ae4632e79dc165e405599421064adfc9c985f3b60d50e0"},
	{"trackingdocs.json", "199d5abf6229a5c3cce5be62f2337d49f92c65c3216aad1ab25dcf3249de6a90"},
	{"contracts/connectors.json", "ef53c0b7154fc37dd2e2aa0d5be13f0ba0c61cd1463b67c17aa869ec3277c01e"},
	{"contracts/banned-names.json", "8189784124b3dd59491f0fec9d208c250559a2639fdfe1a6823947e4f15f78aa"},
}

func TestContractArtifacts_MatchPinnedDigest(t *testing.T) {
	for _, c := range pinnedDigests {
		t.Run(c.path, func(t *testing.T) {
			b, err := os.ReadFile(c.path)
			if err != nil {
				t.Fatalf("%s: %v", c.path, err)
			}
			sum := sha256.Sum256(b)
			got := hex.EncodeToString(sum[:])
			if got != c.sha256 {
				t.Errorf("%s: sha256 is %s, pinned digest is %s -- artifact changed without repinning its digest", c.path, got, c.sha256)
			}
		})
	}
}
