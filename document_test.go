package nextgo

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

func TestDocumentOptionsAreSharedBySSRStreamingAndNavigation(t *testing.T) {
	for _, stream := range []bool{false, true} {
		app := testApp(t, Config{Routes: []Route{{Pattern: "/", Metadata: []MetadataLayer{{Load: func(*Context, seo.Metadata) (seo.Metadata, error) {
			return seo.Metadata{Title: "Default", TitleTemplate: "%s — Site", Language: "fr", Direction: "ltr", Viewport: &seo.Viewport{Fit: "cover", ColorScheme: "light dark", ThemeColors: []seo.ThemeColor{{Color: "red", Media: "(prefers-color-scheme: light)"}}}}, nil
		}}}, Page: func(*Context) (Page, error) {
			body := ui.Text("body")
			if stream {
				body = ui.Defer("one", body, func(context.Context) (ui.Node, error) { return ui.Text("ready"), nil })
			}
			return Page{Stream: stream, Metadata: seo.Metadata{Title: "Bonjour"}, Body: body}, nil
		}}}})
		html := request(app, "/", false).Body.String()
		if !strings.Contains(html, `<html lang="fr" dir="ltr">`) || !strings.Contains(html, "<title>Bonjour — Site</title>") || strings.Count(html, `name="viewport"`) != 1 || !strings.Contains(html, `content="width=device-width,initial-scale=1,viewport-fit=cover"`) {
			t.Fatal("SSR/stream document mismatch", html)
		}
		response := request(app, "/", true)
		var data Envelope
		if err := json.Unmarshal(response.Body.Bytes(), &data); err != nil || data.Title != "Bonjour — Site" || data.Document.Language != "fr" || data.Document.Direction != "ltr" || data.Document.Viewport != "width=device-width,initial-scale=1,viewport-fit=cover" {
			t.Fatal(data, err)
		}
		for _, element := range data.Head {
			if element.Attrs["name"] == "viewport" {
				t.Fatal("duplicate viewport in structured head")
			}
		}
	}
}

func TestPublicPageSnapshotsRetainOwnedDocumentOptions(t *testing.T) {
	language := "ar"
	viewport := seo.Viewport{Fit: "cover", ThemeColors: []seo.ThemeColor{{Color: "red"}}}
	calls := 0
	app := testApp(t, Config{Routes: []Route{{Pattern: "/", Cache: &CachePolicy{TTL: time.Minute}, Page: func(*Context) (Page, error) {
		calls++
		return Page{Metadata: seo.Metadata{Language: language, Direction: "rtl", Viewport: &viewport}, Body: ui.Text("body")}, nil
	}}}})
	first := request(app, "/", true)
	language = "fr"
	viewport.Fit = "contain"
	viewport.ThemeColors[0].Color = "blue"
	second := request(app, "/", true)
	if first.Body.String() != second.Body.String() || calls != 1 {
		t.Fatal("snapshot changed with source metadata", calls)
	}
	html := request(app, "/", false).Body.String()
	if !strings.Contains(html, `lang="ar" dir="rtl"`) || !strings.Contains(html, "viewport-fit=cover") || strings.Contains(html, "blue") {
		t.Fatal("cached document options lost", html)
	}
}

func TestInvalidDocumentMetadataAndReservedViewportFailBeforeCommit(t *testing.T) {
	for _, metadata := range []seo.Metadata{{Language: "bad language"}, {Direction: "sideways"}, {Viewport: &seo.Viewport{InitialScale: -1}}, {TitleTemplate: "invalid"}} {
		app := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) {
			return Page{Title: "private title", Metadata: metadata, Body: ui.Text("private body")}, nil
		}}}})
		response := request(app, "/", false)
		if response.Code != 500 || strings.Contains(response.Body.String(), "private") || !strings.Contains(response.Body.String(), `lang="en" dir="ltr"`) {
			t.Fatal(response.Code, response.Body.String())
		}
	}
	app := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) { return Page{Meta: map[string]string{"VIEWPORT": "wrong"}}, nil }}}})
	if response := request(app, "/", true); response.Code != 500 {
		t.Fatal("reserved viewport accepted", response.Code)
	}
}

func TestLegacyTitleAndAbsoluteMetadataBypassInheritedTemplate(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		app := testApp(t, Config{Routes: []Route{{Pattern: "/", Metadata: []MetadataLayer{{Load: func(*Context, seo.Metadata) (seo.Metadata, error) {
			return seo.Metadata{TitleTemplate: "%s | Parent"}, nil
		}}}, Page: func(*Context) (Page, error) {
			p := Page{Metadata: seo.Metadata{Title: "Standalone", AbsoluteTitle: true}, Body: ui.Text("body")}
			if legacy {
				p.Title = "Legacy"
			}
			return p, nil
		}}}})
		response := request(app, "/", true)
		var data Envelope
		_ = json.Unmarshal(response.Body.Bytes(), &data)
		want := "Standalone"
		if legacy {
			want = "Legacy"
		}
		if data.Title != want {
			t.Fatal(data.Title, want)
		}
	}
}
