package detect

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
)

// LstatFunc reports file metadata without following symlinks -- the single
// filesystem primitive repo-root detection needs. os.Lstat satisfies it.
type LstatFunc func(name string) (fs.FileInfo, error)

// ReadFileFunc reads a file's full contents. os.ReadFile satisfies it.
type ReadFileFunc func(name string) ([]byte, error)

// ErrIndeterminate wraps a filesystem error, other than "not found", that
// stopped repo-root detection from reaching a confident answer. Callers
// must treat a wrapping error as uncertain, never as "no repo found".
var ErrIndeterminate = errors.New("worktree-gate: repo membership indeterminate")

// FindRepoRoot walks up from dir looking for a `.git` entry -- the marker
// of a repository's primary checkout or a linked worktree's redirect file.
// The walk is purely lexical: it never requires an intermediate directory
// to exist, so a target under a not-yet-created directory still resolves
// against the same repo its nearest existing ancestor belongs to.
//
// found is false only when the walk reaches the filesystem root without
// locating a `.git` entry -- dir is confidently outside any repository. A
// non-nil error means the walk hit a filesystem error other than "not
// exist" and could not reach either answer.
func FindRepoRoot(lstat LstatFunc, dir string) (root, gitEntry string, found bool, err error) {
	dir = filepath.Clean(dir)
	for {
		candidate := filepath.Join(dir, ".git")
		switch _, statErr := lstat(candidate); {
		case statErr == nil:
			return dir, candidate, true, nil
		case errors.Is(statErr, fs.ErrNotExist):
			// no entry here; keep climbing
		default:
			return "", "", false, fmt.Errorf("%w: %s: %v", ErrIndeterminate, candidate, statErr)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", "", false, nil
		}
		dir = parent
	}
}

// GitEntryKind classifies a resolved `.git` entry.
type GitEntryKind int

const (
	// KindIndeterminate means the entry's type or contents could not be
	// read confidently. Callers must fail closed on this result, never
	// treat it as either a primary checkout or a worktree.
	KindIndeterminate GitEntryKind = iota
	// KindPrimary is a repository's own `.git` directory.
	KindPrimary
	// KindWorktree is a linked worktree's `.git` redirect file.
	KindWorktree
)

// ClassifyGitEntry decides whether a resolved `.git` entry belongs to a
// repository's primary checkout (a directory) or a linked worktree (a
// regular file whose single line reads "gitdir: <repo>/.git/worktrees/<name>",
// per git's own worktree layout).
func ClassifyGitEntry(lstat LstatFunc, readFile ReadFileFunc, gitEntryPath string) GitEntryKind {
	info, err := lstat(gitEntryPath)
	if err != nil {
		return KindIndeterminate
	}
	if info.IsDir() {
		return KindPrimary
	}
	if !info.Mode().IsRegular() {
		return KindIndeterminate
	}

	contents, err := readFile(gitEntryPath)
	if err != nil {
		return KindIndeterminate
	}
	const prefix = "gitdir:"
	line := strings.TrimSpace(string(contents))
	if !strings.HasPrefix(line, prefix) {
		return KindIndeterminate
	}
	target := strings.TrimSpace(strings.TrimPrefix(line, prefix))
	if strings.Contains(filepath.ToSlash(target), "/worktrees/") {
		return KindWorktree
	}
	return KindIndeterminate
}

// namedPathKind classifies where an absolute path a Bash command names
// resolves -- the primitive SC20's named-path rule judges a write-class
// piece's targets against. The walk starts at absPath itself, not its parent,
// so a target that IS a repository root (rm -rf <primary>) classifies as well
// as one under it. found is false when the path sits confidently outside any
// repository; a non-nil error means membership could not be determined and the
// caller must fail closed.
func namedPathKind(lstat LstatFunc, readFile ReadFileFunc, absPath string) (kind GitEntryKind, found bool, err error) {
	_, gitEntry, found, err := FindRepoRoot(lstat, absPath)
	if err != nil || !found {
		return KindIndeterminate, found, err
	}
	return ClassifyGitEntry(lstat, readFile, gitEntry), true, nil
}
