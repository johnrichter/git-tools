package detect

import (
	"io/fs"
	"path/filepath"
	"syscall"
	"time"
)

// noEnv stands in for a getenv with every variable unset -- the default a
// test opts out of unless it's specifically exercising an environment
// override.
func noEnv(string) string { return "" }

// fakeFileInfo is a minimal fs.FileInfo for test fixtures.
type fakeFileInfo struct {
	name string
	dir  bool
	mode fs.FileMode
}

func (f fakeFileInfo) Name() string       { return f.name }
func (f fakeFileInfo) Size() int64        { return 0 }
func (f fakeFileInfo) Mode() fs.FileMode  { return f.mode }
func (f fakeFileInfo) ModTime() time.Time { return time.Time{} }
func (f fakeFileInfo) IsDir() bool        { return f.dir }
func (f fakeFileInfo) Sys() any           { return nil }

// fakeNode is one entry in a fakeFS.
type fakeNode struct {
	dir     bool
	mode    fs.FileMode
	content []byte
	// statErr, if set, is returned by lstat instead of a normal result --
	// used to simulate an indeterminate filesystem error (e.g. permission
	// denied) distinct from "not found".
	statErr error
}

// fakeFS is a hermetic, in-memory stand-in for the filesystem primitives
// this package depends on, keyed by exact path.
type fakeFS struct {
	nodes map[string]fakeNode
}

func newFakeFS() *fakeFS { return &fakeFS{nodes: map[string]fakeNode{}} }

func (f *fakeFS) dir(path string) *fakeFS {
	f.nodes[path] = fakeNode{dir: true, mode: fs.ModeDir}
	return f
}

func (f *fakeFS) file(path, content string) *fakeFS {
	f.nodes[path] = fakeNode{content: []byte(content), mode: 0}
	return f
}

func (f *fakeFS) errAt(path string, err error) *fakeFS {
	f.nodes[path] = fakeNode{statErr: err}
	return f
}

// device registers a non-regular, non-directory node -- the shape /dev/null
// has, and one a `.git` probe descends through exactly like a regular file.
func (f *fakeFS) device(path string) *fakeFS {
	f.nodes[path] = fakeNode{mode: fs.ModeDevice | fs.ModeCharDevice}
	return f
}

func (f *fakeFS) lstat(path string) (fs.FileInfo, error) {
	n, ok := f.nodes[path]
	if !ok {
		return nil, f.absentErr(path)
	}
	if n.statErr != nil {
		return nil, n.statErr
	}
	return fakeFileInfo{name: path, dir: n.dir, mode: n.mode}, nil
}

func (f *fakeFS) readFile(path string) ([]byte, error) {
	n, ok := f.nodes[path]
	if !ok {
		return nil, f.absentErr(path)
	}
	if n.statErr != nil {
		return nil, n.statErr
	}
	return n.content, nil
}

// absentErr answers for a path holding no node, distinguishing the two errnos
// a real filesystem distinguishes: a path whose ancestry runs through a
// non-directory is ENOTDIR, anything else is ENOENT. The distinction is
// load-bearing -- a write target that is itself an existing file makes its own
// `.git` probe an ENOTDIR, and errors.Is must see it through the *fs.PathError
// wrapper os.Lstat returns.
func (f *fakeFS) absentErr(path string) error {
	for parent := filepath.Dir(path); ; parent = filepath.Dir(parent) {
		if n, ok := f.nodes[parent]; ok && n.statErr == nil && !n.dir {
			return &fs.PathError{Op: "lstat", Path: path, Err: syscall.ENOTDIR}
		}
		if up := filepath.Dir(parent); up == parent {
			return fs.ErrNotExist
		}
	}
}
