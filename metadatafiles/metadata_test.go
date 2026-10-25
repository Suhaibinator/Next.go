//go:build !js

package metadatafiles

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io/fs"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Suhaibinator/Next.go/internal/paths"
	"github.com/Suhaibinator/Next.go/seo"
)

func pngData(t *testing.T, w, h int) []byte {
	t.Helper()
	var buffer bytes.Buffer
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	img.Set(0, 0, color.NRGBA{R: 127, A: 255})
	if err := png.Encode(&buffer, img); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}
func icoData(payload []byte, w, h int) []byte {
	data := make([]byte, 22)
	binary.LittleEndian.PutUint16(data[2:], 1)
	binary.LittleEndian.PutUint16(data[4:], 1)
	data[6], data[7] = byte(w), byte(h)
	binary.LittleEndian.PutUint16(data[10:], 1)
	binary.LittleEndian.PutUint16(data[12:], 32)
	binary.LittleEndian.PutUint32(data[14:], uint32(len(payload)))
	binary.LittleEndian.PutUint32(data[18:], 22)
	return append(data, payload...)
}
func sitemapData(t *testing.T) []byte {
	t.Helper()
	data, err := seo.MarshalSitemap([]seo.SitemapEntry{{URL: "https://example.test/base/", ChangeFrequency: "weekly"}})
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func fullFS(t *testing.T) fstest.MapFS {
	t.Helper()
	return fstest.MapFS{
		"robots.txt":           {Data: []byte("# Custom directive compatibility\nUser-agent: *\nDisallow: /private\nCrawl-delay: 2\nHost: example.test\n")},
		"sitemap.xml":          {Data: sitemapData(t)},
		"favicon.ico":          {Data: icoData(pngData(t, 16, 16), 16, 16)},
		"icon.png":             {Data: pngData(t, 32, 32)},
		"apple-icon.png":       {Data: pngData(t, 64, 64)},
		"manifest.webmanifest": {Data: []byte(`{"name":"Go app","start_url":"./","scope":"./","icons":[{"src":"icon.png","sizes":"32x32","type":"image/png"}]}`)},
	}
}

func TestSnapshotOwnershipHeadAndHTTP(t *testing.T) {
	source := fullFS(t)
	set, err := New(context.Background(), Sources{Static: source}, Options{BasePath: "/base"})
	if err != nil {
		t.Fatal(err)
	}
	entries := set.Entries()
	if len(entries) != 6 || len(set.Digest()) != 64 {
		t.Fatal(entries, set.Digest())
	}
	for _, entry := range entries {
		original := append([]byte(nil), source[entry.Name].Data...)
		if entry.Path != "/base/"+entry.Name || len(entry.Digest) != 64 || entry.ETag != `"`+entry.Digest+`"` || entry.Size != int64(len(original)) {
			t.Fatal(entry)
		}
		data, err := set.Read(entry.Name)
		if err != nil || !bytes.Equal(data, original) {
			t.Fatal(entry.Name, err)
		}
		if len(data) > 0 {
			data[0] ^= 255
		}
		if len(source[entry.Name].Data) > 0 {
			source[entry.Name].Data[0] ^= 255
		}
		for _, method := range []string{"GET", "HEAD"} {
			request := httptest.NewRequest(method, "https://example.test"+entry.Path+"?v=1", nil)
			request.Header.Set("Cookie", "session=private")
			w := httptest.NewRecorder()
			set.ServeHTTP(w, request)
			if w.Code != 200 || w.Header().Get("Content-Type") != entry.ContentType || w.Header().Get("ETag") != entry.ETag || w.Header().Get("X-Content-Type-Options") != "nosniff" || w.Header().Get("Content-Length") == "" {
				t.Fatal(entry, w.Code, w.Header())
			}
			if method == "GET" && !bytes.Equal(w.Body.Bytes(), original) || method == "HEAD" && w.Body.Len() != 0 {
				t.Fatal(method, entry.Name, "body mismatch")
			}
		}
		for _, conditional := range []string{entry.ETag, "W/" + entry.ETag, "*", `"other", ` + entry.ETag} {
			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET", entry.Path, nil)
			req.Header.Set("If-None-Match", conditional)
			set.ServeHTTP(w, req)
			if w.Code != 304 || w.Body.Len() != 0 {
				t.Fatal(conditional, w.Code)
			}
		}
		w := httptest.NewRecorder()
		set.ServeHTTP(w, httptest.NewRequest("POST", entry.Path, nil))
		if w.Code != 405 || w.Header().Get("Allow") != "GET, HEAD" {
			t.Fatal(w.Code, w.Header())
		}
	}
	entries[0].Name = "mutated"
	if set.Entries()[0].Name == "mutated" {
		t.Fatal("descriptor alias")
	}
	head := set.Head()
	if len(head) != 4 {
		t.Fatal(head)
	}
	for _, element := range head {
		if !strings.HasPrefix(element.Attrs["href"], "/base/") {
			t.Fatal(element)
		}
	}
	head[0].Attrs["href"] = "javascript:bad"
	if set.Head()[0].Attrs["href"] == "javascript:bad" {
		t.Fatal("head aliases")
	}
	for _, path := range []string{"/robots.txt", "/base/en/robots.txt", "/base/robots.txt/", "/base/%72obots.txt", "/assets/icon.png", "/missing"} {
		w := httptest.NewRecorder()
		set.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Fatal(path, w.Code)
		}
	}
	if _, err := set.Read("../robots.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
}

func TestStartupProducersOnceConflictsCancellationAndPanic(t *testing.T) {
	calls := 0
	sources := Sources{Robots: func(context.Context) (seo.RobotsOptions, error) {
		calls++
		return seo.RobotsOptions{Rules: []seo.RobotsRule{{Allow: []string{"/"}}}}, nil
	}, Sitemap: func(context.Context) ([]seo.SitemapEntry, error) {
		calls++
		return []seo.SitemapEntry{{URL: "https://example.test/"}}, nil
	}, Manifest: func(context.Context) (Manifest, error) { calls++; return Manifest{Name: "Go app", StartURL: "./"}, nil }}
	set, err := New(context.Background(), sources, Options{})
	if err != nil || calls != 3 {
		t.Fatal(calls, err)
	}
	for range 3 {
		w := httptest.NewRecorder()
		set.ServeHTTP(w, httptest.NewRequest("GET", "/robots.txt", nil))
	}
	if calls != 3 {
		t.Fatal("request-time producer executed")
	}
	sources.Static = fstest.MapFS{"robots.txt": {Data: []byte("User-agent: *\n")}}
	if _, err := New(context.Background(), sources, Options{}); err == nil {
		t.Fatal("static/Go conflict accepted")
	}
	if calls != 3 {
		t.Fatal("conflict executed producer")
	}
	sources.Static = fstest.MapFS{"manifest.webmanifest": {Data: []byte(`{"name":"static"}`)}}
	if _, err := New(context.Background(), sources, Options{}); err == nil || calls != 3 {
		t.Fatal("later inventory conflict executed earlier producer", calls, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := New(ctx, Sources{}, Options{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := New(nil, Sources{}, Options{}); err == nil {
		t.Fatal("nil context accepted")
	}
	if _, err := New(context.Background(), Sources{Robots: func(context.Context) (seo.RobotsOptions, error) { panic("private secret") }}, Options{}); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatal(err)
	}
	if _, err := New(context.Background(), Sources{}, Options{BasePath: "/../bad"}); err == nil {
		t.Fatal("invalid mount accepted")
	}
	_, err = New(context.Background(), Sources{Robots: func(ctx context.Context) (seo.RobotsOptions, error) {
		if got := paths.FromContext(ctx); got.BasePath != "/base" || got.AssetPrefix != "" || got.TrailingSlash {
			t.Fatal("producer did not receive global metadata URL policy", got)
		}
		return seo.RobotsOptions{}, nil
	}}, Options{BasePath: "/base"})
	if err != nil {
		t.Fatal(err)
	}
	empty, err := New(context.Background(), Sources{}, Options{})
	if err != nil || empty.Digest() != "" || len(empty.Head()) != 0 {
		t.Fatal("empty metadata changed legacy identity", empty, err)
	}
}

func TestStaticFilesystemAndBounds(t *testing.T) {
	for _, source := range []fstest.MapFS{
		{"ROBOTS.TXT": {Data: []byte("x")}},
		{"icon.png": {Mode: fs.ModeSymlink, Data: []byte("target")}},
		{"robots.txt": {Data: bytes.Repeat([]byte{'x'}, MaxRobotsBytes+1)}},
		{"manifest.webmanifest": {Data: bytes.Repeat([]byte{' '}, MaxManifestBytes+1)}},
	} {
		if _, err := ReadStatic(context.Background(), source); err == nil {
			t.Fatal("unsafe static file accepted")
		}
	}
	if files, err := ReadStatic(context.Background(), fstest.MapFS{"robots.txt": {Mode: fs.ModeDir}, "robots.txt/route.go": {Data: []byte("package route")}, "private.go": {Data: []byte("secret")}}); err != nil || len(files) != 0 {
		t.Fatal("manual route/source directory treated as metadata", files, err)
	}
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "robots.txt")); err != nil {
		t.Skip(err)
	}
	if _, err := ReadStatic(context.Background(), os.DirFS(root)); err == nil {
		t.Fatal("OS filesystem symlink followed")
	}
	set, err := New(context.Background(), Sources{Static: fstest.MapFS{"robots.txt": {Data: []byte("User-agent: *\n")}}}, Options{BasePath: "/base"})
	if err != nil {
		t.Fatal(err)
	}
	for _, conflict := range []string{"/robots.txt", "/base/robots.txt", "/base/", "/"} {
		if err := set.CheckCollisions([]string{conflict}); err == nil {
			t.Fatal("collision accepted", conflict)
		}
	}
	if err := set.CheckCollisions([]string{"/other", "/base/robots.txt/"}); err != nil {
		t.Fatal(err)
	}
}

func TestStartupProducerInputBoundsAndMidwayCancellation(t *testing.T) {
	for name, sources := range map[string]Sources{
		"robots-count": {Robots: func(context.Context) (seo.RobotsOptions, error) {
			return seo.RobotsOptions{Rules: make([]seo.RobotsRule, 8193)}, nil
		}},
		"robots-text": {Robots: func(context.Context) (seo.RobotsOptions, error) {
			return seo.RobotsOptions{Rules: []seo.RobotsRule{{Allow: []string{strings.Repeat("x", 8193)}}}}, nil
		}},
		"sitemap-count": {Sitemap: func(context.Context) ([]seo.SitemapEntry, error) {
			return make([]seo.SitemapEntry, MaxSitemapEntries+1), nil
		}},
		"sitemap-text": {Sitemap: func(context.Context) ([]seo.SitemapEntry, error) {
			return []seo.SitemapEntry{{URL: "https://example.test/" + strings.Repeat("x", 8193)}}, nil
		}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := New(context.Background(), sources, Options{}); err == nil {
				t.Fatal("unbounded producer input accepted")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	later := false
	_, err := New(ctx, Sources{Robots: func(context.Context) (seo.RobotsOptions, error) {
		cancel()
		return seo.RobotsOptions{}, nil
	}, Sitemap: func(context.Context) ([]seo.SitemapEntry, error) {
		later = true
		return nil, nil
	}}, Options{})
	if !errors.Is(err, context.Canceled) || later {
		t.Fatal("cancellation did not stop subsequent producer", later, err)
	}
}

func TestRasterValidation(t *testing.T) {
	for _, data := range [][]byte{[]byte(`<svg xmlns="http://www.w3.org/2000/svg"><script/></svg>`), pngData(t, 1, 1)[:24]} {
		_, err := Validate("icon.png", data)
		if err == nil {
			t.Fatal("invalid raster accepted")
		}
	}
	// A syntactically valid IHDR with huge dimensions must fail before decoding
	// or allocating its advertised raster, even though compressed data is tiny.
	large := append([]byte(nil), pngData(t, 1, 1)...)
	binary.BigEndian.PutUint32(large[16:], MaxImageDimension+1)
	binary.BigEndian.PutUint32(large[29:], crc32.ChecksumIEEE(large[12:29]))
	if _, err := Validate("icon.png", large); err == nil {
		t.Fatal("dimension bomb accepted")
	}
	valid := icoData(pngData(t, 16, 16), 16, 16)
	if _, err := Validate("favicon.ico", valid); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func([]byte){func(data []byte) { data[2] = 2 }, func(data []byte) { binary.LittleEndian.PutUint32(data[18:], 1) }, func(data []byte) { binary.LittleEndian.PutUint32(data[14:], ^uint32(0)) }, func(data []byte) { data[6] = 32 }} {
		copy := append([]byte(nil), valid...)
		mutate(copy)
		if _, err := Validate("favicon.ico", copy); err == nil {
			t.Fatal("invalid ICO accepted")
		}
	}
	dib := make([]byte, 48)
	binary.LittleEndian.PutUint32(dib, 40)
	binary.LittleEndian.PutUint32(dib[4:], 1)
	binary.LittleEndian.PutUint32(dib[8:], 2)
	binary.LittleEndian.PutUint16(dib[12:], 1)
	binary.LittleEndian.PutUint16(dib[14:], 32)
	if _, err := Validate("favicon.ico", icoData(dib, 1, 1)); err != nil {
		t.Fatal("supported DIB rejected", err)
	}
	if _, err := Validate("favicon.ico", icoData(dib[:44], 1, 1)); err == nil {
		t.Fatal("truncated ICO bitmap accepted")
	}
}
