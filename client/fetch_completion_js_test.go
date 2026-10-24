//go:build js && wasm

package client

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"syscall/js"
	"testing"
	"time"
)

func TestFetchCompletionDoesNotAbortConsumedResponse(t *testing.T) {
	for _, status := range []int{200, 400, 204} {
		for _, streaming := range []bool{false, true} {
			t.Run(fmt.Sprintf("status%d/stream%t", status, streaming), func(t *testing.T) {
				var signal, body js.Value
				mockBrowserFetch(t, func(args []js.Value) any {
					signal = args[1].Get("signal")
					var value any = "complete response"
					if status == 204 {
						value = nil
					}
					response := js.Global().Get("Response").New(value, map[string]any{"status": status})
					body = response.Get("body")
					return js.Global().Get("Promise").Call("resolve", response)
				})
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var response Response
				var err error
				var consumed []byte
				if streaming {
					response, err = FetchStream(ctx, "/complete", FetchOptions{}, nil, func(chunk []byte) error { consumed = append(consumed, chunk...); return nil })
				} else {
					response, err = Fetch(ctx, "/complete", FetchOptions{})
					consumed = response.Body
				}
				if err != nil || response.StatusCode != status || status != 204 && string(consumed) != "complete response" || status == 204 && len(consumed) != 0 {
					t.Fatal(response, string(consumed), err)
				}
				if !body.IsNull() && body.Get("locked").Bool() {
					t.Fatal("completed reader retained its lock")
				}
				if signal.Get("aborted").Bool() {
					t.Fatal("fully consumed response was aborted during cleanup")
				}
				cancel()
				if signal.Get("aborted").Bool() {
					t.Fatal("completed request retained cancellation work")
				}
			})
		}
	}
}

func TestFetchCompletionIncompleteExitsStillAbort(t *testing.T) {
	for _, kind := range []string{"network", "header-error", "header-panic", "content-length", "decoded-limit", "chunk-error", "chunk-panic"} {
		t.Run(kind, func(t *testing.T) {
			var signal, body js.Value
			mockBrowserFetch(t, func(args []js.Value) any {
				signal = args[1].Get("signal")
				if kind == "network" {
					return js.Global().Get("Promise").Call("reject", js.Global().Get("Error").New("network disconnected"))
				}
				headers := map[string]any{}
				if kind == "content-length" {
					headers["Content-Length"] = "4096"
				}
				response := js.Global().Get("Response").New(strings.Repeat("x", 4096), map[string]any{"headers": headers})
				body = response.Get("body")
				return js.Global().Get("Promise").Call("resolve", response)
			})
			failure := errors.New("consumer failure")
			options := FetchOptions{}
			if kind == "content-length" || kind == "decoded-limit" {
				options.MaxBytes = 1024
			}
			_, err := FetchStream(context.Background(), "/incomplete", options, func(Response) error {
				if kind == "header-error" {
					return failure
				}
				if kind == "header-panic" {
					panic("header panic")
				}
				return nil
			}, func([]byte) error {
				if kind == "chunk-error" {
					return failure
				}
				if kind == "chunk-panic" {
					panic("chunk panic")
				}
				return nil
			})
			if err == nil || !signal.Get("aborted").Bool() {
				t.Fatal("incomplete response was not aborted", kind, err, signal.Get("aborted"))
			}
			if (kind == "header-error" || kind == "chunk-error") && !errors.Is(err, failure) {
				t.Fatal("consumer error changed", err)
			}
			if (kind == "content-length" || kind == "decoded-limit") && !errors.Is(err, ErrResponseTooLarge) {
				t.Fatal("byte limit error changed", err)
			}
			until := time.Now().Add(time.Second)
			for body.Type() == js.TypeObject && !body.IsNull() && body.Get("locked").Bool() && time.Now().Before(until) {
				time.Sleep(time.Millisecond)
			}
			if body.Type() == js.TypeObject && !body.IsNull() && body.Get("locked").Bool() {
				t.Fatal("failed reader cleanup retained its lock")
			}
		})
	}
}
