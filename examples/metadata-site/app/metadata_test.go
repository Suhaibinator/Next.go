//go:build !js

package app

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/metadata-site/app/about"
	"github.com/Suhaibinator/Next.go/examples/metadata-site/app/disabled"
	"github.com/Suhaibinator/Next.go/examples/metadata-site/app/override"
	"github.com/Suhaibinator/Next.go/internal/paths"
	"github.com/Suhaibinator/Next.go/metadatafiles"
)

func TestOriginalFilesAndStartupFactoriesAtMount(t *testing.T) {
	s, err := metadatafiles.New(context.Background(), metadatafiles.Sources{Static: os.DirFS("."), Robots: Robots, Sitemap: Sitemap, Manifest: Manifest}, metadatafiles.Options{BasePath: "/notes"})
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Entries()) != 6 {
		t.Fatal(s.Entries())
	}
	for _, entry := range s.Entries() {
		if entry.Path != "/notes/"+entry.Name {
			t.Fatal(entry)
		}
		get := httptest.NewRecorder()
		s.ServeHTTP(get, httptest.NewRequest("GET", "http://untrusted.test"+entry.Path, nil))
		if get.Code != 200 || get.Body.Len() != int(entry.Size) || get.Header().Get("ETag") == "" {
			t.Fatal(entry.Name, get.Code, get.Header())
		}
		head := httptest.NewRecorder()
		s.ServeHTTP(head, httptest.NewRequest("HEAD", "http://untrusted.test"+entry.Path, nil))
		if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("Content-Length") != get.Header().Get("Content-Length") {
			t.Fatal(entry.Name, "HEAD", head.Code)
		}
		r := httptest.NewRequest("GET", "http://untrusted.test"+entry.Path, nil)
		r.Header.Set("If-None-Match", get.Header().Get("ETag"))
		conditional := httptest.NewRecorder()
		s.ServeHTTP(conditional, r)
		if conditional.Code != 304 || conditional.Body.Len() != 0 {
			t.Fatal(entry.Name, "conditional", conditional.Code)
		}
	}
	robots, _ := s.Read("robots.txt")
	if !bytes.Contains(robots, []byte("Sitemap: https://example.test/notes/sitemap.xml")) || bytes.Contains(robots, []byte("untrusted")) {
		t.Fatal(string(robots))
	}
	sitemap, _ := s.Read("sitemap.xml")
	if !bytes.Contains(sitemap, []byte("https://example.test/notes/about")) {
		t.Fatal(string(sitemap))
	}
	manifestData, _ := s.Read("manifest.webmanifest")
	manifest, err := metadatafiles.ParseManifest(manifestData, metadatafiles.Options{BasePath: "/notes"})
	if err != nil || manifest.StartURL != "./" || len(manifest.Icons) != 1 || manifest.Icons[0].Src != "icon.png" {
		t.Fatal(manifest, err)
	}
}

func TestExamplePagesKeepPublicURLsAndExplicitChoices(t *testing.T) {
	c := &nextgo.Context{Context: paths.WithContext(context.Background(), paths.Config{BasePath: "/notes"})}
	p, err := Page(c)
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	if err := p.Body.Render(&body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body.String(), `href="/notes/manifest.webmanifest"`) || !strings.Contains(body.String(), `href="/notes/icon.png"`) {
		t.Fatal(body.String())
	}
	a, err := about.Page(c)
	if err != nil || a.Metadata.Icons != nil || a.Metadata.Manifest != nil {
		t.Fatal(a, err)
	}
	o, err := override.Page(c)
	if err != nil || len(o.Metadata.Icons) != 1 || o.Metadata.Manifest == nil || *o.Metadata.Manifest != "/manifest.webmanifest?explicit=1" {
		t.Fatal(o, err)
	}
	d, err := disabled.Page(c)
	if err != nil || d.Metadata.Icons == nil || len(d.Metadata.Icons) != 0 || d.Metadata.Manifest == nil || *d.Metadata.Manifest != "" {
		t.Fatal(d, err)
	}
}

func TestConfiguredPagesAdvertiseRetainedLayoutPolicy(t *testing.T) {
	t.Setenv("NEXTGO_METADATA_BASE_PATH", "/notes")
	t.Setenv("NEXTGO_METADATA_ASSET_PREFIX", "https://cdn.example.test/static")
	c := nextgo.Config{BuildID: "metadata-layout-test", Routes: []nextgo.Route{
		{Pattern: "/", Page: Page, Layouts: []nextgo.LayoutFunc{Layout}, LayoutIDs: []string{"metadata-root"}, LayoutBases: []string{"/"}},
		{Pattern: "/about", Page: about.Page, Layouts: []nextgo.LayoutFunc{Layout}, LayoutIDs: []string{"metadata-root"}, LayoutBases: []string{"/"}},
	}}
	Configure(&c)
	a, err := nextgo.New(c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	for _, path := range []string{"/notes", "/notes/about"} {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest("GET", "http://example.test"+path, nil))
		if w.Code != 200 {
			t.Fatal(path, w.Code, w.Body.String())
		}
		for _, want := range []string{`data-nextgo-preserve-layouts="true"`, `data-nextgo-layout="metadata-root"`, `data-nextgo-outlet="metadata-root"`, `data-nextgo-island="counter"`, `data-nextgo-assets="https://cdn.example.test/static"`} {
			if !strings.Contains(w.Body.String(), want) {
				t.Fatalf("%s missing %s", path, want)
			}
		}
	}
}
