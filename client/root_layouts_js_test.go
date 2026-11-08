//go:build js && wasm && !nextgo_module

package client

import (
	"strings"
	"syscall/js"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/paths"
)

func TestIndependentRootLayoutsRejectCrossRootNavigationAndPrefetch(t *testing.T) {
	locationEnvironment(t)
	oldDoc, oldPaths, oldManifest, oldURL, oldFrozen := doc, deployment, routeManifest, currentURL, draftFrozen
	t.Cleanup(func() {
		doc, deployment, routeManifest, currentURL, draftFrozen = oldDoc, oldPaths, oldManifest, oldURL, oldFrozen
	})
	a, b := strings.Repeat("a", 64), strings.Repeat("b", 64)
	root := a
	attribute := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if args[0].String() == "data-nextgo-root" {
			return root
		}
		return js.Null()
	})
	defer attribute.Release()
	currentURL = "https://site.test/app/fr/a"
	doc = js.ValueOf(map[string]any{"baseURI": currentURL, "body": map[string]any{"getAttribute": attribute}})
	deployment = paths.Config{BasePath: "/app/fr"}
	routeManifest = navigation.Manifest{Routes: []navigation.Route{{Pattern: "/a", Root: a}, {Pattern: "/a/:id", Root: a}, {Pattern: "/b", Root: b}}}
	draftFrozen = false
	win.Get("location").Set("origin", "https://site.test")
	win.Get("location").Set("href", currentURL)
	requests := 0
	mockBrowserFetch(t, func([]js.Value) any { requests++; t.Error("cross-root request was fetched"); return js.Undefined() })
	for _, target := range []string{"/app/fr/b?q=1#part", "/app/fr/missing"} {
		if eligibleURL(win.Get("URL").New(target, currentURL)) {
			t.Fatal("root boundary accepted", target)
		}
		if Prefetch(target) == nil {
			t.Fatal("cross-root speculation accepted", target)
		}
	}
	if !rootBoundaryURL(win.Get("URL").New("/app/fr/b", currentURL)) || rootBoundaryURL(win.Get("URL").New("/app/fr/missing", currentURL)) {
		t.Fatal("known boundary classification")
	}
	if Navigate("/app/fr/missing") == nil {
		t.Fatal("unknown programmatic route accepted")
	}
	for _, target := range []string{"/app/fr/a", "/app/fr/a/a%20b?q=1#part"} {
		if !eligibleURL(win.Get("URL").New(target, currentURL)) {
			t.Fatal("same-root navigation denied", target)
		}
	}
	if requests != 0 {
		t.Fatal("cross-root speculation")
	}
	root = ""
	if eligibleURL(win.Get("URL").New("/app/fr/a", currentURL)) {
		t.Fatal("missing root accepted")
	}
}
