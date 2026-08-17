package detect

import (
	"strings"
	"testing"
)

// TestAdversarial_CommandWithLiteralDashDash_RemedyStaysRecoverable guards the
// message-format fix: the " -- " token joining situation and remedy in Reason is
// not distinguished from the same substring the situation text echoes verbatim
// (via %q) from caller-controlled input -- most commonly the POSIX "--"
// end-of-options marker in a denied Bash command. Any consumer that recovered
// the remedy by splitting Reason on " -- " would corrupt the split once the
// echoed command contains that token. deny() therefore carries the remedy on
// Decision.Remedy, so it stays recoverable regardless of what the command
// contains, and Reason still closes with that same remedy for human display.
func TestAdversarial_CommandWithLiteralDashDash_RemedyStaysRecoverable(t *testing.T) {
	fs := primaryFS()
	v := testVerbs(t)
	cmd := "somefroblecmd -- /repo/tracked.md"
	d := Decide(fs.lstat, fs.readFile, v, nil, Input{ToolName: "Bash", CWD: "/repo", Command: cmd})
	if !d.Deny {
		t.Fatal("expected deny (fail-closed on unclassifiable command)")
	}

	// The remedy is available intact without parsing Reason, even though Reason
	// now contains two " -- " occurrences (the echoed command's and deny()'s).
	if strings.TrimSpace(d.Remedy) == "" {
		t.Fatalf("Remedy is empty; denial %q carries no recoverable remedy", d.Reason)
	}
	if strings.Contains(d.Remedy, " -- ") {
		t.Fatalf("Remedy %q must not itself contain the join token, or Reason's suffix contract is ambiguous", d.Remedy)
	}
	if !strings.HasSuffix(d.Reason, " -- "+d.Remedy) {
		t.Fatalf("Reason %q must close with %q; got a suffix that does not match the structured remedy", d.Reason, d.Remedy)
	}
	// Confirm the caller-supplied "--" is what would have fooled a Reason split:
	// there are at least two " -- " occurrences, so a naive first-cut would have
	// returned the wrong remedy. The structured field sidesteps it entirely.
	if strings.Count(d.Reason, " -- ") < 2 {
		t.Fatalf("expected the echoed command's own \" -- \" plus deny()'s separator in %q; the case no longer reproduces the ambiguity it guards against", d.Reason)
	}
}
