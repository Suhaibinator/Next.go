package seo

import (
	"encoding/xml"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/ui"
)

func TestMetadataEscapesAndSocialTags(t *testing.T) {
	m := Metadata{Title: `A <script> " title`, Description: `<img src=x onerror="bad">`, Canonical: "https://example.com/a?x=1&y=2", Robots: "noindex, follow", OpenGraph: OpenGraph{Title: `<OG>`, URL: "https://example.com/a", Images: []Image{{URL: "https://example.com/image.png", Alt: `a "quote"`, Width: 1200, Height: 630}}}, Twitter: Twitter{Card: "summary_large_image", Title: "tweet", Images: []string{"https://example.com/t.png"}}, Alternates: map[string]string{"fr": "https://example.com/fr", "en": "https://example.com/en"}, Icons: []Icon{{URL: "/assets/icon.png"}}}
	html, err := ui.HTML(Node(m))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`<title>A &lt;script&gt; &#34; title</title>`, `content="&lt;img src=x onerror=&#34;bad&#34;&gt;"`, `href="https://example.com/a?x=1&amp;y=2"`, `property="og:image:width"`, `content="1200"`, `name="twitter:card"`, `hreflang="en"`, `rel="icon"`} {
		if !strings.Contains(html, want) {
			t.Errorf("missing %s in %s", want, html)
		}
	}
	if strings.Contains(html, "<script>") || strings.Contains(html, "<img") {
		t.Fatalf("unescaped HTML: %s", html)
	}
	elements, err := Elements(m)
	if err != nil {
		t.Fatal(err)
	}
	if elements[0].Tag != "title" || elements[0].Text != m.Title {
		t.Fatal("structured text should remain unescaped")
	}
	if strings.Index(html, `hreflang="en"`) > strings.Index(html, `hreflang="fr"`) {
		t.Fatal("alternate ordering is unstable")
	}
}

func TestURLValidation(t *testing.T) {
	for _, u := range []string{"javascript:alert(1)", "data:text/html,a", "//example.com/a", "/relative", "https://user:pass@example.com/a", "https://", "https://example.com/\nx", " https://example.com", "https://example.com/space here", "http://example.com:bad"} {
		t.Run(u, func(t *testing.T) {
			if AbsoluteURL(u) == nil {
				t.Fatalf("accepted %q", u)
			}
		})
	}
	for _, u := range []string{"http://localhost:3000/a", "https://example.com/a?q=x&v=2", "https://[::1]/", "https://example.com/%20"} {
		if err := AbsoluteURL(u); err != nil {
			t.Fatal(err)
		}
	}
	for name, m := range map[string]Metadata{"canonical": {Canonical: "javascript:bad"}, "og": {OpenGraph: OpenGraph{URL: "/relative"}}, "ogimage": {OpenGraph: OpenGraph{Images: []Image{{URL: "javascript:bad"}}}}, "twitter": {Twitter: Twitter{Images: []string{"/relative"}}}, "alternate": {Alternates: map[string]string{"en": "//example.com"}}, "icon": {Icons: []Icon{{URL: "//example.com"}}}, "backslash": {Icons: []Icon{{URL: "/\\example.com"}}}, "size": {OpenGraph: OpenGraph{Images: []Image{{URL: "https://example.com/a", Width: -1}}}}} {
		t.Run(name, func(t *testing.T) {
			if _, err := Elements(m); err == nil {
				t.Fatal("accepted unsafe metadata")
			}
		})
	}
}

func TestMetadataMerge(t *testing.T) {
	p := Metadata{Title: "parent", Description: "inherited", OpenGraph: OpenGraph{Title: "og parent", Images: []Image{{URL: "https://example.com/a"}}}, Twitter: Twitter{Card: "summary", Images: []string{"https://example.com/t"}}, Alternates: map[string]string{"en": "https://example.com/en", "fr": "https://example.com/fr"}, Icons: []Icon{{URL: "/icon.png"}}}
	c := Metadata{Title: "child", OpenGraph: OpenGraph{Description: "og child"}, Twitter: Twitter{Images: []string{}}, Alternates: map[string]string{"fr": "https://example.com/fr-new"}}
	m := Merge(p, c)
	if m.Title != "child" || m.Description != "inherited" || m.OpenGraph.Title != "og parent" || m.OpenGraph.Description != "og child" || len(m.OpenGraph.Images) != 1 || len(m.Twitter.Images) != 0 || m.Twitter.Images == nil || m.Alternates["en"] == "" || !strings.HasSuffix(m.Alternates["fr"], "fr-new") {
		t.Fatalf("bad merge: %+v", m)
	}
	m.OpenGraph.Images[0].URL = "changed"
	m.Icons[0].URL = "changed"
	m.Alternates["fr"] = "changed"
	if p.OpenGraph.Images[0].URL == "changed" || p.Icons[0].URL == "changed" || p.Alternates["fr"] == "changed" || c.Alternates["fr"] == "changed" {
		t.Fatal("merge aliases inputs")
	}
}

func TestSitemap(t *testing.T) {
	p := 0.5
	h, err := Sitemap([]SitemapEntry{{URL: "https://example.com/a?x=1&y=2", LastModified: time.Date(2026, 10, 22, 12, 30, 0, 0, time.FixedZone("other", 3600)), ChangeFrequency: "daily", Priority: &p}})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("GET", "/sitemap.xml", nil))
	if r.Code != 200 || r.Header().Get("Content-Type") != "application/xml; charset=utf-8" || !strings.Contains(r.Body.String(), "x=1&amp;y=2") || !strings.Contains(r.Body.String(), "2026-10-22T11:30:00Z") {
		t.Fatal(r.Code, r.Header(), r.Body.String())
	}
	d := xml.NewDecoder(strings.NewReader(r.Body.String()))
	for {
		_, err := d.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, method := range []string{"HEAD", "POST"} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest(method, "/sitemap.xml", nil))
		if method == "HEAD" {
			if r.Body.Len() != 0 || r.Header().Get("Content-Length") == "" {
				t.Fatal("HEAD body or length")
			}
		} else if r.Code != 405 || r.Header().Get("Allow") != "GET, HEAD" {
			t.Fatal("POST accepted")
		}
	}
	for _, e := range []SitemapEntry{{URL: "/relative"}, {URL: "https://example.com", ChangeFrequency: "bad"}, {URL: "https://example.com", Priority: func() *float64 { x := 2.0; return &x }()}} {
		if _, err := Sitemap([]SitemapEntry{e}); err == nil {
			t.Fatal("accepted invalid sitemap")
		}
	}
}

func TestRobots(t *testing.T) {
	h, err := Robots(RobotsOptions{Rules: []RobotsRule{{Allow: []string{"/"}, Disallow: []string{"/admin", "/private/*?token="}}}, Sitemaps: []string{"https://example.com/sitemap.xml"}})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/robots.txt", nil))
	want := "User-agent: *\nAllow: /\nDisallow: /admin\nDisallow: /private/*?token=\n\nSitemap: https://example.com/sitemap.xml\n"
	if r.Body.String() != want || r.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Fatal(r.Header(), r.Body.String())
	}
	for _, options := range []RobotsOptions{{Rules: []RobotsRule{{UserAgents: []string{"*\nDisallow: /"}}}}, {Rules: []RobotsRule{{Disallow: []string{"/a\r\nAllow: /"}}}}, {Rules: []RobotsRule{{Disallow: []string{"/a#comment"}}}}, {Sitemaps: []string{"/relative"}}, {Sitemaps: []string{"https://example.com/#\nAllow: /"}}} {
		if _, err := Robots(options); err == nil {
			t.Fatal("accepted robots injection")
		}
	}
}

func TestSitemapIndex(t *testing.T) {
	h, err := SitemapIndex([]SitemapIndexEntry{{URL: "https://example.com/sitemap.xml?a=1&b=2", LastModified: time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)}})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("GET", "/sitemap-index.xml", nil))
	for _, want := range []string{"<sitemapindex", "<sitemap>", "a=1&amp;b=2", "2026-10-22T00:00:00Z"} {
		if !strings.Contains(r.Body.String(), want) {
			t.Fatal(r.Body.String())
		}
	}
	if _, err := SitemapIndex([]SitemapIndexEntry{{URL: "javascript:bad"}}); err == nil {
		t.Fatal("invalid URL accepted")
	}
}
