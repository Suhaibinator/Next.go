//go:build !js

package app

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/offline/app/guide"
	fallback "github.com/Suhaibinator/Next.go/examples/offline/app/offline"
	"github.com/Suhaibinator/Next.go/offline"
)

func TestOfflineDeclarationMatchesExplicitPublicRoutes(t *testing.T) {
	t.Setenv("NEXTGO_OFFLINE_ORIGIN", "https://reader.example")
	t.Setenv("NEXTGO_OFFLINE_BASE_PATH", "/reader")
	policy, err := offline.Normalize(Offline())
	if err != nil {
		t.Fatal(err)
	}
	declaration := policy.Config()
	if declaration.Origin != "https://reader.example" || declaration.BasePath != "/reader" || declaration.Semantics != offline.SavedAnonymousPublic || declaration.Fallback != "/offline" || declaration.Updates != offline.WaitForClosedClients || len(declaration.Pages) != 3 {
		t.Fatal(declaration)
	}
	c := nextgo.Config{Routes: []nextgo.Route{{Pattern: "/"}, {Pattern: "/guide"}, {Pattern: "/offline"}, {Pattern: "/api/health"}, {Pattern: "/not-approved"}}}
	Configure(&c)
	if c.BasePath != declaration.BasePath {
		t.Fatal("runtime mount differs from saved policy", c.BasePath)
	}
	for i, page := range declaration.Pages {
		if page.MaxAge != 24*time.Hour || c.Routes[i].Cache == nil || c.Routes[i].Cache.TTL != time.Hour {
			t.Fatal("public retention/cache declaration missing", page, c.Routes[i])
		}
	}
	for _, route := range c.Routes[3:] {
		if route.Cache != nil {
			t.Fatal("undeclared route received a public page policy", route.Pattern)
		}
	}
	// Operator input stays literal and is rejected by policy validation. The
	// application never derives a trusted origin from a request's Host header.
	for _, origin := range []string{"https://reader.example/path", "https://READER.example", "http://remote.example"} {
		t.Setenv("NEXTGO_OFFLINE_ORIGIN", origin)
		if _, err := offline.Normalize(Offline()); err == nil {
			t.Fatal("accepted noncanonical/insecure operator origin", origin)
		}
	}
}

func TestMeaningfulPublicSSRIncludesScopedNativeLinksAndGoControls(t *testing.T) {
	t.Setenv("NEXTGO_OFFLINE_BASE_PATH", "/reader")
	c := nextgo.Config{BuildID: "offline-sample", Phase: nextgo.PhaseProduction, Routes: []nextgo.Route{
		{Pattern: "/", Page: Page, Layouts: []nextgo.LayoutFunc{Layout}, LayoutIDs: []string{"offline-root"}},
		{Pattern: "/guide", Page: guide.Page, Layouts: []nextgo.LayoutFunc{Layout}, LayoutIDs: []string{"offline-root"}},
		{Pattern: "/offline", Page: fallback.Page, Layouts: []nextgo.LayoutFunc{Layout}, LayoutIDs: []string{"offline-root"}},
	}}
	Configure(&c)
	a, err := nextgo.New(c)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := a.Close(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	for _, page := range []struct{ path, marker, title string }{
		{"/reader", "overview", "Saved public pages"},
		{"/reader/guide", "guide", "Reading guide"},
		{"/reader/offline", "fallback", "Offline reading fallback"},
	} {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest("GET", "https://reader.example"+page.path, nil))
		if w.Code != 200 {
			t.Fatal(page.path, w.Code, w.Body.String())
		}
		for _, want := range []string{page.title, `data-offline-page="` + page.marker + `"`, `href="/reader/guide"`, `data-nextgo-reload=""`, `data-offline-enable="" disabled=""`, `data-offline-count="">3`, `Waiting for lazy Go`} {
			if !strings.Contains(w.Body.String(), want) {
				t.Fatalf("%s missing %q", page.path, want)
			}
		}
		// Every Go-owned button must preserve its SSR disabled gate across
		// reload instead of restoring a previous document's enabled state.
		for _, action := range []string{"enable", "status-button", "update", "disable", "increment"} {
			want := `<button autocomplete="off" data-offline-` + action + `="" disabled="" type="button">`
			if !strings.Contains(w.Body.String(), want) {
				t.Fatalf("%s missing reload-safe hydration gate for %s", page.path, action)
			}
		}
		if strings.Contains(w.Body.String(), "http://127.0.0.1") {
			t.Fatal("unscoped server origin appeared in page links")
		}
	}
}
