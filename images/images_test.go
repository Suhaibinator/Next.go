package images

import (
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/Suhaibinator/Next.go/ui"
)

func fixture(t *testing.T, format string) []byte {
	t.Helper()
	im := image.NewNRGBA(image.Rect(0, 0, 80, 40))
	for y := 0; y < 40; y++ {
		for x := 0; x < 80; x++ {
			im.Set(x, y, color.NRGBA{R: uint8(x * 3), G: uint8(y * 5), B: 100, A: uint8(128 + x)})
		}
	}
	var b bytes.Buffer
	var err error
	switch format {
	case "jpeg":
		err = jpeg.Encode(&b, im, nil)
	case "gif":
		err = gif.Encode(&b, im, nil)
	default:
		err = png.Encode(&b, im)
	}
	if err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}
func handler(t *testing.T, c Config) *Handler {
	t.Helper()
	if c.FS == nil {
		c.FS = fstest.MapFS{"photo.png": {Data: fixture(t, "png")}}
	}
	if c.Widths == nil {
		c.Widths = []int{20, 40, 80, 160}
	}
	h, err := New(c)
	if err != nil {
		t.Fatal(err)
	}
	return h
}
func request(h http.Handler, method, query string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "http://example.test/_nextgo/image?"+query, nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}
func query(src string, w int) string {
	return url.Values{"src": {src}, "w": {strconv.Itoa(w)}}.Encode()
}

func TestResizeFormatsAndNoUpscale(t *testing.T) {
	for _, format := range []string{"png", "jpeg", "gif"} {
		t.Run(format, func(t *testing.T) {
			h := handler(t, Config{FS: fstest.MapFS{"photo": {Data: fixture(t, format)}}})
			for _, tc := range []struct{ w, actualW, actualH int }{{20, 20, 10}, {40, 40, 20}, {160, 80, 40}} {
				w := request(h, "GET", query("/photo", tc.w))
				if w.Code != 200 {
					t.Fatalf("status %d: %s", w.Code, w.Body.String())
				}
				cfg, out, err := image.DecodeConfig(bytes.NewReader(w.Body.Bytes()))
				if err != nil {
					t.Fatal(err)
				}
				if cfg.Width != tc.actualW || cfg.Height != tc.actualH {
					t.Fatalf("dimensions %dx%d", cfg.Width, cfg.Height)
				}
				want := "png"
				if format == "jpeg" {
					want = "jpeg"
				}
				if out != want || w.Header().Get("Content-Type") != "image/"+want {
					t.Fatalf("format %s mime %s", out, w.Header().Get("Content-Type"))
				}
				if w.Header().Get("X-Content-Type-Options") != "nosniff" {
					t.Fatal("missing nosniff")
				}
			}
		})
	}
}

func TestInvalidRequests(t *testing.T) {
	h := handler(t, Config{})
	for _, src := range []string{"https://evil.test/photo", "//evil.test/photo", "/../secret", "/a/../../secret", "/a\\secret", "/photo.png?secret", "/photo.png#frag", "/%2e%2e/secret", "relative.png", "/"} {
		w := request(h, "GET", query(src, 40))
		if w.Code != 400 {
			t.Errorf("%s got %d", src, w.Code)
		}
	}
	for _, q := range []string{"src=%2Fphoto.png&w=33", "src=%2Fphoto.png&w=40&q=0", "src=%2Fphoto.png&w=40&q=101", "src=%2Fphoto.png&w=40&q=garbage", "src=%2Fphoto.png&w=40&w=80", "src=%2Fphoto.png&w=40&extra=1", "src=%ZZ&w=40"} {
		if w := request(h, "GET", q); w.Code != 400 {
			t.Errorf("%s got %d", q, w.Code)
		}
	}
	if w := request(h, "POST", query("/photo.png", 40)); w.Code != 405 || w.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("method response: %d %v", w.Code, w.Header())
	}
	if w := request(h, "GET", query("/missing.png", 40)); w.Code != 404 {
		t.Fatal(w.Code)
	}
	h = handler(t, Config{FS: fstest.MapFS{"evil": {Data: []byte("<svg onload='bad()'>")}}})
	if w := request(h, "GET", query("/evil", 40)); w.Code != 415 {
		t.Fatal(w.Code)
	}
}

func TestInputBoundsBeforeDecode(t *testing.T) {
	h := handler(t, Config{MaxInputBytes: 10})
	if w := request(h, "GET", query("/photo.png", 40)); w.Code != 413 {
		t.Fatalf("byte bound: %d", w.Code)
	}
	h = handler(t, Config{MaxPixels: 100})
	if w := request(h, "GET", query("/photo.png", 40)); w.Code != 413 {
		t.Fatalf("pixel bound: %d", w.Code)
	}
	// A valid PNG header advertising enormous dimensions must be rejected
	// before an incomplete stream ever reaches the allocating decoder.
	b := fixture(t, "png")
	binary.BigEndian.PutUint32(b[16:20], 1_000_000)
	binary.BigEndian.PutUint32(b[20:24], 1_000_000)
	binary.BigEndian.PutUint32(b[29:33], crc32.ChecksumIEEE(b[12:29]))
	h = handler(t, Config{FS: fstest.MapFS{"bomb": {Data: b}}})
	if w := request(h, "GET", query("/bomb", 40)); w.Code != 413 {
		t.Fatalf("bomb status: %d", w.Code)
	}
}

func TestConditionalAndHead(t *testing.T) {
	h := handler(t, Config{})
	first := request(h, "GET", query("/photo.png", 40))
	etag := first.Header().Get("ETag")
	if etag == "" {
		t.Fatal("missing ETag")
	}
	for _, tag := range []string{etag, "W/" + etag, "*", `"other", ` + etag} {
		r := httptest.NewRequest("GET", "http://test/?"+query("/photo.png", 40), nil)
		r.Header.Set("If-None-Match", tag)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 304 || w.Body.Len() != 0 {
			t.Fatalf("conditional %q: %d %s", tag, w.Code, w.Body.String())
		}
	}
	w := request(h, "HEAD", query("/photo.png", 40))
	if w.Code != 200 || w.Body.Len() != 0 || w.Header().Get("Content-Length") != first.Header().Get("Content-Length") {
		t.Fatalf("HEAD: %d %v", w.Code, w.Header())
	}
	if h.lru.Len() != 1 {
		t.Fatal("cache duplicated variants")
	}
}

func TestCacheLimitsAndSourceInvalidation(t *testing.T) {
	source := fstest.MapFS{"photo.png": {Data: fixture(t, "png")}}
	h := handler(t, Config{FS: source, MaxCacheEntries: 1, MaxCacheBytes: 10000})
	a := request(h, "GET", query("/photo.png", 20))
	request(h, "GET", query("/photo.png", 40))
	if h.lru.Len() != 1 || h.cacheBytes > 10000 {
		t.Fatal("cache bounds exceeded")
	}
	if h.get("missing") != nil {
		t.Fatal("unknown cache entry")
	}
	changed := image.NewRGBA(image.Rect(0, 0, 80, 40))
	var b bytes.Buffer
	_ = png.Encode(&b, changed)
	source["photo.png"] = &fstest.MapFile{Data: b.Bytes()}
	other := request(h, "GET", query("/photo.png", 20))
	if a.Header().Get("ETag") == other.Header().Get("ETag") {
		t.Fatal("source changes did not invalidate cache")
	}
	h = handler(t, Config{MaxCacheBytes: 1})
	request(h, "GET", query("/photo.png", 40))
	if h.lru.Len() != 0 {
		t.Fatal("oversized entry cached")
	}
}

func TestConcurrentRequests(t *testing.T) {
	h := handler(t, Config{})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if w := request(h, "GET", query("/photo.png", 40)); w.Code != 200 {
				t.Error(w.Code)
			}
		}()
	}
	wg.Wait()
	if h.lru.Len() != 1 {
		t.Fatal("duplicate entries")
	}
}

func TestDimensions(t *testing.T) {
	for _, tc := range []struct{ w, h, request, max, wantW, wantH int }{{10, 1000, 40, 100, 1, 100}, {80, 40, 160, 4096, 80, 40}, {1, 100, 1, 10, 1, 10}} {
		w, h := dimensions(tc.w, tc.h, tc.request, tc.max)
		if w != tc.wantW || h != tc.wantH {
			t.Fatalf("%+v got %dx%d", tc, w, h)
		}
	}
}

func TestImageComponent(t *testing.T) {
	h := handler(t, Config{})
	html, err := ui.HTML(h.Image(ImageOptions{Src: "/photo.png", Alt: `a < b`, Width: 80, Height: 40, Sizes: "50vw"}))
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{`alt="a &lt; b"`, `width="80"`, `height="40"`, `loading="lazy"`, `decoding="async"`, `srcset=`, `20w`, `40w`, `80w`, `sizes="50vw"`} {
		if !strings.Contains(html, part) {
			t.Errorf("missing %s in %s", part, html)
		}
	}
	if strings.Contains(html, "160w") {
		t.Fatal("upscaled candidate")
	}
	html, err = ui.HTML(h.Image(ImageOptions{Src: "/photo.png", Width: 10, Height: 5, Priority: true}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, `fetchpriority="high"`) || strings.Contains(html, `srcset=`) {
		t.Fatal(html)
	}
	for _, o := range []ImageOptions{{Src: "https://test/x", Width: 1, Height: 1}, {Src: "/x", Width: 0, Height: 1}, {Src: "/x", Width: 1, Height: 1, Quality: 101}} {
		if _, err := ui.HTML(h.Image(o)); err == nil {
			t.Fatalf("accepted %+v", o)
		}
	}
}

func TestConfigValidation(t *testing.T) {
	for _, c := range []Config{{}, {FS: fstest.MapFS{}, Endpoint: "https://test/x"}, {FS: fstest.MapFS{}, Widths: []int{-1}}, {FS: fstest.MapFS{}, MaxInputBytes: -1}, {FS: fstest.MapFS{}, MaxPixels: -1}, {FS: fstest.MapFS{}, MaxCacheEntries: -1}, {FS: fstest.MapFS{}, MaxCacheBytes: -1}, {FS: fstest.MapFS{}, MaxConcurrent: -1}} {
		if _, err := New(c); err == nil {
			t.Fatalf("accepted %+v", c)
		}
	}
}
