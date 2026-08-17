package detect

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

// The fixed hermetic topology every corpus case runs against: a primary
// checkout, a linked worktree under it, an already-existing tracked file in
// each, a device node outside any repo, and the plugin-provisioned CLI at a
// path outside any repo. The existing files and the device node are what let a
// case distinguish a write to a path that already exists from one to a new
// path -- their own `.git` probe is an ENOTDIR, not a missing entry.
const (
	corpusPrimary        = "/repo"
	corpusPrimaryFile    = "/repo/tracked.md"
	corpusWorktree       = "/repo/wt"
	corpusWorktreeFile   = "/repo/wt/tracked.md"
	corpusDevice         = "/dev/null"
	corpusProvisionedBin = "/plugin-data/bin/git-tools"
	corpusBinContent     = "PROVISIONED-CLI-BYTES"
)

// errCorpusFSFailure stands in for an lstat failure that leaves repo membership
// genuinely unknown -- permission denied, an I/O error -- as opposed to one
// that answers the membership question. A case naming err_at must still deny.
var errCorpusFSFailure = errors.New("simulated filesystem failure")

type decideBashCase struct {
	Name       string `json:"name"`
	Command    string `json:"command"`
	CWD        string `json:"cwd"`
	ArgPath    string `json:"arg_path"`    // "" => correct provisioned path; "omit" => empty; else literal
	ArgDigest  string `json:"arg_digest"`  // "" => correct digest; "omit" => empty; "wrong"; else literal
	BinPresent *bool  `json:"bin_present"` // nil => true
	ErrAt      string `json:"err_at"`      // path whose lstat fails indeterminately; "" => none
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

// runDecideBashCase builds one case's fixed hermetic topology plus its
// per-case argv overrides and returns Decide's verdict, so every test driving
// the corpus judges the same fixture the same way.
func runDecideBashCase(t *testing.T, v Verbs, correctDigest string, c decideBashCase) Decision {
	t.Helper()
	fs := newFakeFS().
		dir(corpusPrimary+"/.git").
		file(corpusWorktree+"/.git", "gitdir: /repo/.git/worktrees/wt\n").
		file(corpusPrimaryFile, "tracked\n").
		file(corpusWorktreeFile, "tracked\n").
		device(corpusDevice)
	if c.BinPresent == nil || *c.BinPresent {
		fs.file(corpusProvisionedBin, corpusBinContent)
	}
	if c.ErrAt != "" {
		fs.errAt(c.ErrAt, errCorpusFSFailure)
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

	return Decide(fs.lstat, fs.readFile, v, nil, Input{
		ToolName:             "Bash",
		CWD:                  c.CWD,
		Command:              c.Command,
		ProvisionedBinPath:   argPath,
		ProvisionedBinDigest: argDigest,
	})
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
			d := runDecideBashCase(t, v, correctDigest, c)
			if d.Deny != c.WantDeny {
				t.Errorf("Decide(cwd=%q, cmd=%q) deny=%v, want deny=%v (reason=%q)",
					c.CWD, c.Command, d.Deny, c.WantDeny, d.Reason)
			}
		})
	}
}

// namedPathOrderingWantsTarget pins SC-D: a corpus case whose write-class
// piece names both a redirect target and a destination operand, where the
// operand used to resolve into a repo before the redirect target got a
// chance to. Its Reason must now name the listed target -- the path the shell
// actually opens for writing -- instead of the operand.
var namedPathOrderingWantsTarget = map[string]string{
	"named-redirect-abs-primary-from-primary-cwd":         "/repo/tracked",
	"named-redirect-glued-cd-then-redirect-into-primary":  "/repo/tracked",
	"sc15-stderr-to-file-denies":                          "/repo/somefile",
	"sc15-stdout-to-file-denies":                          "/repo/somefile",
	"sc15-mixed-fd-dup-then-file-redirect-denies":         "/repo/somefile",
	"sc15-mixed-file-redirect-then-fd-dup-denies":         "/repo/somefile",
	"sc15-fd-dup-lookalike-target-is-a-file-denies":       "/repo/1x",
	"existing-file-target-from-primary-cwd-denied":        "/repo/tracked.md",
	"new-path-target-from-primary-cwd-denied-identically": "/repo/new.md",
}

// TestDecide_Bash_NamedPathOrdering_RedirectTargetPrecedesOperand re-runs the
// full pre-existing corpus and asserts the SC-D reorder (redirect targets
// judged ahead of a command's own destination operands) is message-only: the
// denied set stays identical to the corpus's want_deny values -- zero verdict
// flips -- and only the cases in namedPathOrderingWantsTarget gain a Reason
// naming the redirect's real target; every other case's Reason is unaffected
// by the reorder because it never carries both a redirect target and a
// destination operand to reorder between.
func TestDecide_Bash_NamedPathOrdering_RedirectTargetPrecedesOperand(t *testing.T) {
	v := testVerbs(t)
	correctDigest := hex.EncodeToString(sha256Sum(corpusBinContent))

	for _, c := range loadDecideBashCorpus(t) {
		t.Run(c.Name, func(t *testing.T) {
			d := runDecideBashCase(t, v, correctDigest, c)
			if d.Deny != c.WantDeny {
				t.Fatalf("verdict flip: Decide(cwd=%q, cmd=%q) deny=%v, want deny=%v (reason=%q)",
					c.CWD, c.Command, d.Deny, c.WantDeny, d.Reason)
			}
			if want, ok := namedPathOrderingWantsTarget[c.Name]; ok {
				if !strings.Contains(d.Reason, want) {
					t.Errorf("Decide(cwd=%q, cmd=%q) reason %q does not name the redirect target %q",
						c.CWD, c.Command, d.Reason, want)
				}
			}
		})
	}
}

func sha256Sum(s string) []byte {
	sum := sha256.Sum256([]byte(s))
	return sum[:]
}
