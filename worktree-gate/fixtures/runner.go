package fixtures

import (
	"fmt"

	"github.com/johnrichter/git-tools/worktree-gate/detect"
)

// Failure records one Case whose verdict violated its Category's invariant.
type Failure struct {
	Case Case
	Got  detect.Decision
}

func (f Failure) String() string {
	want := "allow"
	if f.Case.wantDeny() {
		want = "deny"
	}
	return fmt.Sprintf("%s [%s/%s]: want %s, got deny=%v reason=%q",
		f.Case.Name, f.Case.Category, f.Case.Topology, want, f.Got.Deny, f.Got.Reason)
}

// VerifyWith runs every case through decide and reports each one whose
// verdict violates its Category's invariant (empty means every case held).
// It's separate from Verify so the suite's own sensitivity can be proven
// against a decider standing in for a regression, without touching the
// real gate.
func VerifyWith(cases []Case, decide func(Case) detect.Decision) []Failure {
	var failures []Failure
	for _, c := range cases {
		got := decide(c)
		if got.Deny != c.wantDeny() {
			failures = append(failures, Failure{Case: c, Got: got})
		}
	}
	return failures
}

// Verify runs cases through the real gate: each case's declared topology is
// rendered as an in-memory filesystem and evaluated by detect.Decide against
// verbs and trackingDocs. Passing verbsErr/trackingDocsErr through lets a
// caller also exercise either classifier-degraded path across the same
// fixture set.
func Verify(cases []Case, verbs detect.Verbs, verbsErr error, trackingDocs detect.TrackingDocs, trackingDocsErr error) []Failure {
	return VerifyWith(cases, func(c Case) detect.Decision {
		lstat, readFile := buildFS(c)
		return detect.Decide(lstat, readFile, verbs, verbsErr, trackingDocs, trackingDocsErr, c.toInput())
	})
}
