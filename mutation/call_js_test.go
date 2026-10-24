//go:build js && wasm

package mutation

import (
	"context"
	"errors"
	"strings"
	"syscall/js"
	"testing"

	"github.com/Suhaibinator/Next.go/client"
)

func callEnvironment(t *testing.T, handler func([]js.Value) any) {
	t.Helper()
	global := js.Global()
	originalFetch, originalLocation, originalDocument := global.Get("fetch"), global.Get("location"), global.Get("document")
	global.Set("location", js.ValueOf(map[string]any{"href": "https://site.test/current", "origin": "https://site.test"}))
	global.Set("document", js.ValueOf(map[string]any{"baseURI": "https://foreign.test/"}))
	fn := js.FuncOf(func(_ js.Value, args []js.Value) any { return handler(args) })
	global.Set("fetch", fn)
	t.Cleanup(func() {
		global.Set("fetch", originalFetch)
		global.Set("location", originalLocation)
		global.Set("document", originalDocument)
		fn.Release()
	})
}
func callResponse(body string, headers map[string]any) any {
	response := js.Global().Get("Response").New(body, map[string]any{"status": 200, "headers": headers})
	js.Global().Get("Object").Call("defineProperty", response, "url", map[string]any{"value": "https://site.test/_nextgo/actions/add/v1"})
	return js.Global().Get("Promise").Call("resolve", response)
}

func TestCallUsesTypedBodySameOriginRevisionAndRedirectRejection(t *testing.T) {
	c := Must[inputJS, outputJS]("add", "v1")
	requests := 0
	callEnvironment(t, func(args []js.Value) any {
		requests++
		options := args[1]
		if args[0].String() != "https://site.test/_nextgo/actions/add/v1" || options.Get("method").String() != "POST" || options.Get("redirect").String() != "error" || options.Get("cache").String() != "no-store" {
			t.Error("unsafe request", args[0], options)
		}
		if options.Get("headers").Call("get", "X-Nextgo-Action-Revision").String() != "v1" {
			t.Error("missing revision")
		}
		body := make([]byte, options.Get("body").Get("byteLength").Int())
		js.CopyBytesToGo(body, options.Get("body"))
		if string(body) != `{"amount":2}` {
			t.Error(string(body))
		}
		return callResponse(`{"data":{"count":3},"redirect":"/done"}`, map[string]any{"Content-Type": "application/json", "X-Nextgo-Action-Protocol": "1", "X-Nextgo-Action-Revision": "v1"})
	})
	response, err := c.Call(context.Background(), inputJS{2})
	if err != nil || response.Data.Count != 3 || response.Redirect != "/done" || requests != 1 {
		t.Fatal(response, err, requests)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.Call(ctx, inputJS{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := c.Call(context.Background(), inputJS{}, CallOptions{MaxInputBytes: 1}); err == nil {
		t.Fatal("oversized input dispatched")
	}
	if _, err := c.Call(context.Background(), inputJS{}, CallOptions{MaxResponseBytes: -1}); err == nil {
		t.Fatal("invalid limit dispatched")
	}
	if requests != 1 {
		t.Fatal("invalid calls dispatched", requests)
	}
}

type inputJS struct {
	Amount int `json:"amount"`
}
type outputJS struct {
	Count int `json:"count"`
}

func TestCallRejectsPoisonedProtocolAndOversizedBodies(t *testing.T) {
	for _, kind := range []string{"missing-marker", "wrong-content-type", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			callEnvironment(t, func([]js.Value) any {
				headers := map[string]any{"Content-Type": "application/json", "X-Nextgo-Action-Protocol": "1", "X-Nextgo-Action-Revision": "v1"}
				body := `{"data":{"count":3}}`
				if kind == "missing-marker" {
					delete(headers, "X-Nextgo-Action-Protocol")
				}
				if kind == "wrong-content-type" {
					headers["Content-Type"] = "text/html"
				}
				if kind == "oversized" {
					body = strings.Repeat("x", 2048)
				}
				return callResponse(body, headers)
			})
			_, err := Must[inputJS, outputJS]("add", "v1").Call(context.Background(), inputJS{}, CallOptions{MaxResponseBytes: 1024})
			if err == nil {
				t.Fatal("poisoned response accepted", kind)
			}
		})
	}
}

func TestCallMultipartSharesProtocolOriginAndTypedResponseChecks(t *testing.T) {
	c := Must[inputJS, outputJS]("add", "v1")
	form, err := client.NewMultipart(8192)
	if err != nil {
		t.Fatal(err)
	}
	if err := form.AddField("amount", "2"); err != nil {
		t.Fatal(err)
	}
	requests := 0
	callEnvironment(t, func(args []js.Value) any {
		requests++
		options := args[1]
		if args[0].String() != "https://site.test/_nextgo/actions/add/v1" || options.Get("redirect").String() != "error" || options.Get("cache").String() != "no-store" {
			t.Error("transport policy lost")
		}
		if !options.Get("body").InstanceOf(js.Global().Get("FormData")) || options.Get("body").Call("get", "amount").String() != "2" {
			t.Error("form lost")
		}
		if !options.Get("headers").Call("get", "Content-Type").IsNull() || options.Get("headers").Call("get", "X-Nextgo-Action-Revision").String() != "v1" {
			t.Error("headers lost")
		}
		return callResponse(`{"data":{"count":3}}`, map[string]any{"Content-Type": "application/json", "X-Nextgo-Action-Protocol": "1", "X-Nextgo-Action-Revision": "v1"})
	})
	result, err := c.CallMultipart(context.Background(), form)
	if err != nil || result.Data.Count != 3 {
		t.Fatal(result, err)
	}
	if _, err := c.CallMultipart(context.Background(), form, CallOptions{MaxInputBytes: 1}); err == nil {
		t.Fatal("budget not checked")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.CallMultipart(ctx, form); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	form.Close()
	if _, err := c.CallMultipart(context.Background(), form); err == nil {
		t.Fatal("closed form")
	}
	if _, err := c.CallMultipart(context.Background(), nil); err == nil {
		t.Fatal("nil form")
	}
	if requests != 1 {
		t.Fatal(requests)
	}
}
