package nextgo

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

func TestCachedPageOwnsRenderedMetadataBacking(t *testing.T) {
	parts := []string{"Headline", "Description", "source", "CMS", "en", "ltr", "Example"}
	blob := strings.Join(parts, "|") + strings.Repeat("x", 4<<20)
	offset := 0
	v := make([]string, len(parts))
	for i, p := range parts {
		v[i] = blob[offset : offset+len(p)]
		offset += len(p) + 1
	}
	page := Page{Title: v[0], Description: v[1], Meta: map[string]string{v[2]: v[3]}, Metadata: seo.Metadata{Language: v[4], Direction: v[5], OpenGraph: seo.OpenGraph{SiteName: v[6]}}, Body: ui.Text("Useful body")}
	app := testApp(t, Config{Routes: []Route{{Pattern: "/", Cache: &CachePolicy{TTL: time.Minute}, Page: func(*Context) (Page, error) { return page, nil }}}})
	req := httptest.NewRequest("GET", "http://example.test/", nil)
	w := httptest.NewRecorder()
	app.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	key := app.configurationID + "|" + req.Host + "|" + req.URL.RequestURI()
	value, info, err := cache.Lookup(context.Background(), app.config.Cache, key, cache.Options{TTL: time.Minute}, func(context.Context) (cachedPage, error) { return cachedPage{}, fmt.Errorf("unexpected load") })
	if err != nil || info.State != cache.Fresh {
		t.Fatal(info, err)
	}
	base := uintptr(unsafe.Pointer(unsafe.StringData(blob)))
	end := base + uintptr(len(blob))
	owns := func(s string) {
		t.Helper()
		if len(s) > 0 {
			at := uintptr(unsafe.Pointer(unsafe.StringData(s)))
			if at >= base && at < end {
				t.Fatalf("cached %q aliases large CMS input", s)
			}
		}
	}
	owns(value.Page.Title)
	owns(value.Page.Description)
	owns(value.HTML)
	for k, s := range value.Page.Meta {
		owns(k)
		owns(s)
	}
	if value.Page.document == nil || value.Page.preparedHead == nil {
		t.Fatal("missing frozen document/head")
	}
	owns(value.Page.document.Language)
	owns(value.Page.document.Direction)
	owns(value.Page.document.Viewport)
	owns(value.Page.preparedHead.markup)
	for _, e := range value.Page.preparedHead.elements {
		owns(e.Tag)
		owns(e.Text)
		for k, s := range e.Attrs {
			owns(k)
			owns(s)
		}
	}
	if value.Page.Title != "Headline" || value.Page.Meta["source"] != "CMS" || value.Page.document.Language != "en" {
		t.Fatal("freezing changed metadata")
	}
}
