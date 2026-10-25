//go:build js && wasm

package client

import (
	"strings"
	"syscall/js"
	"testing"
	"time"
)

func viewportAuditWorker(t *testing.T, c *viewportController) {
	t.Helper()
	stopped := make(chan struct{})
	go func() { c.worker(0); close(stopped) }()
	t.Cleanup(func() {
		c.active, c.disposed = false, true
		c.signal()
		select {
		case <-stopped:
		case <-time.After(time.Second):
			t.Error("viewport audit worker did not stop")
		}
	})
}

func TestViewportAuditDispatchRevalidatesQueuedLink(t *testing.T) {
	for _, kind := range []string{"endpoint", "external", "different-page", "download", "target", "reload", "prefetch-disabled", "disconnected", "reparented", "offscreen", "epoch"} {
		t.Run(kind, func(t *testing.T) {
			f := viewportTestFixture(t)
			doc.Set("body", map[string]any{"getAttribute": f.function(func(js.Value, []js.Value) any { return nil })})
			f.c.scanning = true // Test dispatch independently of observer discovery.
			attrs := map[string]string{"href": "/viewport-audit/old", "data-nextgo-prefetch": "viewport"}
			node := f.link(attrs)
			candidate, ok := f.c.candidate(node)
			if !ok {
				t.Fatal("initial link was ineligible")
			}
			f.c.enqueue(node)
			if len(f.c.queue) != 1 {
				t.Fatal("initial link was not queued")
			}
			switch kind {
			case "endpoint":
				attrs["href"] = "/api/health"
			case "external":
				attrs["href"] = "https://other.test/private"
			case "different-page":
				attrs["href"] = "/viewport-audit/new"
			case "download":
				attrs["download"] = ""
			case "target":
				attrs["target"] = "_blank"
			case "reload":
				attrs["data-nextgo-reload"] = ""
			case "prefetch-disabled":
				attrs["data-nextgo-prefetch"] = "false"
			case "disconnected":
				node.Set("isConnected", false)
			case "reparented":
				node.Set("inRoot", false)
			case "offscreen":
				node.Set("getBoundingClientRect", f.function(func(js.Value, []js.Value) any {
					return map[string]any{"top": 900, "bottom": 930, "left": 1, "right": 30}
				}))
			case "epoch":
				cacheEpoch++
			}
			calls := 0
			mockBrowserFetch(t, func([]js.Value) any {
				calls++
				return js.Global().Get("Promise").Call("reject", js.Global().Get("Error").New("unexpected obsolete dispatch"))
			})
			viewportAuditWorker(t, f.c)
			viewportWait(t, func() bool { return len(f.c.queue) == 0 })
			// Give any wrongly dispatched native Fetch Promise its settlement turn.
			time.Sleep(5 * time.Millisecond)
			if calls != 0 || len(prefetches) != 0 || f.c.flights[0].pending != nil || f.c.attempted(candidate) {
				t.Fatal("obsolete queued candidate started/spent speculative work", kind, calls, len(prefetches))
			}
			if kind == "different-page" {
				// A skipped old URL must not consume the node's new URL attempt.
				f.c.enqueue(node)
				viewportWait(t, func() bool { return calls == 1 })
				viewportWait(t, func() bool { return len(prefetches) == 0 && f.c.flights[0].pending == nil })
			}
		})
	}
}

func TestViewportAuditCanceledCompletionCannotDeleteReplacement(t *testing.T) {
	f := viewportTestFixture(t)
	doc.Set("body", map[string]any{"getAttribute": f.function(func(js.Value, []js.Value) any { return nil })})
	var reject, signal js.Value
	started := false
	executor := js.FuncOf(func(_ js.Value, args []js.Value) any { reject = args[1]; return nil })
	defer executor.Release()
	mockBrowserFetch(t, func(args []js.Value) any {
		signal = args[1].Get("signal")
		started = true
		return js.Global().Get("Promise").New(executor)
	})
	console := js.Global().Get("console")
	originalError := console.Get("error")
	lateCallbackError := false
	logError := js.FuncOf(func(_ js.Value, args []js.Value) any {
		for _, arg := range args {
			if strings.Contains(arg.String(), "released function") {
				lateCallbackError = true
			}
		}
		return nil
	})
	console.Set("error", logError)
	defer func() { console.Set("error", originalError); logError.Release() }()
	key := "https://example.test/viewport-audit/pending-replacement"
	old := prefetchWithOrigin(key, true)
	if old == nil {
		t.Fatal("viewport flight was not admitted")
	}
	viewportWait(t, func() bool { return started })
	replacementCanceled := false
	replacement := &prefetchRequest{done: make(chan struct{}), cancel: func() { replacementCanceled = true }}
	delete(prefetches, key)
	prefetches[key] = replacement
	old.cancel()
	select {
	case <-old.done:
	case <-time.After(time.Second):
		t.Fatal("canceled viewport flight did not settle")
	}
	if prefetches[key] != replacement || replacementCanceled || !signal.Get("aborted").Bool() {
		t.Fatal("old viewport completion affected replacement", prefetches[key], replacementCanceled, signal.Get("aborted"))
	}
	select {
	case <-replacement.done:
		t.Fatal("old completion closed replacement flight")
	default:
	}
	// An aborted Fetch may reject after Go has returned. Its bridge callbacks
	// must remain valid without touching the replacement request's admission.
	reject.Invoke(js.Global().Get("Error").New("late viewport abort"))
	time.Sleep(time.Millisecond)
	if lateCallbackError || prefetches[key] != replacement {
		t.Fatal("late browser rejection affected replacement or released callback")
	}
}
