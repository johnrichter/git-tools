package detect

import "testing"

func testVerbs(t *testing.T) Verbs {
	t.Helper()
	v, err := DefaultVerbs()
	if err != nil {
		t.Fatalf("DefaultVerbs() error: %v", err)
	}
	return v
}

func TestClassifyBash_ReadsNeverTrip(t *testing.T) {
	v := testVerbs(t)
	reads := []string{
		"git status",
		"git diff HEAD~1",
		"git log --oneline -5",
		"git show HEAD",
		"git branch -a",
		"cat README.md",
		"grep -rn TODO .",
		"git status && git diff",
		"git log | head -20",
		"find . -name *.go",
		"find . -type f -print",
	}
	for _, cmd := range reads {
		if got := ClassifyBash(v, cmd); got != ClassRead {
			t.Errorf("ClassifyBash(%q) = %v, want ClassRead", cmd, got)
		}
	}
}

func TestClassifyBash_WritesAlwaysTrip(t *testing.T) {
	v := testVerbs(t)
	writes := []string{
		"git commit -m fix",
		"git add .",
		"vim file.go",
		"echo hi > file.txt",
		"echo hi >> file.txt",
		"rm -rf build",
		"mv a b",
		"npm install left-pad",
		"pip install requests",
		"go get github.com/x/y",
		"git status && rm file",
		"find . -delete",
		"find . -name *.tmp -exec rm {} \\;",
	}
	for _, cmd := range writes {
		if got := ClassifyBash(v, cmd); got != ClassWrite {
			t.Errorf("ClassifyBash(%q) = %v, want ClassWrite", cmd, got)
		}
	}
}

func TestClassifyBash_UnknownCommandIsUncertain(t *testing.T) {
	v := testVerbs(t)
	if got := ClassifyBash(v, "some-unlisted-tool --frobnicate"); got != ClassUncertain {
		t.Errorf("ClassifyBash() = %v, want ClassUncertain for an unrecognized command", got)
	}
}

func TestClassifyBash_MixedUnknownAndReadIsUncertain(t *testing.T) {
	v := testVerbs(t)
	if got := ClassifyBash(v, "git status && some-unlisted-tool"); got != ClassUncertain {
		t.Errorf("ClassifyBash() = %v, want ClassUncertain when any piece is unrecognized", got)
	}
}

// openingRedirect must separate a redirect that opens something from a bare fd
// duplication, and must stay set whenever a file-writing redirect is present --
// including alongside a co-present dup, so a dup can never mask a write.
func TestDecompose_OpeningRedirectSeparatesFdDupFromRealWrite(t *testing.T) {
	cases := []struct {
		command             string
		wantOpeningRedirect bool
		wantWritesFile      bool
	}{
		{"tool run", false, false},
		{"tool run 2>&1", false, false},
		{"tool run >&2", false, false},
		{"tool run 2>&-", false, false},
		{"tool run 2>&10", false, false},
		{"tool run >out", true, true},
		{"tool run 2>out", true, true},
		{"tool run >>out", true, true},
		{"tool run &>out", true, true},
		{"tool run 2>&1 >out", true, true},
		{"tool run >out 2>&1", true, true},
		{"tool run 2>&1x", true, true},
		{"tool run <in", true, false},
		{"tool run <<EOF\nbody\nEOF\n", true, false},
		// A21 (deferred): a bare-digit target reads as a duplication, not the
		// file bash would create. Pinned as-is -- neither widened nor narrowed.
		{"tool run >1", false, false},
	}
	for _, c := range cases {
		pieces := decompose(c.command)
		if len(pieces) != 1 {
			t.Errorf("decompose(%q) produced %d pieces, want 1", c.command, len(pieces))
			continue
		}
		p := pieces[0]
		if p.openingRedirect != c.wantOpeningRedirect || p.writesFile != c.wantWritesFile {
			t.Errorf("decompose(%q) = (openingRedirect=%v, writesFile=%v), want (%v, %v)",
				c.command, p.openingRedirect, p.writesFile, c.wantOpeningRedirect, c.wantWritesFile)
		}
		if p.writesFile && !p.openingRedirect {
			t.Errorf("decompose(%q): a file-writing redirect must always set openingRedirect", c.command)
		}
	}
}
