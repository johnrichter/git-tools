package detect

import "testing"

// A write to a tracking-doc-style path (CLAUDE.md) inside the primary checkout
// must be DENIED: the gate carries no tracking-doc write exemption, so such a
// target is judged like any other primary-checkout write.
func TestSDET_PrimaryCheckoutTrackingDocWrite_NowDenied(t *testing.T) {
	fs := newFakeFS().dir("/repo/.git")
	d := Decide(fs.lstat, fs.readFile, nil, Verbs{}, nil, Input{
		ToolName: "Write",
		FilePath: "/repo/CLAUDE.md",
	})
	if !d.Deny {
		t.Fatalf("expected DENY for primary-checkout tracking-doc write, got allow: %+v", d)
	}
}
