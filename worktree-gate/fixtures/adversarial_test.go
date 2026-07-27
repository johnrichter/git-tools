package fixtures

import (
	"testing"

	"github.com/johnrichter/git-tools/worktree-gate/detect"
)

// TestAdversarialSuite_SatisfiesSCWorktree is the acceptance test for
// SC-WORKTREE: every declared write-fixture must deny, every read-fixture
// must never deny, and every uncertain-fixture must deny (fail closed). Any
// failure here means the gate itself regressed.
func TestAdversarialSuite_SatisfiesSCWorktree(t *testing.T) {
	verbs, err := detect.DefaultVerbs()
	if err != nil {
		t.Fatalf("DefaultVerbs: %v", err)
	}

	cases := Set()
	for _, f := range Verify(cases, verbs, nil) {
		t.Error(f.String())
	}

	var writes, reads, uncertain int
	for _, c := range cases {
		switch c.Category {
		case Write:
			writes++
		case Read:
			reads++
		case Uncertain:
			uncertain++
		}
	}
	if writes == 0 || reads == 0 || uncertain == 0 {
		t.Fatalf("fixture set must cover all three categories, got write=%d read=%d uncertain=%d", writes, reads, uncertain)
	}
}

// TestAdversarialSuite_CatchesAPlantedMiss proves VerifyWith is a live
// check, not a vacuous pass: fed a decider that always allows -- the exact
// shape of a regression that stopped denying writes -- every write and
// uncertain fixture must surface as a failure. Without this, a broken gate
// could still produce a green adversarial suite.
func TestAdversarialSuite_CatchesAPlantedMiss(t *testing.T) {
	cases := Set()
	alwaysAllow := func(Case) detect.Decision { return detect.Decision{} }

	failures := VerifyWith(cases, alwaysAllow)

	wantMisses := 0
	for _, c := range cases {
		if c.wantDeny() {
			wantMisses++
		}
	}
	if len(failures) != wantMisses {
		t.Fatalf("planted always-allow decider: got %d failures, want %d (one per write/uncertain fixture)", len(failures), wantMisses)
	}
	for _, f := range failures {
		if f.Case.Category == Read {
			t.Errorf("a Read fixture must never fail against an always-allow decider: %s", f.Case.Name)
		}
	}
}
