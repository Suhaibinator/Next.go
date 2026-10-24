//go:build js && wasm

package client

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"syscall/js"
	"testing"
	"time"
)

func mockBrowserFetch(t *testing.T, request func([]js.Value) any) {
	t.Helper()
	original := js.Global().Get("fetch")
	mock := js.FuncOf(func(_ js.Value, args []js.Value) any { return request(args) })
	js.Global().Set("fetch", mock)
	t.Cleanup(func() { js.Global().Set("fetch", original); mock.Release() })
}

func browserResponse(body []byte, status int, headers map[string]any) js.Value {
	data := js.Global().Get("Uint8Array").New(len(body))
	js.CopyBytesToJS(data, body)
	response := js.Global().Get("Response").New(data, map[string]any{"status": status, "headers": headers})
	return js.Global().Get("Promise").Call("resolve", response)
}

func TestFetchBinaryBodyHeadersAndHTTPError(t *testing.T) {
	want := []byte{0, 1, 128, 255}
	var method, credentials, header, cache, redirect string
	var sent []byte
	mockBrowserFetch(t, func(args []js.Value) any {
		options := args[1]
		method = options.Get("method").String()
		credentials = options.Get("credentials").String()
		header = options.Get("headers").Call("get", "X-Input").String()
		cache = options.Get("cache").String()
		redirect = options.Get("redirect").String()
		sent = make([]byte, options.Get("body").Get("byteLength").Int())
		js.CopyBytesToGo(sent, options.Get("body"))
		return browserResponse(want, 422, map[string]any{"X-Example": "hello"})
	})
	response, err := Fetch(context.Background(), "/test", FetchOptions{Method: "POST", Headers: map[string]string{"X-Input": "value"}, Body: want, Cache: "no-store", Redirect: "error"})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != 422 || !bytes.Equal(response.Body, want) || response.Header.Get("X-EXAMPLE") != "hello" {
		t.Fatalf("response = %+v", response)
	}
	if method != "POST" || credentials != "same-origin" || header != "value" || cache != "no-store" || redirect != "error" || !bytes.Equal(sent, want) {
		t.Fatalf("request: %q %q %q %q %v", method, credentials, header, cache, sent)
	}
}

func TestFetchBoundsDecodedStream(t *testing.T) {
	for _, contentLength := range []string{"", "65536"} {
		t.Run("length="+contentLength, func(t *testing.T) {
			mockBrowserFetch(t, func([]js.Value) any {
				headers := map[string]any{}
				if contentLength != "" {
					headers["Content-Length"] = contentLength
				}
				return browserResponse([]byte(strings.Repeat("x", 65536)), 200, headers)
			})
			response, err := Fetch(context.Background(), "/large", FetchOptions{MaxBytes: 1024})
			if !errors.Is(err, ErrResponseTooLarge) || len(response.Body) > 1024 {
				t.Fatalf("body length=%d error=%v", len(response.Body), err)
			}
		})
	}
}

func TestFetchNetworkFailureAndInvalidOptions(t *testing.T) {
	mockBrowserFetch(t, func([]js.Value) any {
		return js.Global().Get("Promise").Call("reject", js.Global().Get("Error").New("network disconnected"))
	})
	if _, err := Fetch(context.Background(), "/error", FetchOptions{}); err == nil || !strings.Contains(err.Error(), "network disconnected") {
		t.Fatalf("network error = %v", err)
	}
	if _, err := Fetch(context.Background(), "/error", FetchOptions{MaxBytes: -1}); err == nil {
		t.Fatal("negative MaxBytes accepted")
	}
	if _, err := Fetch(context.Background(), "/error", FetchOptions{Cache: "typo"}); err == nil {
		t.Fatal("invalid Cache mode accepted")
	}
	if _, err := Fetch(context.Background(), "/error", FetchOptions{Redirect: "typo"}); err == nil {
		t.Fatal("invalid Redirect mode accepted")
	}
}

func TestFetchDeadlineAbortsAndRetainsCallbacksUntilRejection(t *testing.T) {
	var signal js.Value
	var reject js.Value
	console := js.Global().Get("console")
	originalError := console.Get("error")
	logged := false
	logError := js.FuncOf(func(_ js.Value, _ []js.Value) any { logged = true; return nil })
	console.Set("error", logError)
	defer func() { console.Set("error", originalError); logError.Release() }()
	executor := js.FuncOf(func(_ js.Value, args []js.Value) any { reject = args[1]; return nil })
	defer executor.Release()
	mockBrowserFetch(t, func(args []js.Value) any {
		signal = args[1].Get("signal")
		return js.Global().Get("Promise").New(executor)
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	_, err := Fetch(ctx, "/slow", FetchOptions{})
	if !errors.Is(err, context.DeadlineExceeded) || !signal.Get("aborted").Bool() {
		t.Fatalf("deadline: error=%v aborted=%v", err, signal.Get("aborted"))
	}
	// A browser may settle its aborted Promise after Fetch returns. This must
	// call a live callback instead of logging "call to released function".
	reject.Invoke(js.Global().Get("Error").New("late abort rejection"))
	time.Sleep(time.Millisecond)
	if logged {
		t.Fatal("late Promise rejection called a released callback")
	}
}

func TestFetchAlreadyCanceledDoesNotStartRequest(t *testing.T) {
	called := false
	mockBrowserFetch(t, func([]js.Value) any { called = true; return js.Undefined() })
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Fetch(ctx, "/canceled", FetchOptions{})
	if !errors.Is(err, context.Canceled) || called {
		t.Fatalf("error=%v request started=%v", err, called)
	}
}

func TestFetchCancellationDuringBodyReadCancelsStream(t *testing.T) {
	canceled := make(chan struct{}, 1)
	reading := make(chan struct{}, 1)
	pull := js.FuncOf(func(js.Value, []js.Value) any {
		select {
		case reading <- struct{}{}:
		default:
		}
		return nil
	})
	defer pull.Release()
	cancelStream := js.FuncOf(func(_ js.Value, _ []js.Value) any {
		canceled <- struct{}{}
		return nil
	})
	defer cancelStream.Release()
	stream := js.Global().Get("ReadableStream").New(map[string]any{"cancel": cancelStream, "pull": pull}, map[string]any{"highWaterMark": 0})
	mockBrowserFetch(t, func([]js.Value) any {
		return js.Global().Get("Promise").Call("resolve", js.Global().Get("Response").New(stream))
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := Fetch(ctx, "/stream", FetchOptions{}); done <- err }()
	// A zero high-water mark makes pull observable only after reader.read;
	// synchronize with that operation instead of guessing with a timer.
	<-reading
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("body read cancellation = %v", err)
	}
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("body stream was not canceled")
	}
	time.Sleep(time.Millisecond)
}

func TestFetchCompressedLengthDoesNotReplaceDecodedLimit(t *testing.T) {
	mockBrowserFetch(t, func([]js.Value) any {
		return browserResponse([]byte("decoded"), 200, map[string]any{"Content-Length": "100", "Content-Encoding": "gzip"})
	})
	response, err := Fetch(context.Background(), "/encoded", FetchOptions{MaxBytes: 10})
	if err != nil || string(response.Body) != "decoded" {
		t.Fatalf("response=%q error=%v", response.Body, err)
	}
}

func TestFetchReportsFinalURL(t *testing.T) {
	mockBrowserFetch(t, func([]js.Value) any {
		response := js.Global().Get("Response").New("ok")
		js.Global().Get("Object").Call("defineProperty", response, "url", map[string]any{"value": "https://other.example/final"})
		return js.Global().Get("Promise").Call("resolve", response)
	})
	response, err := Fetch(context.Background(), "/redirect", FetchOptions{})
	if err != nil || response.URL != "https://other.example/final" {
		t.Fatalf("response=%+v err=%v", response, err)
	}
}

func TestNavigationEnvelopeBudgetIncludesBodyAndStructuredHead(t *testing.T) {
	value := envelope{HTML: strings.Repeat("x", 3<<20), Meta: map[string]string{"title": "metadata"}, Head: []headElement{{Tag: "script", Attrs: map[string]string{"type": "application/ld+json"}, Text: `{"name":"structured"}`}}}
	if envelopeBytes(value) <= int64(len(value.HTML)+len(value.Head[0].Text)) {
		t.Fatal("head/overhead was not budgeted")
	}
	cache := navigationCache[envelope]{weigh: envelopeBytes}
	for _, key := range []string{"first", "second", "third"} {
		cache.put(key, value, time.Second)
	}
	if len(cache.entries) != 2 || cache.bytes > navigationCacheBytes {
		t.Fatalf("budget: %d entries, %d bytes", len(cache.entries), cache.bytes)
	}
}

func TestFetchStreamHeadersChunksAndOwnedOutput(t *testing.T) {
	mockBrowserFetch(t, func([]js.Value) any {
		return browserResponse([]byte("incremental content"), 200, map[string]any{"X-Stream": "yes"})
	})
	headersSeen := false
	var captured []byte
	response, err := FetchStream(context.Background(), "/stream", FetchOptions{}, func(response Response) error {
		headersSeen = true
		if response.Header.Get("X-Stream") != "yes" {
			t.Fatal("headers unavailable before body")
		}
		return nil
	}, func(chunk []byte) error {
		if !headersSeen {
			t.Fatal("body preceded headers")
		}
		captured = append(captured, chunk...)
		return nil
	})
	if err != nil || len(response.Body) != 0 || string(captured) != "incremental content" {
		t.Fatal(err, string(captured))
	}
}
func TestFetchStreamStopsAtConsumerFailureAndByteLimit(t *testing.T) {
	mockBrowserFetch(t, func([]js.Value) any { return browserResponse([]byte(strings.Repeat("x", 4096)), 200, nil) })
	failure := errors.New("consumer rejected data")
	if _, err := FetchStream(context.Background(), "/stream", FetchOptions{}, nil, func([]byte) error { return failure }); !errors.Is(err, failure) {
		t.Fatal("consumer error lost", err)
	}
	count := 0
	if _, err := FetchStream(context.Background(), "/stream", FetchOptions{MaxBytes: 1024}, nil, func(data []byte) error { count += len(data); return nil }); !errors.Is(err, ErrResponseTooLarge) || count > 1024 {
		t.Fatal("stream limit bypassed", count, err)
	}
}
