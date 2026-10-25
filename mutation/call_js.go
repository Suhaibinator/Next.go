//go:build js && wasm

package mutation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"syscall/js"

	"github.com/Suhaibinator/Next.go/client"
	"github.com/Suhaibinator/Next.go/internal/actionwire"
)

type CallOptions struct {
	MaxInputBytes    int   // zero selects 64 KiB
	MaxResponseBytes int64 // zero selects 64 KiB
}

// Call performs a typed POST without navigating. Use a goroutine outside the
// synchronous DOM event callback. Cancellation aborts transport; an error does
// not establish whether a server mutation committed. Unknown outcomes require
// application reconciliation or an idempotency protocol before retrying.
func (c Contract[I, O]) Call(ctx context.Context, input I, options ...CallOptions) (result Response[O], callErr error) {
	defer func() {
		if failure := recover(); failure != nil {
			var zero Response[O]
			result = zero
			callErr = fmt.Errorf("nextgo: mutation setup failed: %v", failure)
		}
	}()
	var zero Response[O]
	if err := c.validate(); err != nil {
		return zero, err
	}
	if ctx == nil || len(options) > 1 {
		return zero, fmt.Errorf("nextgo: mutation requires a context and at most one options value")
	}
	if err := ctx.Err(); err != nil {
		return zero, context.Cause(ctx)
	}
	opts, err := callOptions(options)
	if err != nil {
		return zero, err
	}
	body, err := json.Marshal(input)
	if err != nil {
		return zero, fmt.Errorf("nextgo: encode mutation input: %w", err)
	}
	if len(body) > opts.MaxInputBytes {
		return zero, fmt.Errorf("nextgo: mutation input exceeds byte limit")
	}
	if err := uniqueJSON(body); err != nil {
		return zero, fmt.Errorf("nextgo: invalid mutation input JSON")
	}
	return c.callTransport(ctx, client.FetchOptions{Body: body, Headers: map[string]string{"Content-Type": "application/json"}}, opts)
}

// CallMultipart sends explicit form fields/files to the contract's native Form
// mapper and decodes its typed output. Input field mapping is explicit, not an
// automatic reflection/schema conversion of I. It shares Call's origin, marker,
// response, cancellation and redirect policy. The budget is an accounting bound;
// server request limits remain authoritative for actual multipart framing.
func (c Contract[I, O]) CallMultipart(ctx context.Context, form *client.Multipart, options ...CallOptions) (result Response[O], callErr error) {
	defer func() {
		if failure := recover(); failure != nil {
			result = Response[O]{}
			callErr = fmt.Errorf("nextgo: mutation setup failed: %v", failure)
		}
	}()
	if err := c.validate(); err != nil {
		return result, err
	}
	if ctx == nil || len(options) > 1 {
		return result, fmt.Errorf("nextgo: mutation requires a context and at most one options value")
	}
	if err := ctx.Err(); err != nil {
		return result, context.Cause(ctx)
	}
	opts, err := callOptions(options)
	if err != nil {
		return result, err
	}
	budget, err := form.BudgetBytes()
	if err != nil {
		return result, err
	}
	if budget > int64(opts.MaxInputBytes) {
		return result, fmt.Errorf("nextgo: multipart input exceeds byte budget")
	}
	return c.callTransport(ctx, client.FetchOptions{Multipart: form}, opts)
}
func callOptions(options []CallOptions) (CallOptions, error) {
	var opts CallOptions
	if len(options) > 0 {
		opts = options[0]
	}
	if opts.MaxInputBytes == 0 {
		opts.MaxInputBytes = 64 << 10
	}
	if opts.MaxResponseBytes == 0 {
		opts.MaxResponseBytes = 64 << 10
	}
	if opts.MaxInputBytes < 0 || opts.MaxResponseBytes < 0 {
		return opts, fmt.Errorf("nextgo: mutation byte limits must be positive")
	}
	return opts, nil
}
func (c Contract[I, O]) callTransport(ctx context.Context, request client.FetchOptions, opts CallOptions) (Response[O], error) {
	var decoded Response[O]
	err := dispatchTransport(ctx, c.Path(), c.revision, request, opts, func(response client.Response) error {
		var err error
		decoded, err = decode[O](response.StatusCode, response.Header.Get(actionwire.ProtocolHeader), response.Header.Get(actionwire.RevisionHeader), c.revision, response.Body)
		return err
	})
	if err != nil {
		return Response[O]{}, err
	}
	return decoded, nil
}

func dispatchTransport(ctx context.Context, path, revision string, request client.FetchOptions, opts CallOptions, decode func(client.Response) error) error {
	target, err := client.URL(path)
	if err != nil {
		return err
	}
	global := js.Global()
	u := global.Get("URL").New(target, global.Get("location").Get("href"))
	if scheme := u.Get("protocol").String(); scheme != "http:" && scheme != "https:" {
		return fmt.Errorf("nextgo: mutations require an HTTP(S) origin")
	}
	expected := u.Get("href").String()
	request.Method = "POST"
	request.MaxBytes = opts.MaxResponseBytes
	request.Cache = "no-store"
	request.Redirect = "error"
	if request.Headers == nil {
		request.Headers = map[string]string{}
	}
	request.Headers["Accept"] = "application/json"
	request.Headers[actionwire.ProtocolHeader] = actionwire.Protocol
	request.Headers[actionwire.RevisionHeader] = revision
	var validationErr error
	err = client.DispatchMutation(ctx, expected, request, func(response client.Response) error {
		if response.URL != expected {
			validationErr = fmt.Errorf("%w: endpoint changed", ErrProtocol)
		} else if !jsonMediaType(response.Header.Get("Content-Type")) {
			validationErr = fmt.Errorf("%w: expected JSON", ErrProtocol)
		} else {
			validationErr = decode(response)
		}
		return validationErr
	})
	if validationErr != nil {
		return validationErr
	}
	if err != nil {
		if errors.Is(err, client.ErrMutationQueueFull) || errors.Is(err, client.ErrMutationQueueFrozen) {
			return err
		}
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		return fmt.Errorf("nextgo: mutation transport failed; reconcile before retrying: %w", err)
	}
	return nil
}
