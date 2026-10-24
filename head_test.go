package nextgo

import (
	"encoding/json"
	"github.com/Suhaibinator/Next.go/assets"
	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
	"net/http"
	"strings"
	"testing"
	"time"
)

type testResource []seo.Element

func (r testResource) Elements() ([]seo.Element, error) { return r, nil }

func TestPageResourcesDocumentsAndNavigation(t *testing.T) {
	font := assets.Font{Family: "App Sans", Source: "/assets/font.123.woff2", Format: "woff2"}
	page := Page{Title: "resources", Body: ui.Text("useful body"), Resources: []assets.Resource{assets.Stylesheet("/assets/page.css"), font.Preload()}}
	a := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) { return page, nil }}}})
	full := request(a, "/", false)
	for _, want := range []string{`rel="stylesheet"`, `href="/assets/page.css"`, `as="font"`, `type="font/woff2"`, `crossorigin="anonymous"`, `data-nextgo-head=""`} {
		if !strings.Contains(full.Body.String(), want) {
			t.Fatalf("missing %s: %s", want, full.Body.String())
		}
	}
	nav := request(a, "/", true)
	var envelope Envelope
	if err := json.Unmarshal(nav.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Head) != 2 || envelope.Head[0].Attrs["href"] != "/assets/page.css" || envelope.Head[1].Attrs["as"] != "font" {
		t.Fatalf("head=%+v", envelope.Head)
	}
}

func TestInvalidHeadResourceNeverCachedOrEmitted(t *testing.T) {
	for _, resource := range []assets.Resource{
		assets.Stylesheet("https://foreign.example/a.css"),
		testResource{{Tag: "script", Text: "secret"}},
		testResource{{Tag: "link", Attrs: map[string]string{"rel": "stylesheet", "href": "/a.css", "onload": "secret"}}},
		testResource{{Tag: "link", Attrs: map[string]string{"rel": "preload", "href": "/a.js", "as": "script"}}},
		nil,
	} {
		loads := 0
		a := testApp(t, Config{Routes: []Route{{Pattern: "/", Cache: &CachePolicy{TTL: time.Minute}, Page: func(*Context) (Page, error) {
			loads++
			return Page{Body: ui.Text("private body"), Resources: []assets.Resource{resource}}, nil
		}}}})
		for i := 0; i < 2; i++ {
			w := request(a, "/", i == 1)
			if w.Code != 500 || strings.Contains(w.Body.String(), "private body") || w.Header().Get("X-Nextgo-Prefetch") != "" {
				t.Fatalf("invalid head: %d %s", w.Code, w.Body.String())
			}
		}
		if loads != 2 {
			t.Fatal("invalid resource cached")
		}
	}
}

func TestResetContentOmitsBody(t *testing.T) {
	a := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: func(c *Context) (Page, error) {
		return Page{Body: ui.Text("hidden")}, c.SetStatus(http.StatusResetContent)
	}}}})
	for _, nav := range []bool{false, true} {
		w := request(a, "/", nav)
		if w.Code != 205 || w.Body.Len() != 0 {
			t.Fatalf("205 response: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestStructuredDataDocumentEnvelopeAndCacheValidation(t *testing.T) {
	data, err := seo.JSONLD(map[string]any{"@context": "https://schema.org", "@type": "WebPage", "name": "</script><img src=x>"})
	if err != nil {
		t.Fatal(err)
	}
	a := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) {
		return Page{Body: ui.Text("page"), JSONLD: []seo.StructuredData{data}}, nil
	}}}})
	document := request(a, "/", false)
	if document.Code != 200 || strings.Contains(document.Body.String(), "<img") || !strings.Contains(document.Body.String(), `type="application/ld+json"`) {
		t.Fatal(document.Body.String())
	}
	var envelope Envelope
	if err := json.Unmarshal(request(a, "/", true).Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Head) != 1 || envelope.Head[0].Tag != "script" || !json.Valid([]byte(envelope.Head[0].Text)) {
		t.Fatalf("%+v", envelope.Head)
	}
	invalid := testApp(t, Config{Routes: []Route{{Pattern: "/", Cache: &CachePolicy{TTL: time.Minute}, Page: func(*Context) (Page, error) {
		return Page{Body: ui.Text("invalid page"), JSONLD: []seo.StructuredData{{}}}, nil
	}}}})
	if w := request(invalid, "/", false); w.Code != 500 || strings.Contains(w.Body.String(), "invalid page") {
		t.Fatalf("invalid structured data served: %d %s", w.Code, w.Body.String())
	}
}

func TestRenderFailureDropsOriginalStructuredHead(t *testing.T) {
	data, err := seo.JSONLD(map[string]string{"name": "page-private-metadata"})
	if err != nil {
		t.Fatal(err)
	}
	app := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) {
		return Page{Metadata: seo.Metadata{Canonical: "https://example.com/private"}, JSONLD: []seo.StructuredData{data}, Resources: []assets.Resource{assets.Stylesheet("/private.css")}, Body: panicNode{}}, nil
	}}}})
	for _, nav := range []bool{false, true} {
		w := request(app, "/", nav)
		if w.Code != 500 || strings.Contains(w.Body.String(), "page-private-metadata") || strings.Contains(w.Body.String(), "private.css") || strings.Contains(w.Body.String(), "example.com/private") {
			t.Fatalf("failed render retained original head: %d %s", w.Code, w.Body.String())
		}
	}
}

func TestCachedHeadIsFrozenAndPayloadBudgetSkipsLargePages(t *testing.T) {
	attrs := map[string]string{"rel": "stylesheet", "href": "/assets/original.css"}
	meta := map[string]string{"author": "original"}
	alternates := map[string]string{"en": "https://example.com/original"}
	calls := 0
	a := testApp(t, Config{Routes: []Route{{Pattern: "/", Cache: &CachePolicy{TTL: time.Minute}, Page: func(*Context) (Page, error) {
		calls++
		return Page{Body: ui.Text("body"), Meta: meta, Metadata: seo.Metadata{Title: "metadata title", Description: "metadata description", Alternates: alternates}, Resources: []assets.Resource{testResource{{Tag: "link", Attrs: attrs}}}}, nil
	}}}})
	first := request(a, "/", true)
	attrs["href"] = "/assets/changed.css"
	meta["author"] = "changed"
	alternates["en"] = "https://example.com/changed"
	second := request(a, "/", true)
	if calls != 1 || first.Body.String() != second.Body.String() {
		t.Fatalf("mutable cached head: %d %s", calls, second.Body.String())
	}
	full := request(a, "/", false)
	if !strings.Contains(full.Body.String(), "metadata title") || !strings.Contains(full.Body.String(), "original.css") || strings.Contains(full.Body.String(), "changed") {
		t.Fatal("snapshot inconsistent between HTML and navigation")
	}
	if a.config.Cache.Bytes() <= int64(len("body")) {
		t.Fatal("default page cache has no byte accounting")
	}

	calls = 0
	limited := cache.New(4, cache.WithMaxBytes(1024, nil))
	a = testApp(t, Config{Cache: limited, Routes: []Route{{Pattern: "/", Cache: &CachePolicy{TTL: time.Minute}, Page: func(*Context) (Page, error) { calls++; return Page{Body: ui.Text(strings.Repeat("x", 4096))}, nil }}}})
	for i := 0; i < 2; i++ {
		w := request(a, "/", i == 1)
		if w.Code != 200 {
			t.Fatalf("oversized cache entry broke serving: %d", w.Code)
		}
	}
	if calls != 2 || limited.Len() != 0 || limited.Bytes() != 0 {
		t.Fatal("oversized public page retained")
	}
}
