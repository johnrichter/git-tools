package detect

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// TestSC15_AdversarialEdgeCases probes boundary conditions not covered by the
// main corpus: retarget flag preceding the verb, a redirect on the otherwise
// qualifying piece, digest case-insensitivity, nested (non-top-level)
// placement of an otherwise-qualifying invocation, and a trailing-slash
// mismatch on the verified path.
func TestSC15_AdversarialEdgeCases(t *testing.T) {
	const bin = "/plugin-data/bin/git-tools"
	const content = "PROVISIONED-CLI-BYTES"
	sum := sha256.Sum256([]byte(content))
	digestLower := hex.EncodeToString(sum[:])

	v := testVerbs(t)

	newFS := func() *fakeFS {
		return newFakeFS().dir("/repo/.git").file(bin, content)
	}

	cases := []struct {
		name     string
		command  string
		path     string
		digest   string
		wantDeny bool
		wantNote string
	}{
		{
			name:     "retarget-flag-before-verb-denies",
			command:  bin + " --repo /other merge main",
			path:     bin,
			digest:   digestLower,
			wantDeny: true,
			wantNote: "verb token position shifted by a leading flag must not accidentally match a landing verb",
		},
		{
			name:     "redirect-on-otherwise-qualifying-piece-denies",
			command:  bin + " merge main > /repo/out",
			path:     bin,
			digest:   digestLower,
			wantDeny: true,
			wantNote: "a redirect on the CLI piece must void the exemption and fall through to the named-path rule",
		},
		{
			name:     "digest-case-insensitive-still-allows",
			command:  bin + " merge main",
			path:     bin,
			digest:   toUpper(digestLower),
			wantDeny: false,
			wantNote: "an uppercase-spelled but otherwise-correct digest must still match",
		},
		{
			name:     "nested-in-subshell-not-exempt-at-depth-not-zero",
			command:  "(" + bin + " merge main)",
			path:     bin,
			digest:   digestLower,
			wantDeny: true,
			wantNote: "SC15 exemption is top-level only (depth==0); merge classifies write-class and the nested occurrence must not inherit the top-level exemption -- confirms it does not silently bypass classification at depth>0",
		},
		{
			name:     "trailing-slash-on-verified-path-mismatches-denies",
			command:  bin + "/ merge main",
			path:     bin,
			digest:   digestLower,
			wantDeny: true,
			wantNote: "exact leading-token equality is required; a spelling variant of the verified path must not match",
		},
		{
			name:     "command-substitution-smuggled-into-exempt-piece-denies",
			command:  bin + " merge $(cp evil /repo/tracked)",
			path:     bin,
			digest:   digestLower,
			wantDeny: true,
			wantNote: "the exemption waives the piece's own class/named-path rule but not the SC16 interior scan; a $()-smuggled write into the primary checkout runs shell-level regardless of the CLI and must still be caught",
		},
		{
			name:     "backtick-smuggled-into-exempt-piece-denies",
			command:  bin + " push `cp evil /repo/tracked`",
			path:     bin,
			digest:   digestLower,
			wantDeny: true,
			wantNote: "backtick spelling of the same interior-smuggling bypass must be caught identically",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := newFS()
			d := Decide(fs.lstat, fs.readFile, nil, v, nil, Input{
				ToolName:             "Bash",
				CWD:                  "/repo",
				Command:              c.command,
				ProvisionedBinPath:   c.path,
				ProvisionedBinDigest: c.digest,
			})
			if d.Deny != c.wantDeny {
				t.Errorf("%s: Decide deny=%v, want %v (reason=%q) -- %s", c.name, d.Deny, c.wantDeny, d.Reason, c.wantNote)
			}
		})
	}
}

func toUpper(s string) string {
	out := []byte(s)
	for i, c := range out {
		if c >= 'a' && c <= 'z' {
			out[i] = c - 'a' + 'A'
		}
	}
	return string(out)
}
