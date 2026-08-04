package detect

// Test-engineer verification suite for task M3.P1.T1. These tests assert
// the task's acceptance criteria directly against the shipped code; they
// are NOT edits to the implementation. Left in place as evidence.

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// AC1: no source in worktree-gate reads DAT_MERGE_GATE or
// GIT_TOOLS_WORKTREE_GATE_ENFORCE, and the rollout gating is removed.
func TestAC1_NoRolloutEnvSurface(t *testing.T) {
	root := ".." // worktree-gate/
	var hits []string
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if strings.HasSuffix(path, "acceptance_verification_test.go") {
			// This file itself names the banned env vars in its own check
			// and failure message -- not a source read of them.
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		s := string(b)
		if strings.Contains(s, "DAT_MERGE_GATE") || strings.Contains(s, "GIT_TOOLS_WORKTREE_GATE_ENFORCE") {
			hits = append(hits, path)
		}
		return nil
	})
	if len(hits) > 0 {
		t.Fatalf("AC1 FAIL: rollout/merge-gate env surface still present in: %v", hits)
	}
}

// AC2: a classifier (verbs) data failure on the Bash axis must DENY, not
// fail open/Degraded.
func TestAC2_Bash_VerbsErr_MustDeny(t *testing.T) {
	fs := primaryFS()
	verbsErr := errors.New("verbs.json is corrupt")
	d := Decide(fs.lstat, fs.readFile, Verbs{}, verbsErr, TrackingDocs{}, nil, Input{
		ToolName: "Bash", CWD: "/repo", Command: "rm -rf build",
	})
	if !d.Deny {
		t.Fatalf("AC2 FAIL: verbsErr on a primary-checkout Bash call did not deny (fail-open): %+v", d)
	}
}

// AC2: a tracking-doc data failure on the Write/Edit axis must DENY, not
// fail open/Degraded.
func TestAC2_Write_TrackingDocsErr_MustDeny(t *testing.T) {
	fs := primaryFS()
	tdErr := errors.New("trackingdocs.json is corrupt")
	d := Decide(fs.lstat, fs.readFile, Verbs{}, nil, TrackingDocs{}, tdErr, Input{
		ToolName: "Write", FilePath: "/repo/plan.md", ProjectDir: "/repo",
	})
	if !d.Deny {
		t.Fatalf("AC2 FAIL: trackingDocsErr on a primary-checkout Write did not deny (fail-open): %+v", d)
	}
}

// AC4: decideBash must resolve the effective cwd per SC-CWD-RESOLVER-CONTRACT
// (a leading cd into a worktree before the git/write call), not solely
// trust in.CWD. Session cwd is the primary checkout; the command cd's into
// a worktree before running a destructive command -- the effective (not
// session) cwd governs, so this must be ALLOWED.
func TestAC4_DecideBash_UsesEffectiveCWD_NotSessionCWDAlone(t *testing.T) {
	fs := newFakeFS().dir("/repo/.git").file("/repo/wt/.git", "gitdir: /repo/.git/worktrees/wt\n")
	v := testVerbs(t)
	d := Decide(fs.lstat, fs.readFile, v, nil, TrackingDocs{}, nil, Input{
		ToolName: "Bash", CWD: "/repo", Command: "cd /repo/wt && rm -rf build",
	})
	if d.Deny {
		t.Fatalf("AC4 FAIL: decideBash denied a command whose effective cwd (via leading cd) is a worktree; it evaluated session CWD (%q) instead of the resolved effective cwd: %s", "/repo", d.Reason)
	}
}

// AC4: cwd-corpus.json fixture must exist at the declared path and be
// non-empty (required file_surface entry).
func TestAC4_CwdCorpusFixture_Present(t *testing.T) {
	b, err := os.ReadFile("testdata/cwd-corpus.json")
	if err != nil {
		t.Fatalf("AC4 FAIL: worktree-gate/detect/testdata/cwd-corpus.json missing: %v", err)
	}
	if len(strings.TrimSpace(string(b))) == 0 {
		t.Fatal("AC4 FAIL: cwd-corpus.json exists but is empty")
	}
}
