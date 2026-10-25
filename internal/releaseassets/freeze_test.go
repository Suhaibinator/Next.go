package releaseassets

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"
)

//go:embed all:testdata/embedassets
var embeddedAssets embed.FS

const testRuntime = "0123456789abcdef0123"

func tree() fstest.MapFS {
	return fstest.MapFS{
		"public/a.txt":                             {Data: []byte("public")},
		"public/empty.txt":                         {Data: []byte{}},
		"public/emptydir":                          {Mode: fs.ModeDir | 0755},
		"_nextgo/" + testRuntime + "/app.wasm":     {Data: []byte("wasm")},
		"_nextgo/" + testRuntime + "/boot.js":      {Data: []byte("boot")},
		"_nextgo/" + testRuntime + "/wasm_exec.js": {Data: []byte("shim")},
	}
}
func mustFreeze(t *testing.T, source fs.FS) (fs.FS, string) {
	t.Helper()
	frozen, digest, err := Freeze(context.Background(), source, testRuntime)
	if err != nil {
		t.Fatal(err)
	}
	return frozen, digest
}
func TestFreezeOwnedImmutableAndIndependentHandles(t *testing.T) {
	source := tree()
	frozen, digest := mustFreeze(t, source)
	if len(digest) != 64 {
		t.Fatal(digest)
	}
	source["public/a.txt"].Data[0] = 'X'
	source["public/a.txt"].Mode = 0777
	source["public/added"] = &fstest.MapFile{Data: []byte("new")}
	delete(source, "_nextgo/"+testRuntime+"/app.wasm")
	body, err := fs.ReadFile(frozen, "public/a.txt")
	if err != nil || string(body) != "public" {
		t.Fatal(string(body), err)
	}
	body[0] = 'Y'
	again, _ := fs.ReadFile(frozen, "public/a.txt")
	if string(again) != "public" {
		t.Fatal("returned read aliases snapshot")
	}
	if _, err := fs.Stat(frozen, "public/added"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("source mutation reached snapshot", err)
	}
	first, _ := frozen.Open("public/a.txt")
	defer first.Close()
	second, _ := frozen.Open("public/a.txt")
	defer second.Close()
	one := make([]byte, 1)
	first.Read(one)
	if string(one) != "p" {
		t.Fatal(string(one))
	}
	first.Read(one)
	second.Read(one)
	if string(one) != "p" {
		t.Fatal("open handles share cursor")
	}
	seeker := first.(io.Seeker)
	if _, err := seeker.Seek(0, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	info, _ := first.Stat()
	if info.Mode() != 0444 || info.ModTime() != time.Unix(0, 0).UTC() || info.Sys() != nil {
		t.Fatal(info)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Read(one); !errors.Is(err, fs.ErrClosed) {
		t.Fatal(err)
	}
	dir, err := fs.ReadDir(frozen, ".")
	if err != nil || len(dir) != 2 || dir[0].Name() != "_nextgo" || dir[1].Name() != "public" {
		t.Fatal(dir, err)
	}
	dir[0] = nil
	dir2, _ := fs.ReadDir(frozen, ".")
	if dir2[0] == nil {
		t.Fatal("directory result aliases tree")
	}
}
func TestFreezeDigestCanonicalAndBindsPathsEmptyFilesAndDirectories(t *testing.T) {
	source := tree()
	_, first := mustFreeze(t, source)
	// Independent format fixture; changing the canonical wire representation is
	// a release-contract change, rather than an unnoticed compiler/runtime drift.
	const golden = "5fcc4f2c1cf7a31891f0254f269111104dee802d696a68dd6cc007f4dfa54c1b"
	if first != golden {
		t.Fatalf("canonical digest changed: %s", first)
	}
	for _, file := range source {
		file.ModTime = time.Now()
		file.Mode |= 0600
	}
	_, reordered := mustFreeze(t, openWrapper{source: source, wrap: func(_ string, file fs.File) fs.File {
		if dir, ok := file.(fs.ReadDirFile); ok {
			return &reverseDir{ReadDirFile: dir}
		}
		return file
	}})
	if first != reordered {
		t.Fatal("metadata or directory order entered identity")
	}
	for _, change := range []func(fstest.MapFS){
		func(s fstest.MapFS) { s["public/a.txt"].Data = []byte("changed") },
		func(s fstest.MapFS) { s["public/b.txt"] = s["public/a.txt"]; delete(s, "public/a.txt") },
		func(s fstest.MapFS) { delete(s, "public/empty.txt") },
	} {
		variant := tree()
		change(variant)
		_, next := mustFreeze(t, variant)
		if next == first {
			t.Fatal("tree change did not change identity")
		}
	}
}
func TestFreezeRejectsInvalidInputNamespacesAndSpecialFiles(t *testing.T) {
	for _, runtime := range []string{"", strings.ToUpper(testRuntime), testRuntime + "0", "../" + testRuntime, strings.Repeat("z", 20)} {
		if _, _, err := Freeze(context.Background(), tree(), runtime); err == nil {
			t.Fatal("invalid runtime", runtime)
		}
	}
	if _, _, err := Freeze(nil, tree(), testRuntime); err == nil {
		t.Fatal("nil context")
	}
	if _, _, err := Freeze(context.Background(), nil, testRuntime); err == nil {
		t.Fatal("nil FS")
	}
	cases := map[string]func(fstest.MapFS){
		"missingcore": func(s fstest.MapFS) { delete(s, "_nextgo/"+testRuntime+"/app.wasm") },
		"emptyshim":   func(s fstest.MapFS) { s["_nextgo/"+testRuntime+"/wasm_exec.js"].Data = nil },
		"unknownruntime": func(s fstest.MapFS) {
			s["_nextgo/aaaaaaaaaaaaaaaaaaaa/app.wasm"] = &fstest.MapFile{Data: []byte("unknown")}
		},
		"unknownemptyruntime": func(s fstest.MapFS) {
			s["_nextgo/aaaaaaaaaaaaaaaaaaaa"] = &fstest.MapFile{Mode: fs.ModeDir}
		},
		"unknownemptyroot": func(s fstest.MapFS) {
			s["private"] = &fstest.MapFile{Mode: fs.ModeDir}
		},
		"aliasemptydir": func(s fstest.MapFS) {
			s["public/a%20b"] = &fstest.MapFile{Mode: fs.ModeDir}
		},
		"privateroot":  func(s fstest.MapFS) { s["private/prerender.json"] = &fstest.MapFile{Data: []byte("secret")} },
		"metadataroot": func(s fstest.MapFS) { s["metadata/robots.txt"] = &fstest.MapFile{Data: []byte("managed elsewhere")} },
		"source":       func(s fstest.MapFS) { s["main.go"] = &fstest.MapFile{Data: []byte("source")} },
		"symlink":      func(s fstest.MapFS) { s["public/link"] = &fstest.MapFile{Mode: fs.ModeSymlink} },
		"special":      func(s fstest.MapFS) { s["public/fifo"] = &fstest.MapFile{Mode: fs.ModeNamedPipe} },
		"alias":        func(s fstest.MapFS) { s["public/a%2fb"] = &fstest.MapFile{} },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			source := tree()
			change(source)
			frozen, digest, err := Freeze(context.Background(), source, testRuntime)
			if err == nil || frozen != nil || digest != "" {
				t.Fatal("invalid tree published", err, digest)
			}
		})
	}
}
func TestFreezeRealFilesystemAndSymlink(t *testing.T) {
	root := t.TempDir()
	for name, file := range tree() {
		full := filepath.Join(root, filepath.FromSlash(name))
		if file.Mode.IsDir() {
			if err := os.MkdirAll(full, 0755); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, file.Data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	frozen, digest := mustFreeze(t, os.DirFS(root))
	_, want := mustFreeze(t, tree())
	if digest != want {
		t.Fatal("real FS changed canonical identity")
	}
	if err := fstest.TestFS(frozen, "public/a.txt", "public/empty.txt", "_nextgo/"+testRuntime+"/app.wasm"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("a.txt", filepath.Join(root, "public", "link")); err != nil {
		t.Skip("symlink unavailable", err)
	}
	if _, _, err := Freeze(context.Background(), os.DirFS(root), testRuntime); err == nil {
		t.Fatal("real symlink accepted")
	}
}

type openWrapper struct {
	source fs.FS
	wrap   func(string, fs.File) fs.File
}

func (w openWrapper) Open(name string) (fs.File, error) {
	f, err := w.source.Open(name)
	if err != nil {
		return nil, err
	}
	return w.wrap(name, f), nil
}

type reverseDir struct{ fs.ReadDirFile }

func (d *reverseDir) ReadDir(n int) ([]fs.DirEntry, error) {
	entries, err := d.ReadDirFile.ReadDir(n)
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	return entries, err
}

type statInfo struct {
	fs.FileInfo
	size    *int64
	changed bool
}

func (s statInfo) Size() int64 {
	if s.size != nil {
		return *s.size
	}
	return s.FileInfo.Size()
}
func (s statInfo) ModTime() time.Time {
	if s.changed {
		return s.FileInfo.ModTime().Add(time.Second)
	}
	return s.FileInfo.ModTime()
}

type changedFile struct {
	fs.File
	stats int
}

func (f *changedFile) Stat() (fs.FileInfo, error) {
	info, err := f.File.Stat()
	f.stats++
	return statInfo{FileInfo: info, changed: f.stats > 1}, err
}

type readWrapper struct {
	fs.File
	read func([]byte) (int, error)
}

func (f *readWrapper) Read(data []byte) (int, error) { return f.read(data) }
func TestFreezeRejectsChangingMetadataReadGrowthAndCancellation(t *testing.T) {
	target := "public/a.txt"
	changed := openWrapper{source: tree(), wrap: func(name string, file fs.File) fs.File {
		if name == target {
			return &changedFile{File: file}
		}
		return file
	}}
	if _, _, err := Freeze(context.Background(), changed, testRuntime); err == nil || !strings.Contains(err.Error(), "changed during") {
		t.Fatal(err)
	}
	mutable := tree()
	mutableView := openWrapper{source: mutable, wrap: func(name string, file fs.File) fs.File {
		if name != target {
			return file
		}
		return &readWrapper{File: file, read: func(data []byte) (int, error) {
			mutable[target].ModTime = time.Unix(123, 0)
			return file.Read(data)
		}}
	}}
	if _, _, err := Freeze(context.Background(), mutableView, testRuntime); err == nil || !strings.Contains(err.Error(), "changed during") {
		t.Fatal("mutable FileInfo masked an observed metadata change", err)
	}
	for _, mode := range []string{"growth", "truncate", "noprogress", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			first := true
			source := openWrapper{source: tree(), wrap: func(name string, file fs.File) fs.File {
				if name != target {
					return file
				}
				return &readWrapper{File: file, read: func(data []byte) (int, error) {
					switch mode {
					case "growth":
						for i := range data {
							data[i] = 'x'
						}
						return len(data), nil
					case "truncate":
						return 0, io.EOF
					case "noprogress":
						return 0, nil
					default:
						if first {
							first = false
							cancel()
						}
						return file.Read(data)
					}
				}}
			}}
			frozen, digest, err := Freeze(ctx, source, testRuntime)
			if err == nil || frozen != nil || digest != "" {
				t.Fatal("invalid read published", mode, err)
			}
			if mode == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := Freeze(ctx, tree(), testRuntime); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
func TestFreezeEntryByteDepthAndPathBounds(t *testing.T) {
	source := tree()
	for i := 0; i < MaxEntries; i++ {
		source[fmt.Sprintf("public/f%05d", i)] = &fstest.MapFile{}
	}
	if _, _, err := Freeze(context.Background(), source, testRuntime); err == nil || !strings.Contains(err.Error(), "entry limit") {
		t.Fatal(err)
	}
	// Precharge the total budget to exercise the overflow boundary without
	// allocating a128-MiB fixture or reading bytes that admission must reject.
	sample := fstest.MapFS{"public/a.txt": {Data: []byte("abc")}}
	freezer := &freezer{ctx: context.Background(), source: sample, runtime: testRuntime, nodes: map[string]*entry{}, bytes: MaxBytes - 2}
	if err := freezer.visit("public/a.txt", nil, 1); err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatal(err)
	}
	source = tree()
	source["public/huge"] = &fstest.MapFile{Data: make([]byte, MaxFileBytes+1)}
	if _, _, err := Freeze(context.Background(), source, testRuntime); err == nil || !strings.Contains(err.Error(), "byte limit") {
		t.Fatal(err)
	}
	source = tree()
	source["public/"+strings.Repeat("a/", MaxDepth)+"leaf"] = &fstest.MapFile{}
	if _, _, err := Freeze(context.Background(), source, testRuntime); err == nil {
		t.Fatal("depth bound missing")
	}
	source = tree()
	source["public/"+strings.Repeat("p", MaxPathBytes)] = &fstest.MapFile{}
	if _, _, err := Freeze(context.Background(), source, testRuntime); err == nil {
		t.Fatal("path bound missing")
	}
}
func TestFrozenConcurrentIndependentReads(t *testing.T) {
	frozen, _ := mustFreeze(t, tree())
	var group sync.WaitGroup
	for i := 0; i < 16; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for n := 0; n < 20; n++ {
				body, err := fs.ReadFile(frozen, "public/a.txt")
				if err != nil || !bytes.Equal(body, []byte("public")) {
					t.Error(string(body), err)
				}
				entries, err := fs.ReadDir(frozen, "public")
				if err != nil || len(entries) != 2 {
					t.Error(entries, err)
				}
				if !reflect.DeepEqual(entries[0].Type(), fs.FileMode(0)) {
					t.Error(entries)
				}
			}
		}()
	}
	group.Wait()
}

func TestFreezeOSAndEmbeddedTreesAgreeAcrossEmptyDirectories(t *testing.T) {
	source, err := fs.Sub(embeddedAssets, "testdata/embedassets")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fs.Stat(source, "public"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("fixture must lack public in embed", err)
	}
	embedded, digest := mustFreeze(t, source)
	public, err := fs.ReadDir(embedded, "public")
	if err != nil || len(public) != 0 {
		t.Fatal("missing synthetic public root", public, err)
	}
	root := t.TempDir()
	if err := fs.WalkDir(source, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		target := filepath.Join(root, filepath.FromSlash(name))
		if entry.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		data, err := fs.ReadFile(source, name)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0644)
	}); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"public/empty/child", "_nextgo/" + testRuntime + "/empty/child"} {
		if err := os.MkdirAll(filepath.Join(root, filepath.FromSlash(dir)), 0755); err != nil {
			t.Fatal(err)
		}
	}
	frozen, withEmpty := mustFreeze(t, os.DirFS(root))
	if digest != withEmpty {
		t.Fatal("empty os directories differ from embedded artifact", digest, withEmpty)
	}
	if _, err := fs.Stat(frozen, "public/empty"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal("empty source directory entered materialized tree", err)
	}
	if err := os.RemoveAll(filepath.Join(root, "public")); err != nil {
		t.Fatal(err)
	}
	_, withoutPublic := mustFreeze(t, os.DirFS(root))
	if withoutPublic != digest {
		t.Fatal("absent public changed identity")
	}
	if err := os.MkdirAll(filepath.Join(root, "public"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "public", "empty.txt"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	_, withEmptyFile := mustFreeze(t, os.DirFS(root))
	if withEmptyFile == digest {
		t.Fatal("empty file omitted from identity")
	}
}
