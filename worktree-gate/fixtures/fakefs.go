package fixtures

import (
	"errors"
	"io/fs"
	"path/filepath"
	"time"

	"github.com/johnrichter/git-tools/worktree-gate/detect"
)

// errSimulatedFSFailure stands in for a real filesystem error (e.g.
// permission denied) that FindRepoRoot must treat as indeterminate, never
// as "not found".
var errSimulatedFSFailure = errors.New("fixtures: simulated filesystem failure")

type fakeFileInfo struct {
	dir bool
}

func (f fakeFileInfo) Name() string       { return "" }
func (f fakeFileInfo) Size() int64        { return 0 }
func (f fakeFileInfo) Mode() fs.FileMode  { return 0 }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return f.dir }
func (f fakeFileInfo) Sys() any           { return nil }

// buildFS renders the `.git`-entry layout c.Topology names, anchored at
// c.anchorDir so detect.FindRepoRoot's upward walk resolves it exactly as
// it would on a real filesystem.
func buildFS(c Case) (detect.LstatFunc, detect.ReadFileFunc) {
	gitEntry := filepath.Join(c.anchorDir(), ".git")

	switch c.Topology {
	case Primary:
		return dirAt(gitEntry)
	case WorktreeEntry:
		target := "gitdir: /repo/.git/worktrees/" + filepath.Base(c.anchorDir()) + "\n"
		return fileAt(gitEntry, target)
	case Indeterminate:
		// present, regular, but not a "gitdir: ..." line -- a shape
		// ClassifyGitEntry doesn't recognize.
		return fileAt(gitEntry, "not a gitdir line\n")
	case FSErr:
		return errAt(gitEntry, errSimulatedFSFailure)
	default: // NoRepo: no `.git` entry anywhere: the walk reaches root.
		return emptyFS()
	}
}

func emptyFS() (detect.LstatFunc, detect.ReadFileFunc) {
	lstat := func(string) (fs.FileInfo, error) { return nil, fs.ErrNotExist }
	readFile := func(string) ([]byte, error) { return nil, fs.ErrNotExist }
	return lstat, readFile
}

func dirAt(path string) (detect.LstatFunc, detect.ReadFileFunc) {
	lstat := func(name string) (fs.FileInfo, error) {
		if name == path {
			return fakeFileInfo{dir: true}, nil
		}
		return nil, fs.ErrNotExist
	}
	readFile := func(string) ([]byte, error) { return nil, fs.ErrNotExist }
	return lstat, readFile
}

func fileAt(path, content string) (detect.LstatFunc, detect.ReadFileFunc) {
	lstat := func(name string) (fs.FileInfo, error) {
		if name == path {
			return fakeFileInfo{}, nil
		}
		return nil, fs.ErrNotExist
	}
	readFile := func(name string) ([]byte, error) {
		if name == path {
			return []byte(content), nil
		}
		return nil, fs.ErrNotExist
	}
	return lstat, readFile
}

func errAt(path string, err error) (detect.LstatFunc, detect.ReadFileFunc) {
	lstat := func(name string) (fs.FileInfo, error) {
		if name == path {
			return nil, err
		}
		return nil, fs.ErrNotExist
	}
	readFile := func(string) ([]byte, error) { return nil, fs.ErrNotExist }
	return lstat, readFile
}
