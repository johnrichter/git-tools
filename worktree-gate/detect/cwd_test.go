package detect

import (
	"encoding/json"
	"os"
	"testing"
)

// cwdCorpusCase mirrors one entry of testdata/cwd-corpus.json: a Bash
// command and the statically-resolved effective working directory of its
// first git invocation, per SC-CWD-RESOLVER-CONTRACT. "" means the session
// cwd governs unchanged (no preceding cd/-C); "DENY" means the target is
// unresolvable from the command text alone.
type cwdCorpusCase struct {
	Name    string `json:"name"`
	Command string `json:"command"`
	Expect  string `json:"expect"`
}

type cwdCorpus struct {
	Cases       []cwdCorpusCase `json:"cases"`
	Description string          `json:"description"`
}

func loadCwdCorpus(t *testing.T) cwdCorpus {
	t.Helper()
	b, err := os.ReadFile("testdata/cwd-corpus.json")
	if err != nil {
		t.Fatalf("testdata/cwd-corpus.json: %v", err)
	}
	var c cwdCorpus
	if err := json.Unmarshal(b, &c); err != nil {
		t.Fatalf("testdata/cwd-corpus.json: %v", err)
	}
	if len(c.Cases) == 0 {
		t.Fatal("testdata/cwd-corpus.json carries no cases")
	}
	return c
}

// TestResolveEffectiveCWD_MatchesGoldenCorpus drives resolveEffectiveCWD --
// the same static resolver decideBash calls through effectiveBashCWD --
// against the golden corpus the shell gate's own resolver (git-gate.sh
// --print-eff-dir) is pinned to. One shared corpus, mirrored byte-identical
// into this package, is what keeps the two resolvers from silently
// diverging (SC-CWD-RESOLVER-CONTRACT).
func TestResolveEffectiveCWD_MatchesGoldenCorpus(t *testing.T) {
	corpus := loadCwdCorpus(t)
	for _, c := range corpus.Cases {
		t.Run(c.Name, func(t *testing.T) {
			dir, unresolvable := resolveEffectiveCWD(c.Command)
			if c.Expect == "DENY" {
				if !unresolvable {
					t.Errorf("resolveEffectiveCWD(%q) = (%q, unresolvable=%v), want unresolvable", c.Command, dir, unresolvable)
				}
				return
			}
			if unresolvable || dir != c.Expect {
				t.Errorf("resolveEffectiveCWD(%q) = (%q, unresolvable=%v), want (%q, unresolvable=false)", c.Command, dir, unresolvable, c.Expect)
			}
		})
	}
}
