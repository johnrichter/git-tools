package detect

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

// TestSC15_SDET_AdversarialProbe adds boundary cases the reviewer's and prior
// author's fixtures did not exercise: verb-subtoken gaps, assignment-prefix
// smuggling, quoting/case sensitivity on the verified path, and the glued
// (=) spelling of the retarget flags.
func TestSC15_SDET_AdversarialProbe(t *testing.T) {
	const bin = "/plugin-data/bin/git-tools"
	const content = "PROVISIONED-CLI-BYTES"
	sum := sha256.Sum256([]byte(content))
	digest := hex.EncodeToString(sum[:])
	v := testVerbs(t)

	newFS := func() *fakeFS {
		return newFakeFS().dir("/repo/.git").file(bin, content)
	}

	cases := []struct {
		name     string
		command  string
		wantDeny bool
		note     string
	}{
		{
			name:     "worktree-with-no-subverb-denies",
			command:  bin + " worktree",
			wantDeny: true,
			note:     "worktree alone (no 'add') must not satisfy the three-verb allowance",
		},
		{
			name:     "worktree-list-wrong-subverb-denies",
			command:  bin + " worktree list",
			wantDeny: true,
			note:     "worktree list is not one of the three allowed landing verbs",
		},
		{
			name:     "leading-var-assignment-prefix-denies",
			command:  "FOO=bar " + bin + " merge main",
			wantDeny: true,
			note:     "a leading shell assignment shifts the leading token away from the verified path; must not still match",
		},
		{
			name:     "quoted-binary-path-still-matches-allows",
			command:  `"` + bin + `" merge main`,
			wantDeny: false,
			note:     "quoting alone must not defeat identity match once stripQuotes normalizes it",
		},
		{
			name:     "uppercase-path-spelling-denies",
			command:  "/PLUGIN-DATA/BIN/GIT-TOOLS merge main",
			wantDeny: true,
			note:     "path comparison is case-sensitive; a differently-cased path is a different string, not the verified path",
		},
		{
			name:     "glued-repo-retarget-denies",
			command:  bin + " merge --repo=/other main",
			wantDeny: true,
			note:     "glued --repo= form must be recognized identically to the spaced form",
		},
		{
			name:     "glued-config-retarget-denies",
			command:  bin + " merge --config=/other main",
			wantDeny: true,
			note:     "glued --config= form must be recognized identically to the spaced form",
		},
		{
			name:     "extra-leading-token-before-binary-denies",
			command:  "env " + bin + " merge main",
			wantDeny: true,
			note:     "an extra leading token (e.g. env wrapper) shifts the leading token away from the verified path",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fs := newFS()
			d := Decide(fs.lstat, fs.readFile, v, nil, TrackingDocs{}, nil, Input{
				ToolName:             "Bash",
				CWD:                  "/repo",
				Command:              c.command,
				ProvisionedBinPath:   bin,
				ProvisionedBinDigest: digest,
			})
			if d.Deny != c.wantDeny {
				t.Errorf("%s: Decide deny=%v, want %v (reason=%q) -- %s", c.name, d.Deny, c.wantDeny, d.Reason, c.note)
			}
		})
	}
}
