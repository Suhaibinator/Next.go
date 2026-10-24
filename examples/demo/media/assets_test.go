//go:build !js

package media_test

import (
	"image/png"
	"net/http/httptest"
	"strings"
	"testing"

	page "github.com/Suhaibinator/Next.go/examples/demo/app/media"
	"github.com/Suhaibinator/Next.go/examples/demo/media"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

func TestEmbeddedImageAndMetadata(t *testing.T) {
	p, err := page.Page(nil)
	if err != nil {
		t.Fatal(err)
	}
	html, err := ui.HTML(p.Body)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`srcset=`, `width="960"`, `height="540"`, `fetchpriority="high"`, `Smooth teal and green waves`} {
		if !strings.Contains(html, want) {
			t.Errorf("page lacks %s", want)
		}
	}
	head, err := ui.HTML(seo.Node(p.Metadata))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`rel="canonical"`, `https://example.com/media`, `property="og:image"`, `name="twitter:card"`} {
		if !strings.Contains(head, want) {
			t.Errorf("head lacks %s", want)
		}
	}
	r := httptest.NewRecorder()
	media.Handler().ServeHTTP(r, httptest.NewRequest("GET", "/_nextgo/image?src=%2Fdemo.png&w=320&q=75", nil))
	if r.Code != 200 {
		t.Fatal(r.Code, r.Body.String())
	}
	img, err := png.Decode(r.Body)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 320 || img.Bounds().Dy() != 180 {
		t.Fatal(img.Bounds())
	}
	for path, handler := range media.Crawlers() {
		r := httptest.NewRecorder()
		handler.ServeHTTP(r, httptest.NewRequest("GET", path, nil))
		if r.Code != 200 {
			t.Fatal(path, r.Code)
		}
		if path == "/sitemap.xml" && !strings.Contains(r.Body.String(), "https://example.com/media") {
			t.Fatal("media absent from sitemap")
		}
		if path == "/robots.txt" && !strings.Contains(r.Body.String(), "Sitemap: https://example.com/sitemap.xml") {
			t.Fatal("sitemap absent from robots")
		}
	}
}
