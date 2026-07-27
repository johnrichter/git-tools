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
