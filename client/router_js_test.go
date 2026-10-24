//go:build js && wasm

package client

import (
	"syscall/js"
	"testing"
)

func TestRouterAPIsRequireRunAndRejectNonPageTargets(t *testing.T) {
	oldDoc, oldWin, oldURL := doc, win, currentURL
	t.Cleanup(func() { doc, win, currentURL = oldDoc, oldWin, oldURL })
	currentURL = ""
	for _, call := range []func(string) error{Navigate, Replace, Prefetch} {
		if err := call("/about"); err == nil {
			t.Fatal("router API allowed use before Run")
		}
	}
	currentURL = "https://site.example/"
	doc = js.ValueOf(map[string]any{"baseURI": currentURL})
	win = js.ValueOf(map[string]any{"URL": js.Global().Get("URL"), "location": map[string]any{"origin": "https://site.example"}})
	for _, target := range []string{"https://foreign.example/about", "javascript:alert(1)", "https://user:password@site.example/about", "/api/health", "/assets/a.css", "/_nextgo/actions/example", "http://[bad"} {
		for _, call := range []func(string) error{Navigate, Replace, Prefetch} {
			if err := call(target); err == nil {
				t.Errorf("accepted %q", target)
			}
		}
	}
}
