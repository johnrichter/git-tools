package detect

import (
	"errors"
	"io/fs"
	"syscall"
	"testing"
)

func TestFindRepoRoot_PrimaryCheckout(t *testing.T) {
	fs := newFakeFS().dir("/repo/.git")
	root, gitEntry, found, err := FindRepoRoot(fs.lstat, "/repo/a/b")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !found || root != "/repo" || gitEntry != "/repo/.git" {
		t.Errorf("FindRepoRoot() = root=%q entry=%q found=%v, want /repo, /repo/.git, true", root, gitEntry, found)
	}
}

func TestFindRepoRoot_NotYetCreatedDirectory(t *testing.T) {
	// The target directory doesn't exist yet; the walk must still resolve
	// against the nearest existing ancestor's repo.
	fs := newFakeFS().dir("/repo/.git")
	root, _, found, err := FindRepoRoot(fs.lstat, "/repo/new/nested/dir")
	if err != nil || !found || root != "/repo" {
		t.Fatalf("FindRepoRoot() over an uncreated dir = root=%q found=%v err=%v", root, found, err)
	}
}

func TestFindRepoRoot_NoRepo(t *testing.T) {
	fs := newFakeFS()
	_, _, found, err := FindRepoRoot(fs.lstat, "/tmp/scratch")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Error("expected no repo found")
	}
}

func TestFindRepoRoot_Indeterminate(t *testing.T) {
	permErr := errors.New("permission denied")
	fs := newFakeFS().errAt("/repo/sub/.git", permErr)
	_, _, found, err := FindRepoRoot(fs.lstat, "/repo/sub")
	if found {
		t.Error("expected found=false on an indeterminate error")
	}
	if !errors.Is(err, ErrIndeterminate) {
		t.Errorf("err = %v, want wrapping ErrIndeterminate", err)
	}
}

// TestFindRepoRoot_ProbeOutcomes pins every outcome the `.git`-probe lstat can
// produce, so the set of errnos allowed to keep the walk climbing stays a
// closed, reviewed list. Exactly two may: a missing entry, and ENOTDIR -- which
// proves the candidate is not a directory and therefore cannot be a repo root.
// Every other error leaves membership unknown and must fail closed, or the
// gate's whole conservative posture collapses into "keep climbing".
func TestFindRepoRoot_ProbeOutcomes(t *testing.T) {
	probeAt := "/repo/sub/.git"
	pathErr := func(errno syscall.Errno) error {
		return &fs.PathError{Op: "lstat", Path: probeAt, Err: errno}
	}

	for _, tc := range []struct {
		name      string
		probeErr  error // nil: the probed entry exists
		wantDeny  bool  // the caller-visible consequence of ErrIndeterminate
		wantRoot  string
		wantFound bool
	}{
		{name: "eacces-fails-closed", probeErr: pathErr(syscall.EACCES), wantDeny: true},
		{name: "eio-fails-closed", probeErr: pathErr(syscall.EIO), wantDeny: true},
		{name: "eloop-fails-closed", probeErr: pathErr(syscall.ELOOP), wantDeny: true},
		{name: "bare-error-fails-closed", probeErr: errors.New("permission denied"), wantDeny: true},
		{name: "nil-resolves-here", probeErr: nil, wantRoot: "/repo/sub", wantFound: true},
		{name: "enoent-climbs", probeErr: pathErr(syscall.ENOENT), wantRoot: "/repo", wantFound: true},
		{name: "enotdir-climbs", probeErr: pathErr(syscall.ENOTDIR), wantRoot: "/repo", wantFound: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fakeFS := newFakeFS().dir("/repo/.git")
			if tc.probeErr == nil {
				fakeFS.dir(probeAt)
			} else {
				fakeFS.errAt(probeAt, tc.probeErr)
			}

			root, _, found, err := FindRepoRoot(fakeFS.lstat, "/repo/sub")

			if tc.wantDeny {
				if !errors.Is(err, ErrIndeterminate) {
					t.Fatalf("err = %v, want wrapping ErrIndeterminate", err)
				}
				if found {
					t.Error("found = true on an indeterminate probe, want false")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if found != tc.wantFound || root != tc.wantRoot {
				t.Errorf("FindRepoRoot() = root=%q found=%v, want root=%q found=%v", root, found, tc.wantRoot, tc.wantFound)
			}
		})
	}
}

// TestFindRepoRoot_ExistingNonDirectoryTargets covers the shape that motivated
// the ENOTDIR arm, end to end through the fake filesystem's own errno modeling
// rather than an injected error: the walk starts AT a write target that already
// exists and is not a directory.
func TestFindRepoRoot_ExistingNonDirectoryTargets(t *testing.T) {
	fakeFS := newFakeFS().
		dir("/repo/.git").
		file("/repo/tracked.md", "contents\n").
		device("/dev/null")

	t.Run("existing-file-resolves-to-its-repo", func(t *testing.T) {
		root, _, found, err := FindRepoRoot(fakeFS.lstat, "/repo/tracked.md")
		if err != nil || !found || root != "/repo" {
			t.Fatalf("FindRepoRoot() = root=%q found=%v err=%v, want /repo, true, nil", root, found, err)
		}
	})

	t.Run("nested-under-existing-file-still-resolves", func(t *testing.T) {
		// Several ENOTDIR levels in a row must all climb, not just the first.
		root, _, found, err := FindRepoRoot(fakeFS.lstat, "/repo/tracked.md/a/b")
		if err != nil || !found || root != "/repo" {
			t.Fatalf("FindRepoRoot() = root=%q found=%v err=%v, want /repo, true, nil", root, found, err)
		}
	})

	t.Run("device-node-outside-any-repo", func(t *testing.T) {
		_, _, found, err := FindRepoRoot(fakeFS.lstat, "/dev/null")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if found {
			t.Error("found = true for /dev/null, want confidently outside any repo")
		}
	})
}

func TestClassifyGitEntry_Primary(t *testing.T) {
	fs := newFakeFS().dir("/repo/.git")
	if got := ClassifyGitEntry(fs.lstat, fs.readFile, "/repo/.git"); got != KindPrimary {
		t.Errorf("ClassifyGitEntry() = %v, want KindPrimary", got)
	}
}

func TestClassifyGitEntry_Worktree(t *testing.T) {
	fs := newFakeFS().file("/repo/.claude/worktrees/slug/.git", "gitdir: /repo/.git/worktrees/slug\n")
	got := ClassifyGitEntry(fs.lstat, fs.readFile, "/repo/.claude/worktrees/slug/.git")
	if got != KindWorktree {
		t.Errorf("ClassifyGitEntry() = %v, want KindWorktree", got)
	}
}

func TestClassifyGitEntry_UnrecognizedContent(t *testing.T) {
	fs := newFakeFS().file("/repo/.git", "not a gitdir line\n")
	got := ClassifyGitEntry(fs.lstat, fs.readFile, "/repo/.git")
	if got != KindIndeterminate {
		t.Errorf("ClassifyGitEntry() = %v, want KindIndeterminate on unrecognized content", got)
	}
}

func TestClassifyGitEntry_UnreadableFile(t *testing.T) {
	fs := newFakeFS().file("/repo/.git", "gitdir: /repo/.git/worktrees/slug\n")
	fs.nodes["/repo/.git"] = fakeNode{statErr: errors.New("boom")}
	got := ClassifyGitEntry(fs.lstat, fs.readFile, "/repo/.git")
	if got != KindIndeterminate {
		t.Errorf("ClassifyGitEntry() = %v, want KindIndeterminate on a stat error", got)
	}
}
