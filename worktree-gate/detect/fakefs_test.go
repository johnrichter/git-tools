package detect

import (
	"io/fs"
	"time"
)

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

func (f *fakeFS) lstat(path string) (fs.FileInfo, error) {
	n, ok := f.nodes[path]
	if !ok {
		return nil, fs.ErrNotExist
	}
	if n.statErr != nil {
		return nil, n.statErr
	}
	return fakeFileInfo{name: path, dir: n.dir, mode: n.mode}, nil
}

func (f *fakeFS) readFile(path string) ([]byte, error) {
	n, ok := f.nodes[path]
	if !ok {
		return nil, fs.ErrNotExist
	}
	if n.statErr != nil {
		return nil, n.statErr
	}
	return n.content, nil
}
