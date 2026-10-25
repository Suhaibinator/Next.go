//go:build js && wasm

package client

import (
	"syscall/js"
	"testing"
)

func TestManifestNavigationHeadValidation(t *testing.T) {
	oldDoc, oldWin := doc, win
	t.Cleanup(func() { doc, win = oldDoc, oldWin })
	var functions []js.Func
	defer func() {
		for _, fn := range functions {
			fn.Release()
		}
	}()
	bind := func(fn func(js.Value, []js.Value) any) js.Func {
		f := js.FuncOf(fn)
		functions = append(functions, f)
		return f
	}
	var nodes []map[string]string
	appendChild := bind(func(js.Value, []js.Value) any { return nil })
	fragment := bind(func(js.Value, []js.Value) any { return map[string]any{"appendChild": appendChild} })
	create := bind(func(_ js.Value, args []js.Value) any {
		if args[0].String() != "link" {
			t.Error("unexpected element", args[0])
		}
		attrs := map[string]string{}
		nodes = append(nodes, attrs)
		set := bind(func(_ js.Value, args []js.Value) any { attrs[args[0].String()] = args[1].String(); return nil })
		return map[string]any{"setAttribute": set}
	})
	doc = js.ValueOf(map[string]any{"createDocumentFragment": fragment, "createElement": create, "baseURI": "https://site.test/app/"})
	win = js.ValueOf(map[string]any{"URL": js.Global().Get("URL")})
	for _, href := range []string{"/app/site.webmanifest", "https://cdn.example.test/site.webmanifest"} {
		for _, typed := range []bool{false, true} {
			attrs := map[string]string{"rel": "manifest", "href": href}
			if typed {
				attrs["type"] = "application/manifest+json"
			}
			if _, err := buildHead([]headElement{{Tag: "link", Attrs: attrs}}); err != nil {
				t.Fatal("valid manifest rejected", attrs, err)
			}
			if node := nodes[len(nodes)-1]; node["href"] != href || node["rel"] != "manifest" {
				t.Fatal("manifest changed during creation", node)
			}
		}
	}
	for _, attrs := range []map[string]string{
		{"rel": "manifest", "href": "manifest.json"},
		{"rel": "manifest", "href": "//other.test/m"},
		{"rel": "manifest", "href": "https://user@example.test/m"},
		{"rel": "manifest", "href": "/\\other.test/m"},
		{"rel": "manifest", "href": "/bad\nmanifest"},
		{"rel": "manifest", "href": "/bad manifest"},
		{"rel": "manifest", "href": "javascript:alert(1)"},
		{"rel": "manifest", "href": "/m", "type": "text/html"},
		{"rel": "manifest", "href": "/m", "type": ""},
		{"rel": "manifest", "href": "/m", "crossorigin": "anonymous"},
		{"rel": "manifest", "href": "/m", "integrity": "sha256-AAAA"},
		{"rel": "manifest", "href": "/m", "sizes": "any"},
		{"rel": "manifest", "href": "/m", "onload": "alert(1)"},
	} {
		before := len(nodes)
		if _, err := buildHead([]headElement{{Tag: "link", Attrs: attrs}}); err == nil || len(nodes) != before {
			t.Fatal("invalid manifest reached DOM", attrs, err)
		}
	}
}
