package nextgo

import (
	"context"
	"encoding/json"
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

func TestCachedPageOwnsRegionBackingWithoutCopyingFallbackPerRegion(t *testing.T) {
	names := "account-hole|account" + strings.Repeat("x", 4<<20)
	id, registration := names[:len("account-hole")], names[len("account-hole")+1:len("account-hole|account")]
	raw := []byte(`{"public":1}` + strings.Repeat(" ", 4<<20))
	props := json.RawMessage(raw[:len(`{"public":1}`)])
	shell := `<div aria-busy="true" data-nextgo-boundary="account-hole"><p>Public fallback</p></div>`
	bodyBacking := strings.Repeat("x", 2<<20) + shell + strings.Repeat("y", 2<<20)
	body := bodyBacking[2<<20 : (2<<20)+len(shell)]
	backing := make([]ui.RegionSpec, 1, 4096)
	backing[0] = ui.RegionSpec{ID: id, Region: registration, Props: props, Start: 0, End: len(body)}
	owned := ownCachedPage(cachedPage{HTML: body, Regions: backing})
	if len(owned.Regions) != 1 || cap(owned.Regions) != 1 || owned.HTML != shell || owned.Regions[0].Start != 0 || owned.Regions[0].End != len(shell) {
		t.Fatal("region snapshot changed or retained extra descriptor capacity", owned)
	}
	aliasesString := func(value, large string) bool {
		if value == "" {
			return false
		}
		at := uintptr(unsafe.Pointer(unsafe.StringData(value)))
		base := uintptr(unsafe.Pointer(unsafe.StringData(large)))
		return at >= base && at < base+uintptr(len(large))
	}
	if aliasesString(owned.HTML, bodyBacking) || aliasesString(owned.Regions[0].ID, names) || aliasesString(owned.Regions[0].Region, names) {
		t.Fatal("frozen shell/region identity retains oversized source backing")
	}
	at := uintptr(unsafe.Pointer(unsafe.SliceData(owned.Regions[0].Props)))
	base := uintptr(unsafe.Pointer(unsafe.SliceData(raw)))
	if at >= base && at < base+uintptr(len(raw)) {
		t.Fatal("region props retain oversized source backing")
	}
	backing[0].Start = 1
	backing[0].Region = "changed"
	raw[0] = '!'
	if owned.Regions[0].Start != 0 || owned.Regions[0].Region != "account" || string(owned.Regions[0].Props) != `{"public":1}` {
		t.Fatal("owned descriptors or props alias caller mutations", owned.Regions)
	}
	// A region records offsets into the one frozen HTML body, not another copy
	// of its fallback. The accounted body contribution must remain exactly once.
	withoutHTML := owned
	withoutHTML.HTML = ""
	if owned.CacheSize()-withoutHTML.CacheSize() != int64(len(shell)) {
		t.Fatal("fallback shell body is counted more than once")
	}
	withoutRegions := owned
	withoutRegions.Regions = nil
	if owned.CacheSize()-withoutRegions.CacheSize() < int64(len(id)+len(registration)+len(props)) {
		t.Fatal("region identity/props are absent from cache accounting")
	}
}

func TestCachedPageRegionOwnershipPreservesNilAndDropsSpareCapacity(t *testing.T) {
	if ownCachedPage(cachedPage{}).Regions != nil {
		t.Fatal("nil descriptor state changed")
	}
	source := make([]ui.RegionSpec, 0, 1000)
	owned := ownCachedPage(cachedPage{Regions: source})
	if owned.Regions == nil || len(owned.Regions) != 0 || cap(owned.Regions) != 0 {
		t.Fatal("empty descriptor snapshot retains source capacity")
	}
}
