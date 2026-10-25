//go:build js && wasm

package mutation

import (
	"context"
	"encoding/json"
	"fmt"
	"syscall/js"

	"github.com/Suhaibinator/Next.go/client"
	"github.com/Suhaibinator/Next.go/internal/actionwire"
)

// RefreshResponse separates a confirmed mutation from its fresh view. A nil
// top-level error confirms Response even if View.Err is nonnil. Never retry a
// confirmed mutation to repair its view; refresh/reconcile the view separately.
type RefreshResponse[O any] struct {
	Response[O]
	View client.ActionViewOutcome
}

// CallAndRefresh negotiates one POST containing typed confirmation followed by
// the server's fresh page. It does not issue another mutation on view failure.
func (c Contract[I, O]) CallAndRefresh(ctx context.Context, input I, options ...CallOptions) (result RefreshResponse[O], err error) {
	defer func() {
		if failure := recover(); failure != nil {
			err = fmt.Errorf("nextgo: mutation setup failed: %v", failure)
		}
	}()
	if err = c.validate(); err != nil {
		return
	}
	if ctx == nil || len(options) > 1 {
		return result, fmt.Errorf("nextgo: mutation requires a context and at most one options value")
	}
	if err = ctx.Err(); err != nil {
		return
	}
	opts, err := callOptions(options)
	if err != nil {
		return result, err
	}
	body, err := json.Marshal(input)
	if err != nil {
		return result, fmt.Errorf("nextgo: encode mutation input: %w", err)
	}
	if len(body) > opts.MaxInputBytes || uniqueJSON(body) != nil {
		return result, fmt.Errorf("nextgo: invalid or oversized mutation input")
	}
	return c.refreshTransport(ctx, client.FetchOptions{Body: body, Headers: map[string]string{"Content-Type": "application/json"}}, opts)
}

// CallMultipartAndRefresh uses the same explicit form mapper and confirmation
// rules as CallAndRefresh; the browser owns multipart boundary generation.
func (c Contract[I, O]) CallMultipartAndRefresh(ctx context.Context, form *client.Multipart, options ...CallOptions) (result RefreshResponse[O], err error) {
	defer func() {
		if failure := recover(); failure != nil {
			err = fmt.Errorf("nextgo: mutation setup failed: %v", failure)
		}
	}()
	if err = c.validate(); err != nil {
		return
	}
	if ctx == nil || len(options) > 1 {
		return result, fmt.Errorf("nextgo: mutation requires a context and at most one options value")
	}
	if err = ctx.Err(); err != nil {
		return
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
	return c.refreshTransport(ctx, client.FetchOptions{Multipart: form}, opts)
}

func (c Contract[I, O]) refreshTransport(ctx context.Context, request client.FetchOptions, opts CallOptions) (result RefreshResponse[O], err error) {
	confirmed := false
	defer func() {
		if failure := recover(); failure != nil {
			err = fmt.Errorf("nextgo: action/view setup failed: %v", failure)
		}
		if confirmed && err != nil {
			result.View.Err = err
			err = nil
		}
	}()
	target, err := client.URL(c.Path())
	if err != nil {
		return result, err
	}
	u := js.Global().Get("URL").New(target, js.Global().Get("location").Get("href"))
	expected := u.Get("href").String()
	if scheme := u.Get("protocol").String(); scheme != "http:" && scheme != "https:" {
		return result, fmt.Errorf("nextgo: mutations require an HTTP(S) origin")
	}
	if request.Headers == nil {
		request.Headers = map[string]string{}
	}
	request.Headers[actionwire.ProtocolHeader] = actionwire.Protocol
	request.Headers[actionwire.RevisionHeader] = c.revision
	result.View, err = client.ActionView(ctx, expected, request, func(response client.Response) error {
		if int64(len(response.Body)) > opts.MaxResponseBytes {
			return client.ErrResponseTooLarge
		}
		value, decodeErr := decode[O](response.StatusCode, response.Header.Get(actionwire.ProtocolHeader), response.Header.Get(actionwire.RevisionHeader), c.revision, response.Body)
		if decodeErr != nil {
			return decodeErr
		}
		result.Response = value
		confirmed = true
		return nil
	})
	return result, err
}
