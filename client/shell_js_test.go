//go:build js && wasm

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"syscall/js"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/internal/navigation"
)

func testShellEnvelope() envelope {
	return envelope{Version: navigation.ProtocolVersion, Build: "build", Configuration: strings.Repeat("b", 64), URL: "/partial?q=one", HTML: "<h1>Public fallback</h1>", Shell: strings.Repeat("a", 64), Status: 200, Title: "Public title", Description: "Public description", Meta: map[string]string{"robots": "index"}, Document: navigation.Document{Language: "en", Direction: "ltr", Viewport: "width=device-width"}, Head: []headElement{{Tag: "meta", Attrs: map[string]string{"name": "theme-color", "content": "#fff"}}}}
}
func TestShellReferenceOnlyRestoresPinnedHTMLAfterExactBinding(t *testing.T) {
	old := cacheEpoch
	defer func() { cacheEpoch = old }()
	cacheEpoch = 8
	for _, name := range []string{"valid", "no-hint", "epoch", "hash", "build", "configuration", "url", "title", "description", "meta", "document", "head", "status", "missing-cached-html", "cached-reference"} {
		t.Run(name, func(t *testing.T) {
			saved := testShellEnvelope()
			hint := &shellResumeHint{page: saved, epoch: cacheEpoch}
			// Decode independently so field mutations cannot alter the pinned snapshot.
			body, _ := json.Marshal(saved)
			var data envelope
			_ = json.Unmarshal(body, &data)
			data.HTML = ""
			data.ShellRef = true
			switch name {
			case "no-hint":
				hint = nil
			case "epoch":
				hint.epoch--
			case "hash":
				data.Shell = strings.Repeat("c", 64)
			case "build":
				data.Build = "different"
			case "configuration":
				data.Configuration = strings.Repeat("c", 64)
			case "url":
				data.URL = "/partial?q=other"
			case "title":
				data.Title = "forged"
			case "description":
				data.Description = "forged"
			case "meta":
				data.Meta["robots"] = "noindex"
			case "document":
				data.Document.Language = "fr"
			case "head":
				data.Head[0].Attrs["content"] = "#000"
			case "status":
				data.Status = 403
			case "missing-cached-html":
				hint.page.HTML = ""
			case "cached-reference":
				hint.page.ShellRef = true
			}
			err := applyShellReference(&data, hint, "https://example.test/partial?q=one")
			if name == "valid" {
				if err != nil || data.HTML != saved.HTML || data.shellEpoch != cacheEpoch {
					t.Fatal(data, err)
				}
			} else if err == nil || data.HTML != "" {
				t.Fatal("forged/unbound reference adopted", name, data, err)
			}
		})
	}
}

func TestShellEnvelopeFieldsRejectAmbiguousAndUnboundReferences(t *testing.T) {
	hash := strings.Repeat("a", 64)
	cases := []struct {
		name, body string
		valid      bool
	}{
		{"legacy", `{"html":"normal"}`, true},
		{"full", `{"shell":"` + hash + `","html":"public"}`, true},
		{"reference", `{"shell":"` + hash + `","shellRef":true,"html":""}`, true},
		{"no-generation", `{"shellRef":true}`, false},
		{"false-without-generation", `{"shellRef":false}`, false},
		{"null-generation", `{"shell":null}`, false},
		{"short-generation", `{"shell":"a"}`, false},
		{"null-flag", `{"shell":"` + hash + `","shellRef":null}`, false},
		{"null-html", `{"shell":"` + hash + `","shellRef":true,"html":null}`, false},
		{"nonempty-ref", `{"shell":"` + hash + `","shellRef":true,"html":"private"}`, false},
		{"alias", `{"Shell":"` + hash + `"}`, false},
		{"duplicate", `{"shell":"` + hash + `","shell":"` + hash + `"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var data envelope
			if err := json.Unmarshal([]byte(tc.body), &data); err != nil {
				t.Fatal(err)
			}
			err := validateShellFields([]byte(tc.body), data)
			if (err == nil) != tc.valid {
				t.Fatal(err, tc.valid)
			}
		})
	}
}

type shellObservedContext struct {
	context.Context
	observed chan struct{}
	once     sync.Once
}

func (c *shellObservedContext) Done() <-chan struct{} {
	c.once.Do(func() { close(c.observed) })
	return c.Context.Done()
}

func TestCachedPublicShellAlwaysContactsServerIncludingPendingPrefetch(t *testing.T) {
	for _, pending := range []bool{false, true} {
		t.Run(fmt.Sprint(pending), func(t *testing.T) {
			oldDoc, oldWin, oldID, oldEpoch, oldParallel, oldPrefetch := doc, win, navigationID, cacheEpoch, routeManifest.Parallel, prefetches
			defer func() {
				doc, win, navigationID, cacheEpoch, routeManifest.Parallel, prefetches = oldDoc, oldWin, oldID, oldEpoch, oldParallel, oldPrefetch
				pageCache.clear()
			}()
			getAttribute := js.FuncOf(func(js.Value, []js.Value) any { return nil })
			defer getAttribute.Release()
			doc = js.ValueOf(map[string]any{"body": map[string]any{"getAttribute": getAttribute}})
			win = js.Global()
			navigationID = 123
			cacheEpoch = 17
			routeManifest.Parallel = ""
			prefetches = map[string]*prefetchRequest{}
			pageCache.clear()
			target := "https://example.test/partial?q=one"
			saved := testShellEnvelope()
			saved.URL = target
			calls := 0
			mockBrowserFetch(t, func(args []js.Value) any {
				calls++
				headers := args[1].Get("headers")
				if headers.Call("get", navigation.ShellProtocolHeader).String() != "1" || headers.Call("get", navigation.ShellHeader).String() != saved.Shell {
					t.Error("cached shell did not become a negotiated resume hint")
				}
				return js.Global().Get("Promise").Call("reject", js.Global().Get("Error").New("authorization probe failed"))
			})
			ctx := context.Context(context.Background())
			if pending {
				waiting := &shellObservedContext{Context: context.Background(), observed: make(chan struct{})}
				ctx = waiting
				flight := &prefetchRequest{done: make(chan struct{}), cancel: func() {}}
				prefetches[target] = flight
				go func() { <-waiting.observed; pageCache.put(target, saved, time.Minute); close(flight.done) }()
			} else {
				pageCache.put(target, saved, time.Minute)
			}
			err := navigate(ctx, target, false, js.Undefined(), navigationID, NavigationOptions{})
			if calls != 1 || err == nil || !strings.Contains(err.Error(), "authorization probe failed") {
				t.Fatal("public shell committed without server revalidation", calls, err)
			}
			if _, ok := pageCache.get(target); ok {
				t.Fatal("failed resume retained its retry hint")
			}
		})
	}
}
