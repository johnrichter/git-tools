package detect

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

// The fixed hermetic topology every corpus case runs against: a primary
// checkout, a linked worktree under it, and the plugin-provisioned CLI at a
// path outside any repo.
const (
	corpusPrimary        = "/repo"
	corpusWorktree       = "/repo/wt"
	corpusProvisionedBin = "/plugin-data/bin/git-tools"
	corpusBinContent     = "PROVISIONED-CLI-BYTES"
)

type decideBashCase struct {
	Name       string `json:"name"`
	Command    string `json:"command"`
	CWD        string `json:"cwd"`
	ArgPath    string `json:"arg_path"`    // "" => correct provisioned path; "omit" => empty; else literal
	ArgDigest  string `json:"arg_digest"`  // "" => correct digest; "omit" => empty; "wrong"; else literal
	BinPresent *bool  `json:"bin_present"` // nil => true
	WantDeny   bool   `json:"want_deny"`
}

func loadDecideBashCorpus(t *testing.T) []decideBashCase {
	t.Helper()
	b, err := os.ReadFile("testdata/decide-bash-corpus.json")
	if err != nil {
		t.Fatalf("testdata/decide-bash-corpus.json: %v", err)
	}
	var c struct {
		Cases []decideBashCase `json:"cases"`
	}
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatalf("testdata/decide-bash-corpus.json: %v", err)
	}
	if len(c.Cases) == 0 {
		t.Fatal("decide-bash corpus carries no cases")
	}
	return c.Cases
}

// TestDecide_Bash_NamedPathAndSC15_Corpus drives Decide over the full
// SC20/SC15 fixture matrix: named-path denial ahead of both cwd short-circuits,
// the read-class and worktree-target anti-lockout allowances, and SC15's
// digest-verified provisioned-CLI carve-out across verbs, digest states, and
// retarget flags. One decomposition, one fixed topology, per-case argv.
func TestDecide_Bash_NamedPathAndSC15_Corpus(t *testing.T) {
	v := testVerbs(t)
	correctDigest := hex.EncodeToString(sha256Sum(corpusBinContent))

	for _, c := range loadDecideBashCorpus(t) {
		t.Run(c.Name, func(t *testing.T) {
			fs := newFakeFS().
				dir(corpusPrimary+"/.git").
				file(corpusWorktree+"/.git", "gitdir: /repo/.git/worktrees/wt\n")
			if c.BinPresent == nil || *c.BinPresent {
				fs.file(corpusProvisionedBin, corpusBinContent)
			}

			argPath := corpusProvisionedBin
			switch c.ArgPath {
			case "omit":
				argPath = ""
			case "":
				// keep the correct provisioned path
			default:
				argPath = c.ArgPath
			}

			argDigest := correctDigest
			switch c.ArgDigest {
			case "omit":
				argDigest = ""
			case "wrong":
				argDigest = "0000000000000000000000000000000000000000000000000000000000000000"
			case "":
				// keep the correct digest
			default:
				argDigest = c.ArgDigest
			}

			d := Decide(fs.lstat, fs.readFile, v, nil, TrackingDocs{}, nil, Input{
				ToolName:             "Bash",
				CWD:                  c.CWD,
				Command:              c.Command,
				ProvisionedBinPath:   argPath,
				ProvisionedBinDigest: argDigest,
			})
			if d.Deny != c.WantDeny {
				t.Errorf("Decide(cwd=%q, cmd=%q) deny=%v, want deny=%v (reason=%q)",
					c.CWD, c.Command, d.Deny, c.WantDeny, d.Reason)
			}
		})
	}
}

func sha256Sum(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}
