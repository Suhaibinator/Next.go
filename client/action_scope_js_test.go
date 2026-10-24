//go:build js && wasm

package client

import (
	"context"
	"errors"
	"syscall/js"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/internal/paths"
)

func fakeActionEnvironment(t *testing.T) {
	t.Helper()
	oldDoc, oldWin, oldDeployment := doc, win, deployment
	t.Cleanup(func() { doc, win, deployment = oldDoc, oldWin, oldDeployment })
	doc = js.ValueOf(map[string]any{"baseURI": "https://site.test/app/"})
	win = js.ValueOf(map[string]any{"URL": js.Global().Get("URL"), "location": map[string]any{"origin": "https://site.test"}})
	deployment = paths.Config{BasePath: "/app"}
}

func TestFrameworkActionScopeDoesNotInvalidateOtherOriginsOrMounts(t *testing.T) {
	fakeActionEnvironment(t)
	for _, tc := range []struct {
		target, method string
		want           bool
	}{
		{"/app/_nextgo/actions/save", "POST", true},
		{"https://SITE.test:443/app/_nextgo/actions/save", "post", true},
		{"_nextgo/actions/save", "POST", true},
		{"/app/_nextgo/actions/save", "GET", false},
		{"/app/_nextgo/actions/save", "DELETE", false},
		{"/_nextgo/actions/save", "POST", false},
		{"/other/_nextgo/actions/save", "POST", false},
		{"https://foreign.test/app/_nextgo/actions/save", "POST", false},
		{"https://user:pass@site.test/app/_nextgo/actions/save", "POST", false},
		{"/app/api/write?next=/_nextgo/actions/save", "POST", false},
		{"/app/_nextgo/actions/../api", "POST", false},
	} {
		if got := frameworkActionRequest(tc.target, tc.method); got != tc.want {
			t.Error(tc, got)
		}
	}
}

func TestActionFailuresInvalidateBeforeAndAfterTransport(t *testing.T) {
	for _, kind := range []string{"network", "http"} {
		t.Run(kind, func(t *testing.T) {
			fakeActionEnvironment(t)
			t.Cleanup(invalidateCache)
			pageCache.put("before", envelope{Title: "old"}, time.Minute)
			canceledBefore, canceledDuring := false, false
			prefetches["before"] = &prefetchRequest{done: make(chan struct{}), cancel: func() { canceledBefore = true }}
			before := cacheEpoch
			mockBrowserFetch(t, func([]js.Value) any {
				if _, ok := pageCache.get("before"); ok || !canceledBefore || cacheEpoch != before+1 {
					t.Error("dispatch did not invalidate preceding state")
				}
				pageCache.put("during", envelope{Title: "intervening"}, time.Minute)
				prefetches["during"] = &prefetchRequest{done: make(chan struct{}), cancel: func() { canceledDuring = true }}
				if kind == "network" {
					return js.Global().Get("Promise").Call("reject", js.Global().Get("Error").New("disconnected"))
				}
				return browserResponse([]byte("denied"), 403, nil)
			})
			response, err := Fetch(context.Background(), "/app/_nextgo/actions/save", FetchOptions{Method: "POST"})
			if kind == "network" && err == nil || kind == "http" && (err != nil || response.StatusCode != 403) {
				t.Fatal(response, err)
			}
			if _, ok := pageCache.get("during"); ok || !canceledDuring || cacheEpoch != before+2 || len(prefetches) != 0 {
				t.Fatal("failed settlement retained intervening cache state")
			}
		})
	}
}

func TestAlreadyCanceledActionDoesNotDispatchOrInvalidate(t *testing.T) {
	fakeActionEnvironment(t)
	t.Cleanup(invalidateCache)
	before := cacheEpoch
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Fetch(ctx, "/app/_nextgo/actions/save", FetchOptions{Method: "POST"})
	if !errors.Is(err, context.Canceled) || cacheEpoch != before {
		t.Fatal(err, cacheEpoch, before)
	}
}
