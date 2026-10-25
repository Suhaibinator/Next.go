//go:build !js

package app

import (
	"context"
	"encoding/json"
	nextgo "github.com/Suhaibinator/Next.go"
	"independent.example/alpha/app/api/state"
	"independent.example/alpha/internal/state"
	"independent.example/alpha/mutations"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublicSSRUsesPinnedPathsAndTypedNativePreview(t *testing.T) {
	c := nextgo.Config{BuildID: "independent-unit", BasePath: "/mount", AssetPrefix: "/release-assets", Routes: []nextgo.Route{{Pattern: "/", Page: Page, Layouts: []nextgo.LayoutFunc{Layout}, LayoutIDs: []string{"root"}}, {Pattern: "/api/state", API: stateapi.Handle}}}
	Configure(&c)
	if c.BasePath != "/mount" || c.AssetPrefix != "/release-assets" || c.BuildID != "independent-unit" {
		t.Fatal("application changed wrapper-owned placement")
	}
	a, err := nextgo.New(c)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := a.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "http://example.test/mount", nil))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, want := range []string{`data-independent-zone="alpha"`, `data-independent-instance="primary"`, `data-independent-instance="secondary"`, `data-independent-count="">3`, `data-independent-count="">10`, `href="/release-assets/assets/card.css"`, `href="/release-assets/assets/release.txt"`, `action="/mount/_nextgo/actions/preview/v1"`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Fatalf("missing %s", want)
		}
	}
	before := state.Revision()
	r := httptest.NewRequest("POST", "http://example.test/mount/_nextgo/actions/preview/v1", strings.NewReader(`{"amount":2}`))
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json")
	r.Header.Set("Origin", "http://example.test")
	w = httptest.NewRecorder()
	a.ServeHTTP(w, r)
	var response struct {
		Data mutations.Output `json:"data"`
	}
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Data.Applied != 2 || response.Data.Revision != before+2 || response.Data.NativeVersion != state.NativeVersion || len(response.Data.Instance) != 32 {
		t.Fatal(w.Code, w.Body.String())
	}
	r = httptest.NewRequest("POST", "http://example.test/mount/_nextgo/actions/preview/v1", strings.NewReader("amount=9"))
	r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	r.Header.Set("Origin", "http://example.test")
	r.Header.Set("Accept", "application/json")
	w = httptest.NewRecorder()
	a.ServeHTTP(w, r)
	if w.Code != 422 || state.Revision() != before+2 {
		t.Fatal("invalid preview committed", w.Code, w.Body.String())
	}
}
