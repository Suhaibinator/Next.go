package nextgo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

func TestInheritedMetadataUsesOwnedParentsAndExplicitPagePrecedence(t *testing.T) {
	parent := seo.Metadata{BaseURL: "https://public.example/site", Title: "Parent", Description: "Parent description", Robots: "index", OpenGraph: seo.OpenGraph{SiteName: "Parent site", Images: []seo.Image{{URL: "images/parent.png"}}}, Alternates: map[string]string{"en": "english"}}
	app := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) {
		return Page{Title: "Explicit page", Meta: map[string]string{"DESCRIPTION": "Explicit description", "RoBoTs": "noindex"}, Metadata: seo.Metadata{Title: "Page metadata"}, Body: ui.Text("body")}, nil
	}, Metadata: []MetadataLayer{
		{Load: func(*Context, seo.Metadata) (seo.Metadata, error) { return parent, nil }},
		{Load: func(_ *Context, inherited seo.Metadata) (seo.Metadata, error) {
			if inherited.Title != "Parent" {
				t.Fatal(inherited)
			}
			inherited.Alternates["en"] = "modified"
			inherited.OpenGraph.Images[0].URL = "modified.png"
			return seo.Metadata{Title: "Child", Canonical: "article"}, nil
		}},
	}}}})
	response := request(app, "/", true)
	var data Envelope
	if err := json.Unmarshal(response.Body.Bytes(), &data); err != nil || data.Status != 200 || data.Title != "Explicit page" || data.Description != "Explicit description" || data.Meta["robots"] != "noindex" {
		t.Fatal(data, err)
	}
	found := map[string]string{}
	for _, element := range data.Head {
		if element.Attrs["rel"] == "canonical" {
			found["canonical"] = element.Attrs["href"]
		}
		if element.Attrs["property"] == "og:site_name" {
			found["site"] = element.Attrs["content"]
		}
		if element.Attrs["property"] == "og:image" {
			found["image"] = element.Attrs["content"]
		}
		if element.Attrs["rel"] == "alternate" {
			found["alternate"] = element.Attrs["href"]
		}
		if element.Attrs["name"] == "robots" {
			t.Fatal("duplicate typed/legacy robots")
		}
	}
	if found["canonical"] != "https://public.example/site/article" || found["image"] != "https://public.example/site/images/parent.png" || found["alternate"] != "https://public.example/site/english" || found["site"] != "Parent site" {
		t.Fatal(found)
	}
	if parent.Alternates["en"] != "english" || parent.OpenGraph.Images[0].URL != "images/parent.png" {
		t.Fatal("parent data aliased")
	}
	html := request(app, "/", false).Body.String()
	if strings.Count(html, `name="description"`) != 1 || strings.Count(html, `name="robots"`) != 1 {
		t.Fatal("duplicate document metadata", html)
	}
}

func TestMetadataSharesRequestMemoAndPublicSnapshot(t *testing.T) {
	calls, providers := 0, 0
	record := func(c *Context) (string, error) {
		return Memo(c.Context, "metadata record", func(context.Context) (string, error) { calls++; return "Record", nil })
	}
	layer := func(c *Context, _ seo.Metadata) (seo.Metadata, error) {
		providers++
		title, err := record(c)
		return seo.Metadata{Title: title, BaseURL: "https://example.test", Canonical: "/record"}, err
	}
	app := testApp(t, Config{Routes: []Route{{Pattern: "/", Cache: &CachePolicy{TTL: time.Minute}, Page: func(c *Context) (Page, error) { value, err := record(c); return Page{Body: ui.Text(value)}, err }, Layouts: []LayoutFunc{func(c *Context, n ui.Node) (ui.Node, error) { _, err := record(c); return n, err }}, Metadata: []MetadataLayer{{Load: layer}, {Load: layer}}}}})
	for i := 0; i < 2; i++ {
		response := request(app, "/", true)
		var data Envelope
		if err := json.Unmarshal(response.Body.Bytes(), &data); err != nil || data.Title != "Record" || data.HTML != "Record" {
			t.Fatal(data, err)
		}
	}
	if calls != 1 || providers != 2 {
		t.Fatal("loaders/metadata not shared or snapshotted", calls, providers)
	}
}

func TestMetadataFailuresDiscardPartialHeadAndUseBoundaries(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{{ErrNotFound, 404}, {ErrUnauthorized, 401}, {ErrForbidden, 403}, {fmt.Errorf("private metadata error"), 500}} {
		nested := false
		app := testApp(t, Config{NotFound: static("custom missing"), Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) { return Page{Title: "private title", Body: ui.Text("private body")}, nil }, Layouts: []LayoutFunc{func(_ *Context, n ui.Node) (ui.Node, error) { return n, nil }}, Access: []AccessBoundary{{Layouts: 1, Unauthorized: func(*Context) (Page, error) { nested = true; return Page{}, nil }, Forbidden: func(*Context) (Page, error) { nested = true; return Page{}, nil }}}, Metadata: []MetadataLayer{{Load: func(*Context, seo.Metadata) (seo.Metadata, error) {
			return seo.Metadata{Title: "private metadata"}, tc.err
		}}}}}})
		response := request(app, "/", true)
		if response.Code != tc.status || nested || strings.Contains(response.Body.String(), "private") {
			t.Fatal(tc.status, response.Code, response.Body.String(), nested)
		}
	}
}

func TestPageFailureSkipsMetadataAndInvalidRegistrationsFail(t *testing.T) {
	ran := false
	app := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) { return Page{}, ErrUnauthorized }, Metadata: []MetadataLayer{{Load: func(*Context, seo.Metadata) (seo.Metadata, error) { ran = true; return seo.Metadata{}, nil }}}}}})
	if w := request(app, "/", false); w.Code != 401 || ran {
		t.Fatal("metadata ran after denied page", ran, w.Code)
	}
	load := func(*Context, seo.Metadata) (seo.Metadata, error) { return seo.Metadata{}, nil }
	for _, layers := range [][]MetadataLayer{{{}}, {{Load: load, Layouts: -1}}, {{Load: load, Layouts: 1}}} {
		if _, err := New(Config{Routes: []Route{{Pattern: "/", Page: static("x"), Metadata: layers}}}); err == nil {
			t.Fatal("invalid metadata layer accepted", layers)
		}
	}
	if _, err := New(Config{Routes: []Route{{Pattern: "/", API: func(http.ResponseWriter, *http.Request) {}, Metadata: []MetadataLayer{{Load: load}}}}}); err == nil {
		t.Fatal("API accepted page metadata")
	}
	duplicate := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) {
		return Page{Meta: map[string]string{"ROBOTS": "index", "robots": "noindex"}}, nil
	}}}})
	if w := request(duplicate, "/", true); w.Code != 500 {
		t.Fatal("ambiguous metadata accepted", w.Code)
	}
}

func TestMetadataRunsBeforeStreamHeadAndCanceledContributionsAreDiscarded(t *testing.T) {
	app := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) {
		return Page{Stream: true, Body: ui.Defer("region", ui.Text("Loading"), func(context.Context) (ui.Node, error) { return ui.Text("Ready"), nil })}, nil
	}, Metadata: []MetadataLayer{{Load: func(*Context, seo.Metadata) (seo.Metadata, error) {
		return seo.Metadata{Title: "Metadata before shell", Description: "Server head"}, nil
	}}}}}})
	response := request(app, "/", false)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "<title>Metadata before shell</title>") || strings.Index(response.Body.String(), "<title>") > strings.Index(response.Body.String(), "data-nextgo-boundary") {
		t.Fatal("metadata missing from stream shell", response.Code, response.Body.String())
	}
	ctx, cancel := context.WithCancel(context.Background())
	canceled := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: static("body"), Metadata: []MetadataLayer{{Load: func(*Context, seo.Metadata) (seo.Metadata, error) {
		cancel()
		return seo.Metadata{Title: "discarded private metadata"}, nil
	}}}}}})
	writer := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
	canceled.ServeHTTP(writer, req)
	if writer.Code != 500 || strings.Contains(writer.Body.String(), "discarded private metadata") {
		t.Fatal("canceled metadata committed", writer.Code, writer.Body.String())
	}
}
