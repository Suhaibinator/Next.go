//go:build js && wasm && !nextgo_module

package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"syscall/js"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/paths"
)

type staticShellFixture struct {
	dom        js.Value
	descriptor navigation.StaticShellDescriptor
	seed       envelope
	body       []byte
	parses     int
}

// Reuse the ClientView ownership DOM double. Its parser returns a prepared
// tree, so these are Go/WASM transport/guard tests, not real browser HTML tests.
func staticShellEnvironment(t *testing.T) *staticShellFixture {
	t.Helper()
	oldDoc, oldWin, oldPaths, oldManifest := doc, win, deployment, routeManifest
	oldShell, oldURL, oldLocation := staticShell, currentURL, committedLocation
	oldEpoch := cacheEpoch
	f := &staticShellFixture{dom: clientOnlyDOM(t)}
	f.dom.Get("root").Call("setAttribute", "data-nextgo-island", "workspace")
	deployment = paths.Config{BasePath: "/app"}
	routeManifest = navigation.Manifest{Routes: []navigation.Route{{Pattern: "/workspace/*rest?"}, {Pattern: "/about"}}}
	f.seed = envelope{Version: navigation.ProtocolVersion, Build: strings.Repeat("a", 20), Configuration: strings.Repeat("b", 64), URL: "/app/workspace", HTML: `<div data-nextgo-client="1" data-nextgo-island="workspace" data-nextgo-props="null"><p>Public fallback</p></div>`, Status: 200, Document: navigation.Document{Language: "en", Direction: "ltr", Viewport: "width=device-width"}}
	f.body, _ = json.Marshal(f.seed)
	sum := sha256.Sum256(f.body)
	f.descriptor = navigation.StaticShellDescriptor{Version: navigation.StaticShellVersion, Build: f.seed.Build, Configuration: f.seed.Configuration, Pattern: "/workspace/*rest?", Component: "workspace", SeedURL: f.seed.URL, SHA256: hex.EncodeToString(sum[:]), Bytes: len(f.body)}
	f.descriptor.EnvelopeURL, _ = navigation.StaticShellEnvelopeURL(deployment, f.seed.Build, f.descriptor.SHA256)
	raw, err := navigation.EncodeStaticShellDescriptor(f.descriptor, deployment, f.seed.Build, f.seed.Configuration)
	if err != nil {
		t.Fatal(err)
	}
	body := f.dom.Call("make", "body", map[string]any{"data-nextgo-export": "true", "data-nextgo-build": f.seed.Build, "data-nextgo-configuration": f.seed.Configuration, staticShellAttribute: string(raw)})
	head := f.dom.Call("make", "head")
	doc = f.dom.Get("owner")
	doc.Set("body", body)
	doc.Set("head", head)
	doc.Set("baseURI", "https://site.test/app/workspace/deep?q=one#part")
	getElement := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if args[0].String() == "nextgo-app" {
			return f.dom.Get("outer")
		}
		return nil
	})
	parse := js.FuncOf(func(js.Value, []js.Value) any {
		f.parses++
		return map[string]any{"body": f.dom.Get("outer"), "head": map[string]any{"childNodes": []any{}}, "querySelectorAll": f.dom.Get("outer").Get("querySelectorAll").Call("bind", f.dom.Get("outer"))}
	})
	doc.Set("getElementById", getElement)
	parser := js.Global().Get("Function").New("parse", "return function(){return {parseFromString:parse}}").Invoke(parse)
	win = js.ValueOf(map[string]any{"URL": js.Global().Get("URL"), "Object": js.Global().Get("Object"), "DOMParser": parser, "location": map[string]any{"href": "https://site.test/app/workspace/deep?q=one#part", "origin": "https://site.test"}})
	staticShell = &f.descriptor
	currentURL = "https://site.test/app/about"
	committedLocation = Location{URL: currentURL, Pathname: "/app/about"}
	t.Cleanup(func() {
		doc, win, deployment, routeManifest = oldDoc, oldWin, oldPaths, oldManifest
		staticShell, currentURL, committedLocation = oldShell, oldURL, oldLocation
		cacheEpoch = oldEpoch
		getElement.Release()
		parse.Release()
	})
	return f
}

func staticShellResponse(body []byte, status int, mime, target string) js.Value {
	array := js.Global().Get("Uint8Array").New(len(body))
	js.CopyBytesToJS(array, body)
	response := js.Global().Get("Response").New(array, map[string]any{"status": status, "headers": map[string]any{"Content-Type": mime, "Cache-Control": "no-store"}})
	js.Global().Get("Object").Call("defineProperty", response, "url", map[string]any{"value": target})
	return js.Global().Get("Promise").Call("resolve", response)
}

func TestStaticShellFetchUsesImmutableAssetAndRebindsOnlyValidatedCopy(t *testing.T) {
	f := staticShellEnvironment(t)
	stored := append([]byte(nil), f.body...)
	calls := 0
	mockBrowserFetch(t, func(args []js.Value) any {
		calls++
		want := "https://site.test" + f.descriptor.EnvelopeURL
		if args[0].String() != want || args[1].Get("redirect").String() != "error" {
			t.Error("not the declared immutable asset", args[0])
		}
		return staticShellResponse(f.body, 200, "application/json", want)
	})
	target := "https://site.test/app/workspace/a%20b/?q=two#part"
	data, _, fallback, err := fetchEnvelope(context.Background(), target)
	if err != nil || fallback || calls != 1 || data.URL != "https://site.test/app/workspace/a%20b?q=two#part" {
		t.Fatal(data.URL, calls, fallback, err)
	}
	if currentURL != "https://site.test/app/about" || committedLocation.Pathname != "/app/about" {
		t.Fatal("pending shell fetch published its target")
	}
	var original envelope
	if err := json.Unmarshal(f.body, &original); err != nil || original.URL != f.descriptor.SeedURL || !bytes.Equal(stored, f.body) || data.HTML != original.HTML {
		t.Fatal("immutable seed bytes or content changed", err)
	}
}

func TestStaticShellDoesNotReplaceOrdinaryMissingJSONOrWinningLiteral(t *testing.T) {
	for _, literal := range []bool{false, true} {
		t.Run(map[bool]string{false: "outside", true: "specific-page"}[literal], func(t *testing.T) {
			f := staticShellEnvironment(t)
			target, want := "https://site.test/app/missing?q=one", "https://site.test/app/_nextgo/data/missing.json"
			if literal {
				routeManifest.Routes = append([]navigation.Route{{Pattern: "/workspace/specific"}}, routeManifest.Routes...)
				target, want = "https://site.test/app/workspace/specific", "https://site.test/app/_nextgo/data/workspace/specific.json"
			}
			calls := 0
			mockBrowserFetch(t, func(args []js.Value) any {
				calls++
				if args[0].String() != want {
					t.Error("ordinary route borrowed the shell", args[0])
				}
				return staticShellResponse([]byte("missing"), 404, "text/plain", want)
			})
			_, _, _, _ = fetchEnvelope(context.Background(), target)
			if calls != 1 || f.parses != 0 {
				t.Fatal("missing JSON caused a second shell request or decode", calls, f.parses)
			}
		})
	}
}

func TestStaticShellTransportAndDigestFailuresNeverDecodeOrPublish(t *testing.T) {
	for _, kind := range []string{"hash", "length", "status", "mime", "mime-ambiguous", "response-url", "cancelled", "cancel-on-response"} {
		t.Run(kind, func(t *testing.T) {
			f := staticShellEnvironment(t)
			body := append([]byte(nil), f.body...)
			status, mime, responseURL := 200, "application/json", "https://site.test"+f.descriptor.EnvelopeURL
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch kind {
			case "hash":
				body[0] = '['
			case "length":
				body = body[:len(body)-1]
			case "status":
				status = 404
			case "mime":
				mime = "text/html"
			case "mime-ambiguous":
				mime = "application/json-bogus"
			case "response-url":
				responseURL = "https://other.test" + f.descriptor.EnvelopeURL
			case "cancelled":
				cancel()
			}
			calls := 0
			mockBrowserFetch(t, func([]js.Value) any {
				calls++
				if kind == "cancel-on-response" {
					cancel()
				}
				return staticShellResponse(body, status, mime, responseURL)
			})
			_, _, fallback, err := fetchEnvelope(ctx, "https://site.test/app/workspace/new?q=two")
			if err == nil || fallback || f.parses != 0 || currentURL != "https://site.test/app/about" || committedLocation.Pathname != "/app/about" {
				t.Fatal("invalid asset decoded, reloaded, or changed location", err, fallback, f.parses)
			}
			if kind == "cancelled" && (!errors.Is(err, context.Canceled) || calls != 0) {
				t.Fatal("pre-cancelled shell fetch dispatched", err, calls)
			}
		})
	}
}

func TestStaticShellInitialDescriptorAdmissionKeepsFallback(t *testing.T) {
	for _, kind := range []string{"valid", "ordinary", "malformed", "not-export", "wrong-build", "missing-route", "winning-literal", "outside", "draft", "locale", "parallel", "preserve-layouts", "offline", "selection"} {
		t.Run(kind, func(t *testing.T) {
			f := staticShellEnvironment(t)
			body := doc.Get("body")
			switch kind {
			case "ordinary":
				win.Get("location").Set("href", "https://site.test/app/about")
				f.dom.Get("root").Call("setAttribute", "data-nextgo-island", "ordinary")
			case "malformed":
				body.Call("setAttribute", staticShellAttribute, "{")
			case "not-export":
				body.Call("removeAttribute", "data-nextgo-export")
			case "wrong-build":
				body.Call("setAttribute", "data-nextgo-build", strings.Repeat("c", 20))
			case "missing-route":
				routeManifest.Routes = nil
			case "winning-literal":
				routeManifest.Routes = append([]navigation.Route{{Pattern: "/workspace/deep"}}, routeManifest.Routes...)
			case "outside":
				win.Get("location").Set("href", "https://site.test/app/outside")
			case "draft":
				routeManifest.Draft = true
			case "locale":
				routeManifest.Locale = "fr"
			case "parallel":
				routeManifest.Parallel = "parallel-policy"
			case "preserve-layouts":
				body.Call("setAttribute", "data-nextgo-preserve-layouts", "true")
			case "offline":
				body.Call("setAttribute", "data-nextgo-offline", "bad")
			case "selection":
				f.dom.Get("outer").Call("setAttribute", "data-nextgo-selection", "stale")
			}
			err := initializeStaticShellDocument()
			valid := kind == "valid" || kind == "ordinary"
			if (err == nil) != valid || (staticShell != nil) != valid {
				t.Fatal(kind, err, staticShell)
			}
			if !f.dom.Get("root").Get("firstChild").Equal(f.dom.Get("fallback")) || f.parses != 0 || currentURL != "https://site.test/app/about" {
				t.Fatal("initial validation mounted or changed useful fallback")
			}
		})
	}
}

func TestStaticShellSeedRejectsStaleFrameworkTreesAndPathMetadata(t *testing.T) {
	for _, kind := range []string{"missing", "duplicate", "ordinary-island", "different-component"} {
		t.Run(kind, func(t *testing.T) {
			f := staticShellEnvironment(t)
			switch kind {
			case "missing":
				f.dom.Get("outer").Call("removeChild", f.dom.Get("root"))
			case "duplicate":
				f.dom.Get("outer").Call("appendChild", f.dom.Call("make", "div", map[string]any{"data-nextgo-island": "workspace", "data-nextgo-client": "1", "data-nextgo-props": "null"}))
			case "ordinary-island":
				f.dom.Get("root").Call("removeAttribute", clientBoundaryAttribute)
			case "different-component":
				f.dom.Get("root").Call("setAttribute", "data-nextgo-island", "different")
			}
			if err := validateStaticShellTree(f.dom.Get("outer"), "workspace"); err == nil {
				t.Fatal("invalid ClientIsland declaration admitted", kind)
			}
		})
	}
	for _, tag := range []string{"nextgo-layout", "nextgo-outlet", "nextgo-slot", "nextgo-template", "template", "script"} {
		t.Run(tag, func(t *testing.T) {
			f := staticShellEnvironment(t)
			f.dom.Get("outer").Call("appendChild", f.dom.Call("make", tag))
			if err := validateStaticShellTree(f.dom.Get("outer"), "workspace"); err == nil {
				t.Fatal("stale framework tree admitted", tag)
			}
		})
	}
	for _, marker := range []string{"data-nextgo-selection", "data-nextgo-boundary", "data-nextgo-chunk", "data-nextgo-metadata", "data-nextgo-region", "data-nextgo-slot"} {
		t.Run(marker, func(t *testing.T) {
			f := staticShellEnvironment(t)
			f.dom.Get("outer").Call("setAttribute", marker, "stale")
			if err := validateStaticShellTree(f.dom.Get("outer"), "workspace"); err == nil {
				t.Fatal("stale root marker admitted", marker)
			}
		})
	}
	for _, head := range []headElement{{Tag: "script"}, {Tag: "link", Attrs: map[string]string{"rel": "canonical"}}, {Tag: "link", Attrs: map[string]string{"rel": "alternate"}}, {Tag: "link", Attrs: map[string]string{"rel": "icon", "hreflang": ""}}, {Tag: "meta", Attrs: map[string]string{"property": "og:url"}}} {
		if err := validateStaticShellHeadElement(head.Tag, head.Attrs); err == nil {
			t.Fatal("path-specific head or structured data admitted", head)
		}
	}
	f := staticShellEnvironment(t)
	f.seed.URL += "?seed-query"
	if err := validateStaticShellSeed(f.seed, f.descriptor); err == nil {
		t.Fatal("seed URL with query admitted")
	}
}
