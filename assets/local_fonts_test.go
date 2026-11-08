package assets

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"
)

func localFontFixture(t testing.TB, format string) []byte {
	t.Helper()
	data, err := os.ReadFile("../examples/demo/localfonts/fixture." + format)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func localFontOptions(t testing.TB) LocalFontConfig {
	return LocalFontConfig{FS: fstest.MapFS{"source.woff2": {Data: localFontFixture(t, "woff2")}}, Fonts: []LocalFont{{File: "source.woff2", Face: Font{Family: "Snapshot Fixture"}}}}
}

func TestLocalFontsOwnBytesAndAllManifestValues(t *testing.T) {
	c := localFontOptions(t)
	adjust := 105.0
	c.Fonts[0].Face.SizeAdjust = &adjust
	set, err := NewLocalFonts(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(set.Manifest())
	manifest := set.Manifest()
	font := manifest.Faces[0]
	if font.Format != "woff2" || font.Source == "" || len(manifest.Assets) != 2 || len(set.Paths()) != 2 {
		t.Fatal("missing frozen font/CSS inventory", manifest)
	}
	original := append([]byte(nil), c.FS.(fstest.MapFS)["source.woff2"].Data...)
	c.FS.(fstest.MapFS)["source.woff2"].Data[0] = 0
	adjust = 2
	*manifest.Faces[0].SizeAdjust = 300
	manifest.Faces[0].Source = "/changed"
	manifest.Assets[0].URL = "/changed"
	paths := set.Paths()
	paths[0] = "/changed"
	after, _ := json.Marshal(set.Manifest())
	if !bytes.Equal(before, after) || bytes.Contains(after, []byte("source.woff2")) {
		t.Fatal("manifest retained source paths or borrowed state", string(after))
	}
	w := httptest.NewRecorder()
	set.ServeHTTP(w, httptest.NewRequest("GET", font.Source, nil))
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), original) {
		t.Fatal("serving reread mutable source bytes")
	}
}

func TestLocalFontsBindSourceBytesToFontAndStylesheetURLs(t *testing.T) {
	c := localFontOptions(t)
	first, err := NewLocalFonts(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	same, err := NewLocalFonts(context.Background(), c)
	if err != nil || first.css != same.css {
		t.Fatal("same inputs changed immutable URLs", err)
	}
	// A WOFF container version byte changes content identity without requiring
	// a glyph decoder. Browser decoding is covered by the separate fixture.
	c.FS.(fstest.MapFS)["source.woff2"].Data[27] ^= 1
	changed, err := NewLocalFonts(context.Background(), c)
	if err != nil || changed.css == first.css || changed.faces[0].Source == first.faces[0].Source {
		t.Fatal("edited bytes reused an immutable URL", err)
	}
	w := httptest.NewRecorder()
	first.ServeHTTP(w, httptest.NewRequest("GET", first.css, nil))
	if strings.Contains(w.Body.String(), LocalFontPrefix) || !strings.Contains(w.Body.String(), strings.TrimPrefix(first.faces[0].Source, LocalFontPrefix)) {
		t.Fatal("CSS must use relative frozen filenames for mounted paths", w.Body.String())
	}
}

func TestLocalFontsHTTPMetadataMethodsAndValidators(t *testing.T) {
	c := localFontOptions(t)
	c.FS.(fstest.MapFS)["source.woff"] = &fstest.MapFile{Data: localFontFixture(t, "woff")}
	c.Fonts = append(c.Fonts, LocalFont{File: "source.woff", Face: Font{Family: "WOFF Fixture"}})
	set, err := NewLocalFonts(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range set.Manifest().Assets {
		for _, method := range []string{"GET", "HEAD", "POST"} {
			w := httptest.NewRecorder()
			set.ServeHTTP(w, httptest.NewRequest(method, entry.URL, nil))
			if method == "POST" {
				if w.Code != 405 || w.Header().Get("Allow") != "GET, HEAD" || strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
					t.Fatal("invalid method/cache contract", w.Code, w.Header())
				}
				continue
			}
			if w.Code != 200 || w.Header().Get("ETag") != `"`+entry.SHA256+`"` || w.Header().Get("Content-Type") != entry.ContentType || w.Header().Get("Content-Length") != fmt.Sprint(entry.Bytes) || !strings.Contains(w.Header().Get("Cache-Control"), "immutable") || w.Header().Get("Access-Control-Allow-Origin") != "*" || w.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatal("font/CSS response identity", entry, w.Code, w.Header())
			}
			if method == "HEAD" && w.Body.Len() != 0 || method == "GET" && int64(w.Body.Len()) != entry.Bytes {
				t.Fatal("font/CSS body size")
			}
		}
		for _, tag := range []string{`"` + entry.SHA256 + `"`, `W/"` + entry.SHA256 + `"`, "*"} {
			r := httptest.NewRequest("GET", entry.URL, nil)
			r.Header.Set("If-None-Match", tag)
			w := httptest.NewRecorder()
			set.ServeHTTP(w, r)
			if w.Code != 304 || w.Body.Len() != 0 {
				t.Fatal("font/CSS conditional response", w.Code)
			}
		}
	}
	w := httptest.NewRecorder()
	set.ServeHTTP(w, httptest.NewRequest("GET", LocalFontPrefix+"unknown.woff", nil))
	if w.Code != 404 || strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Fatal("missing paths advertised immutable caching")
	}
}

func TestLocalFontsRejectUnsafeSourcesAndDeclarations(t *testing.T) {
	for _, name := range []string{"../source.woff2", "/source.woff2", "a/../source.woff2", "a\\source.woff2", "C:source.woff2", ".", "a\n.woff2", strings.Repeat("a/", 32) + "source"} {
		c := localFontOptions(t)
		c.Fonts[0].File = name
		if _, err := NewLocalFonts(context.Background(), c); err == nil {
			t.Fatal("unsafe font path accepted", name)
		}
	}
	for _, change := range []func(*LocalFontConfig){
		func(c *LocalFontConfig) { c.FS = nil },
		func(c *LocalFontConfig) { c.Fonts = nil },
		func(c *LocalFontConfig) { c.MaxFileBytes = -1 },
		func(c *LocalFontConfig) { c.MaxTotalBytes = 65 << 20 },
		func(c *LocalFontConfig) { c.Fonts[0].Face.Source = "/manual.woff2" },
		func(c *LocalFontConfig) { c.Fonts[0].Face.Format = "woff" },
		func(c *LocalFontConfig) { c.Fonts[0].Face.Family = "" },
		func(c *LocalFontConfig) { c.Fonts[0].Face.Weight = "9999" },
		func(c *LocalFontConfig) { c.Fonts = append(c.Fonts, c.Fonts[0]) },
		func(c *LocalFontConfig) { c.FS.(fstest.MapFS)["source.woff2"].Mode = fs.ModeSymlink },
		func(c *LocalFontConfig) { c.FS.(fstest.MapFS)["source.woff2"].Mode = fs.ModeNamedPipe },
	} {
		c := localFontOptions(t)
		change(&c)
		if _, err := NewLocalFonts(context.Background(), c); err == nil {
			t.Fatal("invalid font declaration accepted")
		}
	}
}

func TestLocalFontsBoundReadWorkAndReuseOneSourceAcrossFaces(t *testing.T) {
	c := localFontOptions(t)
	c.MaxFileBytes = int64(len(localFontFixture(t, "woff2"))) - 1
	if _, err := NewLocalFonts(context.Background(), c); err == nil {
		t.Fatal("font file byte bound ignored")
	}
	large := fstest.MapFS{"large.woff2": {Data: make([]byte, (4<<20)+1)}}
	if _, err := NewLocalFonts(context.Background(), LocalFontConfig{FS: large, Fonts: []LocalFont{{File: "large.woff2", Face: Font{Family: "Too large for default"}}}}); err == nil || !strings.Contains(err.Error(), "byte budget") {
		t.Fatal("default per-font byte bound did not reject before header validation", err)
	}
	c = localFontOptions(t)
	c.MaxTotalBytes = int64(len(localFontFixture(t, "woff2")))
	c.Fonts = append(c.Fonts, LocalFont{File: "source.woff2", Face: Font{Family: "Same bytes, different face"}})
	set, err := NewLocalFonts(context.Background(), c)
	if err != nil || len(set.files) != 2 || len(set.faces) != 2 {
		t.Fatal("same source consumed its byte budget twice", err)
	}
	c.FS.(fstest.MapFS)["second.woff2"] = &fstest.MapFile{Data: localFontFixture(t, "woff2")}
	c.Fonts[1].File = "second.woff2"
	if _, err := NewLocalFonts(context.Background(), c); err == nil {
		t.Fatal("total source byte bound ignored")
	}
	c = localFontOptions(t)
	for i := 0; i < 1025; i++ {
		c.FS.(fstest.MapFS)[fmt.Sprintf("a%04d", i)] = &fstest.MapFile{}
	}
	if _, err := NewLocalFonts(context.Background(), c); err == nil {
		t.Fatal("directory enumeration work bound ignored")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewLocalFonts(ctx, localFontOptions(t)); !errors.Is(err, context.Canceled) {
		t.Fatal("font construction ignored cancellation", err)
	}
}

func TestLocalFontsHeaderAdmission(t *testing.T) {
	for _, format := range []string{"woff", "woff2"} {
		data := localFontFixture(t, format)
		if got, err := localFontFormat(data); err != nil || got != format {
			t.Fatal("valid original container rejected", got, err)
		}
		for _, mutate := range []func([]byte){
			func(d []byte) { d[0] = 'x' },
			func(d []byte) { binary.BigEndian.PutUint32(d[8:12], uint32(len(d)+1)) },
			func(d []byte) { binary.BigEndian.PutUint16(d[12:14], 257) },
			func(d []byte) { binary.BigEndian.PutUint16(d[14:16], 1) },
			func(d []byte) { binary.BigEndian.PutUint32(d[16:20], 65<<20) },
		} {
			copy := append([]byte(nil), data...)
			mutate(copy)
			if _, err := localFontFormat(copy); err == nil {
				t.Fatal("invalid font container header accepted")
			}
		}
	}
}

func FuzzLocalFontContainerHeader(f *testing.F) {
	f.Add(localFontFixture(f, "woff"))
	f.Add(localFontFixture(f, "woff2"))
	f.Fuzz(func(t *testing.T, data []byte) {
		format, err := localFontFormat(data)
		if err == nil && format != "woff" && format != "woff2" {
			t.Fatal("unexpected admitted format", format)
		}
	})
}
