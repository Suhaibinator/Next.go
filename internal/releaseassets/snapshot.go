package releaseassets

import (
	"bytes"
	"io"
	"io/fs"
	"path"
	"sort"
	"time"
)

type entry struct {
	name     string
	dir      bool
	data     []byte
	children []*entry
}
type snapshot struct{ nodes map[string]*entry }

func newSnapshot(nodes map[string]*entry) *snapshot {
	for name, node := range nodes {
		if name != "." {
			parent := nodes[path.Dir(name)]
			parent.children = append(parent.children, node)
		}
	}
	for _, node := range nodes {
		sort.Slice(node.children, func(i, j int) bool { return node.children[i].name < node.children[j].name })
	}
	return &snapshot{nodes: nodes}
}
func (s *snapshot) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	node := s.nodes[name]
	if node == nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return &opened{node: node, reader: bytes.NewReader(node.data)}, nil
}
func (s *snapshot) Stat(name string) (fs.FileInfo, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrInvalid}
	}
	node := s.nodes[name]
	if node == nil {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
	}
	return node.info(), nil
}
func (s *snapshot) ReadDir(name string) ([]fs.DirEntry, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrInvalid}
	}
	node := s.nodes[name]
	if node == nil {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrNotExist}
	}
	if !node.dir {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrInvalid}
	}
	return dirEntries(node.children), nil
}
func dirEntries(nodes []*entry) []fs.DirEntry {
	result := make([]fs.DirEntry, len(nodes))
	for i, node := range nodes {
		result[i] = node.info()
	}
	return result
}
func (e *entry) info() fileInfo {
	return fileInfo{name: path.Base(e.name), size: int64(len(e.data)), dir: e.dir}
}

type fileInfo struct {
	name string
	size int64
	dir  bool
}

func (i fileInfo) Name() string { return i.name }
func (i fileInfo) Size() int64  { return i.size }
func (i fileInfo) Mode() fs.FileMode {
	if i.dir {
		return fs.ModeDir | 0555
	}
	return 0444
}
func (i fileInfo) ModTime() time.Time         { return time.Unix(0, 0).UTC() }
func (i fileInfo) IsDir() bool                { return i.dir }
func (i fileInfo) Sys() any                   { return nil }
func (i fileInfo) Type() fs.FileMode          { return i.Mode().Type() }
func (i fileInfo) Info() (fs.FileInfo, error) { return i, nil }

type opened struct {
	node   *entry
	reader *bytes.Reader
	offset int
	closed bool
}

func (f *opened) failure(op string, err error) error {
	return &fs.PathError{Op: op, Path: f.node.name, Err: err}
}
func (f *opened) Close() error {
	if f.closed {
		return f.failure("close", fs.ErrClosed)
	}
	f.closed = true
	return nil
}
func (f *opened) Stat() (fs.FileInfo, error) {
	if f.closed {
		return nil, f.failure("stat", fs.ErrClosed)
	}
	return f.node.info(), nil
}
func (f *opened) Read(data []byte) (int, error) {
	if f.closed {
		return 0, f.failure("read", fs.ErrClosed)
	}
	if f.node.dir {
		return 0, f.failure("read", fs.ErrInvalid)
	}
	return f.reader.Read(data)
}
func (f *opened) ReadAt(data []byte, offset int64) (int, error) {
	if f.closed {
		return 0, f.failure("readat", fs.ErrClosed)
	}
	if f.node.dir {
		return 0, f.failure("readat", fs.ErrInvalid)
	}
	return f.reader.ReadAt(data, offset)
}
func (f *opened) Seek(offset int64, whence int) (int64, error) {
	if f.closed {
		return 0, f.failure("seek", fs.ErrClosed)
	}
	if f.node.dir {
		return 0, f.failure("seek", fs.ErrInvalid)
	}
	return f.reader.Seek(offset, whence)
}
func (f *opened) ReadDir(n int) ([]fs.DirEntry, error) {
	if f.closed {
		return nil, f.failure("readdir", fs.ErrClosed)
	}
	if !f.node.dir {
		return nil, f.failure("readdir", fs.ErrInvalid)
	}
	if f.offset >= len(f.node.children) {
		if n > 0 {
			return nil, io.EOF
		}
		return []fs.DirEntry{}, nil
	}
	end := len(f.node.children)
	if n > 0 && n < end-f.offset {
		end = f.offset + n
	}
	result := dirEntries(f.node.children[f.offset:end])
	f.offset = end
	return result, nil
}

var _ fs.FS = (*snapshot)(nil)
var _ fs.StatFS = (*snapshot)(nil)
var _ fs.ReadDirFS = (*snapshot)(nil)
var _ fs.ReadDirFile = (*opened)(nil)
var _ io.ReadSeeker = (*opened)(nil)
var _ io.ReaderAt = (*opened)(nil)
