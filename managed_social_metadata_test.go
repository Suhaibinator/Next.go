package nextgo

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/Suhaibinator/Next.go/metadatafiles"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

func TestManagedSocialMetadataDefaultsOverridesAndLocaleOwnership(t *testing.T) {
	image := managedMetadataFS(t)["icon.png"].Data
	var calls atomic.Int32
	producer := func(context.Context) ([]byte, error) { calls.Add(1); return image, nil }
	page := func(c *Context) (Page, error) {
		p := Page{Body: ui.Text("public page")}
		switch c.Request.URL.Path {
		case "/explicit":
			p.Metadata.OpenGraph.Images = []seo.Image{{URL: "https://trusted.example/explicit.png", Alt: "Explicit", Width: 4, Height: 3}}
			p.Metadata.Twitter = seo.Twitter{Card: "summary", Images: []string{"https://trusted.example/twitter-explicit.png"}}
		case "/clear":
			p.Metadata.OpenGraph.Images = []seo.Image{}
			p.Metadata.Twitter.Images = []string{}
		case "/legacy":
			p.Meta = map[string]string{"Twitter:Image": "https://trusted.example/legacy.png", "Twitter:Card": "summary"}
		}
		return p, nil
	}
	var routes []Route
	for _, pattern := range []string{"/", "/explicit", "/clear", "/legacy"} {
		routes = append(routes, Route{Pattern: pattern, Page: page, Metadata: []MetadataLayer{{Load: func(*Context, seo.Metadata) (seo.Metadata, error) {
			return seo.Metadata{BaseURL: "https://trusted.example/base/fr"}, nil
		}}}})
	}
	app := managedMetadataApp(t, Config{BuildID: "social", BasePath: "/site", AssetPrefix: "https://cdn.example/static", Locales: &LocaleConfig{Codes: []string{"en", "fr"}, Default: "en"}, MetadataFiles: metadatafiles.Sources{OpenGraphImage: producer, TwitterImage: producer, Static: fstest.MapFS{"opengraph-image.alt.txt": {Data: []byte("A <public> card\n")}, "twitter-image.alt.txt": {Data: []byte("Twitter public card")}}}, Routes: routes})
	if calls.Load() != 2 || app.config.MetadataFiles.OpenGraphImage != nil || app.config.MetadataFiles.TwitterImage != nil {
		t.Fatal("producer repeated/retained", calls.Load())
	}
	for _, prefix := range []string{"/site", "/site/fr"} {
		for _, nav := range []bool{false, true} {
			headers := map[string]string{}
			if nav {
				headers["X-Nextgo-Navigate"] = "1"
			}
			for _, path := range []string{"", "/explicit", "/clear", "/legacy"} {
				got := managedRequest(app, "GET", "https://untrusted.invalid"+prefix+path, headers)
				if got.Code != 200 {
					t.Fatal(got.Code, got.Body.String())
				}
				text := got.Body.String()
				if nav {
					var env Envelope
					if json.Unmarshal(got.Body.Bytes(), &env) != nil {
						t.Fatal(text)
					}
					data, _ := json.Marshal(env.Head)
					legacy, _ := json.Marshal(env.Meta)
					text = string(data) + string(legacy)
				} else {
					text, _, _ = strings.Cut(text, "</head>")
				}
				if strings.Contains(text, "untrusted.invalid") || strings.Contains(text, "cdn.example/static/opengraph") || strings.Contains(text, "/site/fr/opengraph-image") {
					t.Fatal("social image origin/mount inferred from request", text)
				}
				if path == "" {
					for _, want := range []string{"https://trusted.example/site/opengraph-image.png", "https://trusted.example/site/twitter-image.png", "og:image:width", "twitter:image:alt"} {
						if !strings.Contains(text, want) {
							t.Fatal("missing managed social default", want, text)
						}
					}
				} else if path == "/clear" {
					if strings.Contains(text, "og:image") || strings.Contains(text, "twitter:image") || strings.Contains(text, "twitter:card") {
						t.Fatal("empty list did not clear social group", text)
					}
				} else if path == "/explicit" {
					if strings.Contains(text, "opengraph-image.png") || strings.Contains(text, "twitter-image.png") || !strings.Contains(text, "explicit.png") {
						t.Fatal("explicit images did not replace defaults", text)
					}
				} else if !strings.Contains(text, "legacy.png") || strings.Contains(text, "twitter-image.png") {
					t.Fatal("legacy named metadata duplicated default", text)
				}
			}
		}
	}
	for _, name := range []string{"opengraph-image.png", "twitter-image.png", "opengraph-image.alt.txt", "twitter-image.alt.txt"} {
		if got := managedRequest(app, "GET", "https://untrusted.invalid/site/"+name, nil); got.Code != http.StatusOK {
			t.Fatal(name, got.Code)
		}
		if got := managedRequest(app, "GET", "https://untrusted.invalid/site/fr/"+name, nil); got.Code == http.StatusOK {
			t.Fatal("localized alias served global resource", name)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("requests reran producer", calls.Load())
	}
}

func TestManagedSocialMetadataRequiresExplicitBaseForAutomaticHead(t *testing.T) {
	image := managedMetadataFS(t)["icon.png"].Data
	app := managedMetadataApp(t, Config{MetadataFiles: metadatafiles.Sources{Static: fstest.MapFS{"opengraph-image.png": {Data: image}}}, Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) { return Page{Body: ui.Text("no trusted origin configured")}, nil }}}})
	got := managedRequest(app, "GET", "https://untrusted.invalid/", nil)
	if got.Code != 200 || strings.Contains(got.Body.String(), "og:image") || strings.Contains(got.Body.String(), "untrusted.invalid") {
		t.Fatal(got.Code, got.Body.String())
	}
	if got := managedRequest(app, "GET", "https://untrusted.invalid/opengraph-image.png", nil); got.Code != 200 {
		t.Fatal("missing base disabled public bytes", got.Code)
	}
}
