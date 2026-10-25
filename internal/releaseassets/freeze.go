// Package releaseassets snapshots the public byte tree of one deployable native
// release. It never retains the source FS or exposes its private sibling trees.
// Freeze checks cancellation between FS calls; an arbitrary FS which blocks in
// Open, Stat, Read or ReadDir cannot be preempted. Sources must remain stable
// during Freeze. Observed metadata/size changes are rejected, rather than treated
// as an atomic snapshot of concurrently modified files.
package releaseassets

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	_ "github.com/Suhaibinator/Next.go/serveronly"
)

const (
	// MaxBytes bounds owned file payloads. Directory/map metadata is separately
	// bounded by MaxEntries and MaxPathBytes; this is not an RSS guarantee.
	MaxBytes     = 128 << 20
	MaxFileBytes = 32 << 20
	// MaxEntries includes the synthetic root and every selected directory/file.
	MaxEntries   = 16384
	MaxPathBytes = 4096
	MaxDepth     = 64
)

type freezer struct {
	ctx     context.Context
	source  fs.FS
	runtime string
	nodes   map[string]*entry
	bytes   int64
}

// Freeze admits exactly public/** and _nextgo/<runtimeID>/**. The runtime tree
// must exist; every other root/runtime is rejected, so passing a project or private
// artifact FS fails closed. Selected symlinks, special files and path aliases
// are rejected. The three runtime files app.wasm, wasm_exec.js and boot.js must
// be regular and nonempty. runtimeID is exactly20 lowercase hexadecimal bytes.
//
// The returned FS owns its bytes, has independent read cursors and fixed read-
// only metadata. It implements Stat/ReadDir without exposing a mutable map or
// data buffer. HTTP callers must reject directory requests before FileServer.
//
// Empty source directories are validated but omitted, matching Go embed's file
// semantics. Ancestors of files and the fixed root/public/_nextgo/runtime roots
// are materialized; public is synthetic when absent. Empty files participate.
//
// The lowercase SHA256 identity is domain-separated by nextgo.releaseassets.v1
// followed by NUL. The materialized entries, including root and empty files,
// are sorted by UTF8 path bytes. Each entry encodes one kind byte ('d' or 'f'),
// a big-endian uint64 path-byte count, the path bytes, a big-endian uint64 data
// length, and32 content-digest bytes (zero for directories). Source permissions,
// timestamps and enumeration order never enter this canonical identity.
func Freeze(ctx context.Context, fsys fs.FS, runtimeID string) (fs.FS, string, error) {
	if ctx == nil || fsys == nil || !validRuntime(runtimeID) {
		return nil, "", fmt.Errorf("releaseassets: context, filesystem and exact20 lowercase hexadecimal runtime ID required")
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	f := &freezer{ctx: ctx, source: fsys, runtime: runtimeID, nodes: map[string]*entry{}}
	if err := f.visit(".", nil, 0); err != nil {
		return nil, "", err
	}
	for _, name := range []string{"_nextgo", "_nextgo/" + runtimeID} {
		node := f.nodes[name]
		if node == nil || !node.dir {
			return nil, "", fmt.Errorf("releaseassets: missing directory %s", name)
		}
	}
	for _, name := range []string{"app.wasm", "wasm_exec.js", "boot.js"} {
		node := f.nodes["_nextgo/"+runtimeID+"/"+name]
		if node == nil || node.dir || len(node.data) == 0 {
			return nil, "", fmt.Errorf("releaseassets: missing nonempty runtime file %s", name)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, "", err
	}
	if f.nodes["public"] == nil && len(f.nodes) >= MaxEntries {
		return nil, "", fmt.Errorf("releaseassets: entry limit exceeded by synthetic public root")
	}
	nodes := materializedTree(f.nodes)
	frozen := newSnapshot(nodes)
	return frozen, treeDigest(nodes), nil
}

func materializedTree(source map[string]*entry) map[string]*entry {
	nodes := map[string]*entry{".": source["."], "public": {name: "public", dir: true}}
	for name, node := range source {
		if node.dir {
			continue
		}
		for current := name; ; current = path.Dir(current) {
			nodes[current] = source[current]
			if current == "." {
				break
			}
		}
	}
	return nodes
}
func validRuntime(id string) bool {
	if len(id) != 20 {
		return false
	}
	for _, c := range id {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
func validName(name string) bool {
	return name != "" && name != "." && name != ".." && utf8.ValidString(name) && !strings.ContainsAny(name, "/\\%?#") && strings.IndexFunc(name, unicode.IsControl) < 0
}
func sameInfo(a, b fs.FileInfo) bool {
	return a != nil && b != nil && a.Name() == b.Name() && a.Size() == b.Size() && a.Mode() == b.Mode() && a.ModTime().Equal(b.ModTime())
}

// FileInfo is an interface and custom filesystems can return a view backed by
// mutable state. Record each observation now, before invoking the FS again.
type sourceInfo struct {
	name     string
	size     int64
	mode     fs.FileMode
	modified time.Time
}

func observedInfo(info fs.FileInfo) fs.FileInfo {
	if info == nil {
		return nil
	}
	return sourceInfo{name: info.Name(), size: info.Size(), mode: info.Mode(), modified: info.ModTime()}
}
func (i sourceInfo) Name() string       { return i.name }
func (i sourceInfo) Size() int64        { return i.size }
func (i sourceInfo) Mode() fs.FileMode  { return i.mode }
func (i sourceInfo) ModTime() time.Time { return i.modified }
func (i sourceInfo) IsDir() bool        { return i.mode.IsDir() }
func (i sourceInfo) Sys() any           { return nil }
func (f *freezer) check() error         { return f.ctx.Err() }
func (f *freezer) allowed(name string) bool {
	if name == "." || name == "public" || name == "_nextgo" {
		return true
	}
	return strings.HasPrefix(name, "public/") || name == "_nextgo/"+f.runtime || strings.HasPrefix(name, "_nextgo/"+f.runtime+"/")
}
func (f *freezer) visit(name string, listed fs.FileInfo, depth int) (err error) {
	if err = f.check(); err != nil {
		return err
	}
	if !fs.ValidPath(name) || len(name) > MaxPathBytes || depth > MaxDepth || !f.allowed(name) {
		return fmt.Errorf("releaseassets: disallowed asset path %q", name)
	}
	if len(f.nodes) >= MaxEntries {
		return fmt.Errorf("releaseassets: entry limit exceeded")
	}
	if _, exists := f.nodes[name]; exists {
		return fmt.Errorf("releaseassets: duplicate asset path %q", name)
	}
	if listed != nil && listed.Mode().Type() != 0 && listed.Mode().Type() != fs.ModeDir {
		return fmt.Errorf("releaseassets: nonregular asset %s", name)
	}
	file, err := f.source.Open(name)
	if err != nil {
		return fmt.Errorf("releaseassets: open %s: %w", name, err)
	}
	if file == nil {
		return fmt.Errorf("releaseassets: nil asset file %s", name)
	}
	defer func() {
		if closeErr := file.Close(); err == nil && closeErr != nil {
			err = fmt.Errorf("releaseassets: close %s: %w", name, closeErr)
		}
	}()
	if err = f.check(); err != nil {
		return err
	}
	before, err := file.Stat()
	if err != nil {
		return fmt.Errorf("releaseassets: stat %s: %w", name, err)
	}
	before = observedInfo(before)
	if before == nil || name != "." && before.Name() != path.Base(name) || before.Mode().Type() != 0 && before.Mode().Type() != fs.ModeDir || listed != nil && !sameInfo(listed, before) {
		return fmt.Errorf("releaseassets: inconsistent or nonregular asset %s", name)
	}
	if (name == "." || name == "public" || name == "_nextgo" || name == "_nextgo/"+f.runtime) && !before.IsDir() {
		return fmt.Errorf("releaseassets: required asset directory is not a directory: %s", name)
	}
	node := &entry{name: strings.Clone(name), dir: before.IsDir()}
	f.nodes[name] = node
	if node.dir {
		if err = f.directory(name, file, depth); err != nil {
			return err
		}
	} else {
		size := before.Size()
		if size < 0 || size > MaxFileBytes || size > MaxBytes-f.bytes {
			return fmt.Errorf("releaseassets: byte limit exceeded at %s", name)
		}
		node.data = make([]byte, int(size))
		if err = f.readExact(file, node.data); err != nil {
			return fmt.Errorf("releaseassets: read %s: %w", name, err)
		}
		f.bytes += size
	}
	if err = f.check(); err != nil {
		return err
	}
	after, err := file.Stat()
	if err != nil {
		return fmt.Errorf("releaseassets: restat %s: %w", name, err)
	}
	after = observedInfo(after)
	if !sameInfo(before, after) {
		return fmt.Errorf("releaseassets: asset changed during snapshot: %s", name)
	}
	return f.check()
}
func (f *freezer) directory(name string, file fs.File, depth int) error {
	reader, ok := file.(fs.ReadDirFile)
	if !ok {
		return fmt.Errorf("releaseassets: directory cannot be enumerated: %s", name)
	}
	seen := map[string]bool{}
	for {
		if err := f.check(); err != nil {
			return err
		}
		// Read in bounded batches, never ask a source FS to materialize an entire
		// unbounded directory before we can enforce entry admission.
		batch, err := reader.ReadDir(128)
		if len(batch) > 128 {
			return fmt.Errorf("releaseassets: invalid directory batch: %s", name)
		}
		for _, child := range batch {
			if checkErr := f.check(); checkErr != nil {
				return checkErr
			}
			if child == nil {
				return fmt.Errorf("releaseassets: invalid or duplicate directory entry in %s", name)
			}
			childName := strings.Clone(child.Name())
			if !validName(childName) || seen[childName] {
				return fmt.Errorf("releaseassets: invalid or duplicate directory entry in %s", name)
			}
			seen[childName] = true
			full := childName
			if name != "." {
				full = name + "/" + childName
			}
			if !f.allowed(full) {
				return fmt.Errorf("releaseassets: disallowed asset path %q", full)
			}
			if child.Type() != 0 && child.Type() != fs.ModeDir {
				return fmt.Errorf("releaseassets: nonregular directory entry %s", childName)
			}
			info, infoErr := child.Info()
			if infoErr != nil {
				return fmt.Errorf("releaseassets: entry info %s: %w", childName, infoErr)
			}
			info = observedInfo(info)
			if info == nil || info.Name() != childName || info.Mode().Type() != child.Type() || info.IsDir() != child.IsDir() {
				return fmt.Errorf("releaseassets: inconsistent directory entry %s", childName)
			}
			if visitErr := f.visit(full, info, depth+1); visitErr != nil {
				return visitErr
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("releaseassets: enumerate %s: %w", name, err)
		}
		if len(batch) == 0 {
			return fmt.Errorf("releaseassets: directory made no progress: %s", name)
		}
	}
}
func (f *freezer) readExact(reader io.Reader, data []byte) error {
	offset, empty := 0, 0
	var extra [1]byte
	for {
		if err := f.check(); err != nil {
			return err
		}
		target := extra[:]
		if offset < len(data) {
			end := offset + 32<<10
			if end > len(data) {
				end = len(data)
			}
			target = data[offset:end]
		}
		n, err := reader.Read(target)
		if n < 0 || n > len(target) {
			return fmt.Errorf("invalid read count")
		}
		if offset == len(data) && n != 0 {
			return fmt.Errorf("file grew beyond declared size")
		}
		offset += n
		if err != nil {
			if errors.Is(err, io.EOF) && offset == len(data) {
				return nil
			}
			if errors.Is(err, io.EOF) {
				return io.ErrUnexpectedEOF
			}
			return err
		}
		if n == 0 {
			empty++
			if empty >= 100 {
				return io.ErrNoProgress
			}
		} else {
			empty = 0
		}
	}
}
func digestUint(h hash.Hash, value uint64) {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], value)
	_, _ = h.Write(b[:])
}
func treeDigest(nodes map[string]*entry) string {
	names := make([]string, 0, len(nodes))
	for name := range nodes {
		names = append(names, name)
	}
	sort.Strings(names)
	h := sha256.New()
	_, _ = h.Write([]byte("nextgo.releaseassets.v1\x00"))
	for _, name := range names {
		node := nodes[name]
		kind := byte('f')
		if node.dir {
			kind = 'd'
		}
		_, _ = h.Write([]byte{kind})
		digestUint(h, uint64(len(name)))
		_, _ = h.Write([]byte(name))
		digestUint(h, uint64(len(node.data)))
		var content [32]byte
		if !node.dir {
			content = sha256.Sum256(node.data)
		}
		_, _ = h.Write(content[:])
	}
	return hex.EncodeToString(h.Sum(nil))
}
