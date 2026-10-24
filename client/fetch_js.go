//go:build js && wasm

package client

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"syscall/js"
)

// FetchOptions controls a browser HTTP request. Fetch uses same-origin
// credentials, follows browser redirects, and observes browser CORS rules.
// A zero MaxBytes limits responses to 8 MiB. Body is sent unchanged.
type FetchOptions struct {
	Method  string
	Headers map[string]string
	Body    []byte
	// Multipart supplies native FormData, mutually exclusive with Body and a
	// caller-supplied Content-Type (the browser supplies its boundary).
	Multipart *Multipart
	MaxBytes  int64
	// Cache accepts browser Fetch cache modes. Empty uses the browser default.
	// only-if-cached also sets same-origin mode, as required by browsers.
	Cache string
	// Redirect accepts follow (default), error or manual. Mutations can use
	// error to prevent a redirect from forwarding their body elsewhere.
	Redirect string
}

// Headers contains case-insensitive response headers visible to the browser.
// Browsers deliberately hide some headers, including Set-Cookie.
type Headers map[string][]string

func (h Headers) Get(name string) string {
	values := h.Values(name)
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func (h Headers) Values(name string) []string {
	for key, values := range h {
		if strings.EqualFold(key, name) {
			return values
		}
	}
	return nil
}

// Response is a complete, size-bounded browser response. HTTP error statuses
// are returned normally; callers inspect StatusCode. Transport errors return
// an error instead. No net/http dependency is needed in browser bundles.
type Response struct {
	// URL is the browser-reported final response URL after redirects.
	URL        string
	StatusCode int
	Header     Headers
	Body       []byte
}

var ErrResponseTooLarge = errors.New("nextgo: response exceeds byte limit")

type promiseResult struct {
	value js.Value
	err   error
}

// awaitBrowserPromise retains both callbacks until the Promise settles, even
// when context cancellation lets the caller return first. Releasing them at
// cancellation would let the browser call released Go functions later.
func awaitBrowserPromise(ctx context.Context, promise, controller js.Value) (js.Value, error) {
	settled := make(chan promiseResult, 1)
	resolve := js.FuncOf(func(_ js.Value, args []js.Value) any {
		settled <- promiseResult{value: args[0]}
		return nil
	})
	reject := js.FuncOf(func(_ js.Value, args []js.Value) any {
		message := "browser request failed"
		if len(args) != 0 {
			reason := args[0]
			if reason.Type() == js.TypeString {
				message = reason.String()
			} else if reason.Type() == js.TypeObject && !reason.IsNull() {
				if detail := reason.Get("message"); detail.Type() == js.TypeString {
					message = detail.String()
				}
			}
		}
		settled <- promiseResult{err: errors.New(message)}
		return nil
	})
	promise.Call("then", resolve, reject)
	release := func() { resolve.Release(); reject.Release() }
	select {
	case result := <-settled:
		release()
		if err := ctx.Err(); err != nil {
			return js.Undefined(), err
		}
		return result.value, result.err
	case <-ctx.Done():
		controller.Call("abort")
		go func() { <-settled; release() }()
		return js.Undefined(), ctx.Err()
	}
}

// Fetch performs a browser HTTP request without importing net/http. Call it
// from a goroutine: blocking a synchronous JavaScript event callback prevents
// browser Promises from completing. Context cancellation aborts the request;
// callers can use context.WithTimeout for a deadline.
func Fetch(ctx context.Context, target string, options FetchOptions) (Response, error) {
	return fetchBrowser(ctx, target, options, nil, nil)
}

// FetchStream reads a bounded response incrementally, with the same origin,
// redirect and cancellation policy as Fetch. Callbacks run in the caller's Go
// goroutine, provide backpressure, and must not block a JavaScript event callback.
// Each chunk is an owned byte slice; the returned Response has an empty Body.
func FetchStream(ctx context.Context, target string, options FetchOptions, headers func(Response) error, chunk func([]byte) error) (Response, error) {
	if chunk == nil {
		return Response{}, fmt.Errorf("nextgo: FetchStream requires a chunk callback")
	}
	return fetchBrowser(ctx, target, options, headers, chunk)
}
func fetchBrowser(ctx context.Context, target string, options FetchOptions, onHeaders func(Response) error, onChunk func([]byte) error) (response Response, err error) {
	defer func() {
		if failure := recover(); failure != nil {
			err = fmt.Errorf("nextgo: browser fetch: %v", failure)
		}
	}()
	if ctx == nil {
		return response, fmt.Errorf("nextgo: Fetch requires a context")
	}
	if err := ctx.Err(); err != nil {
		return response, err
	}
	limit := options.MaxBytes
	if limit == 0 {
		limit = 8 << 20
	}
	if limit < 0 {
		return response, errors.New("nextgo: MaxBytes must be positive")
	}
	switch options.Cache {
	case "", "default", "no-store", "reload", "no-cache", "force-cache", "only-if-cached":
	default:
		return response, fmt.Errorf("nextgo: invalid Fetch cache mode %q", options.Cache)
	}
	switch options.Redirect {
	case "", "follow", "error", "manual":
	default:
		return response, fmt.Errorf("nextgo: invalid Fetch redirect mode %q", options.Redirect)
	}
	method := options.Method
	if method == "" {
		method = "GET"
	}
	if options.Multipart != nil {
		if options.Body != nil || strings.EqualFold(method, "GET") || strings.EqualFold(method, "HEAD") {
			return response, fmt.Errorf("nextgo: multipart requires a body-capable method and no byte body")
		}
		for name := range options.Headers {
			if strings.EqualFold(name, "Content-Type") {
				return response, fmt.Errorf("nextgo: multipart Content-Type belongs to the browser")
			}
		}
	}
	controller := js.Global().Get("AbortController").New()
	complete := false
	// Abort incomplete/error exits, including synchronous browser API panics.
	// Aborting after EOF can turn a consumed response into ERR_ABORTED in
	// Chromium instead of allowing its normal request-finished notification.
	defer func() {
		if !complete {
			controller.Call("abort")
		}
	}()
	headers := js.Global().Get("Headers").New()
	for name, value := range options.Headers {
		headers.Call("set", name, value)
	}
	init := map[string]any{"method": method, "headers": headers, "credentials": "same-origin", "signal": controller.Get("signal")}
	if options.Cache != "" {
		init["cache"] = options.Cache
	}
	if options.Redirect != "" {
		init["redirect"] = options.Redirect
	}
	if options.Cache == "only-if-cached" {
		init["mode"] = "same-origin"
	}
	if options.Body != nil {
		body := js.Global().Get("Uint8Array").New(len(options.Body))
		js.CopyBytesToJS(body, options.Body)
		init["body"] = body
	}
	if options.Multipart != nil {
		form, err := options.Multipart.formData()
		if err != nil {
			return response, err
		}
		init["body"] = form
	}
	if frameworkActionRequest(target, method) {
		// Discard reuse on dispatch and settlement, including failures: an
		// aborted/failed transport does not prove a mutation was rolled back.
		invalidateCache()
		defer invalidateCache()
	}
	result, err := awaitBrowserPromise(ctx, js.Global().Call("fetch", target, init), controller)
	if err != nil {
		return response, err
	}
	response.URL = result.Get("url").String()
	response.StatusCode = result.Get("status").Int()
	response.Header = Headers{}
	iterator := result.Get("headers").Call("entries")
	for item := iterator.Call("next"); !item.Get("done").Bool(); item = iterator.Call("next") {
		pair := item.Get("value")
		name := pair.Index(0).String()
		response.Header[name] = append(response.Header[name], pair.Index(1).String())
	}
	// Content-Length may be the compressed size, so this is only an early
	// rejection. The streaming loop also checks decoded bytes before copying.
	if length, parseErr := strconv.ParseInt(response.Header.Get("Content-Length"), 10, 64); parseErr == nil && response.Header.Get("Content-Encoding") == "" && length > limit {
		return response, ErrResponseTooLarge
	}
	if onHeaders != nil {
		if err := onHeaders(response); err != nil {
			return response, err
		}
	}
	body := result.Get("body")
	if body.IsNull() || body.IsUndefined() {
		complete = true
		return response, nil
	}
	reader := body.Call("getReader")
	defer func() {
		defer func() { _ = recover() }()
		if !complete {
			// Stop reading immediately, and retain callbacks until cancellation
			// settles. Cleanup must never delay return of the caller's ctx.Err.
			canceled := reader.Call("cancel")
			go func() {
				defer func() { _ = recover() }()
				_, _ = awaitBrowserPromise(context.Background(), canceled, controller)
				reader.Call("releaseLock")
			}()
			return
		}
		reader.Call("releaseLock")
	}()
	var received int64
	for {
		chunk, readErr := awaitBrowserPromise(ctx, reader.Call("read"), controller)
		if readErr != nil {
			return response, readErr
		}
		if chunk.Get("done").Bool() {
			complete = true
			return response, nil
		}
		value := chunk.Get("value")
		length := value.Get("byteLength").Int()
		if int64(length) > limit-received {
			return response, ErrResponseTooLarge
		}
		received += int64(length)
		if onChunk != nil {
			data := make([]byte, length)
			js.CopyBytesToGo(data, value)
			if err := onChunk(data); err != nil {
				return response, err
			}
		} else {
			start := len(response.Body)
			response.Body = append(response.Body, make([]byte, length)...)
			js.CopyBytesToGo(response.Body[start:], value)
		}
	}
}

func frameworkActionRequest(target, method string) bool {
	if !strings.EqualFold(method, "POST") || doc.IsUndefined() || win.IsUndefined() {
		return false
	}
	u := win.Get("URL").New(target, doc.Get("baseURI"))
	if u.Get("origin").String() != win.Get("location").Get("origin").String() || u.Get("username").String() != "" || u.Get("password").String() != "" {
		return false
	}
	local, inside := deployment.Strip(u.Get("pathname").String())
	return inside && strings.HasPrefix(local, "/_nextgo/actions/") && len(local) > len("/_nextgo/actions/")
}
