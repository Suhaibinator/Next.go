//go:build js && wasm && !nextgo_module

package client

import (
	"syscall/js"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/paths"
)

func TestDocumentSubtreesRejectClientNavigationAndPrefetch(t *testing.T) {
	locationEnvironment(t)
	oldDoc, oldPaths, oldManifest, oldURL, oldFrozen := doc, deployment, routeManifest, currentURL, draftFrozen
	t.Cleanup(func() {
		doc, deployment, routeManifest, currentURL, draftFrozen = oldDoc, oldPaths, oldManifest, oldURL, oldFrozen
	})
	currentURL = "https://site.test/app/fr/current"
	deployment = paths.Config{BasePath: "/app/fr"}
	routeManifest = navigation.Manifest{Documents: []string{"/blog/*nextgoDocument?"}, Routes: []navigation.Route{{Pattern: "/blog/new"}, {Pattern: "/health", Endpoint: true}}}
	draftFrozen = false
	doc = js.ValueOf(map[string]any{"baseURI": currentURL})
	win.Get("location").Set("origin", "https://site.test")
	win.Get("location").Set("href", currentURL)
	requests := 0
	mockBrowserFetch(t, func([]js.Value) any { requests++; t.Error("document boundary issued SPA fetch"); return js.Undefined() })
	anchor := js.Global().Get("Object").New()
	anchor.Set("nodeType", 1)
	closest := js.FuncOf(func(js.Value, []js.Value) any { return anchor })
	hasAttribute := js.FuncOf(func(js.Value, []js.Value) any { return false })
	var href string
	getAttribute := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if args[0].String() == "href" {
			return href
		}
		return js.Null()
	})
	defer closest.Release()
	defer hasAttribute.Release()
	defer getAttribute.Release()
	anchor.Set("closest", closest)
	anchor.Set("hasAttribute", hasAttribute)
	anchor.Set("getAttribute", getAttribute)
	for _, target := range []string{"/app/fr/blog", "/app/fr/blog/", "/app/fr/blog/new?q=one#part", "/app/fr/blog/a%20b", "/app/fr/%62log/a"} {
		href = target
		if _, _, eligible := eligibleLink(anchor); eligible {
			t.Fatal("native anchor would be intercepted", target)
		}
		u := win.Get("URL").New(target, currentURL)
		if eligibleURL(u) {
			t.Fatal("document target eligible for link interception", target)
		}
		if err := Navigate(target); err == nil {
			t.Fatal("Navigate accepted document target", target)
		}
		if err := Prefetch(target); err == nil {
			t.Fatal("Prefetch accepted document target", target)
		}
	}
	if requests != 0 || currentURL != "https://site.test/app/fr/current" {
		t.Fatal("document rejection changed view or issued work", requests, currentURL)
	}
	for _, target := range []string{"/app/fr/blogger", "/app/fr/other?q=one#part"} {
		if !eligibleURL(win.Get("URL").New(target, currentURL)) {
			t.Fatal("ordinary page became ineligible", target)
		}
	}
	for _, target := range []string{"/app/fr/health", "/app/fr/api/health", "/app/fr/assets/site.css", "/app/fr/_nextgo/build/app.wasm", "/app/other/blog", "/%61pp/fr/blog", "https://outside.test/app/fr/blog"} {
		if eligibleURL(win.Get("URL").New(target, currentURL)) {
			t.Fatal("endpoint, asset or foreign scope became eligible", target)
		}
	}
}
