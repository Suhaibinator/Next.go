package seo

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCrawlerMarshalMatchesExistingHandlers(t *testing.T) {
	entries := []SitemapEntry{{URL: "https://example.test/a?x=1&y=2"}}
	options := RobotsOptions{Rules: []RobotsRule{{Disallow: []string{"/private"}}}, Sitemaps: []string{"https://example.test/sitemap.xml"}}
	for _, kind := range []string{"sitemap", "robots"} {
		var data []byte
		var handler http.Handler
		var err error
		if kind == "sitemap" {
			data, err = MarshalSitemap(entries)
			if err == nil {
				handler, err = Sitemap(entries)
			}
		} else {
			data, err = MarshalRobots(options)
			if err == nil {
				handler, err = Robots(options)
			}
		}
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
		if !bytes.Equal(w.Body.Bytes(), data) {
			t.Fatal(kind, "handler/marshal output differs")
		}
	}
}
