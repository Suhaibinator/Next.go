//go:build !js

package mutation

import (
	"encoding/json"
	"mime"
	"net/url"
	"strings"

	"github.com/Suhaibinator/Next.go/actions"
)

type Result[O any] struct {
	Data                            O
	Redirect                        string
	RevalidateTags, RevalidatePaths []string
}

type BindOptions[I any] struct {
	// Authorize can reject a request before decoding input. Handlers remain
	// responsible for data-level authorization and transaction validation.
	Authorize func(*actions.Context) error
	// Form explicitly maps progressive form values to the shared input type.
	// Without it the binding accepts JSON requests only.
	Form func(*actions.Context, url.Values) (I, error)
}

// Bind registers the versioned endpoint. Invalid/duplicate bindings panic, like
// actions.Router.Handle. Input is decoded before invoking the typed handler.
func (c Contract[I, O]) Bind(router *actions.Router, handler func(*actions.Context, I) (Result[O], error), options ...BindOptions[I]) {
	if c.validate() != nil || router == nil || handler == nil || len(options) > 1 {
		panic("mutation: invalid typed action binding")
	}
	var opts BindOptions[I]
	if len(options) == 1 {
		opts = options[0]
	}
	router.HandleContract(c.Key(), c.revision, func(ctx *actions.Context) (actions.Result, error) {
		if err := ctx.Err(); err != nil {
			return actions.Result{}, err
		}
		if opts.Authorize != nil {
			if err := opts.Authorize(ctx); err != nil {
				return actions.Result{}, err
			}
		}
		var input I
		media, _, _ := mime.ParseMediaType(ctx.Request.Header.Get("Content-Type"))
		if opts.Form == nil || media == "application/json" || strings.HasSuffix(media, "+json") {
			if err := ctx.DecodeJSON(&input); err != nil {
				return actions.Result{}, err
			}
		} else {
			values, err := ctx.ParseForm()
			if err != nil {
				return actions.Result{}, err
			}
			input, err = opts.Form(ctx, values)
			if err != nil {
				return actions.Result{}, err
			}
		}
		if err := ctx.Err(); err != nil {
			return actions.Result{}, err
		}
		result, err := handler(ctx, input)
		if err != nil {
			return actions.Result{}, err
		}
		// Force a present data field even for nil/interface outputs. The router
		// still stages serialization and revalidation before writing success.
		data, err := json.Marshal(result.Data)
		if err != nil {
			return actions.Result{}, err
		}
		return actions.Result{Data: json.RawMessage(data), Redirect: result.Redirect, RevalidateTags: result.RevalidateTags, RevalidatePaths: result.RevalidatePaths}, nil
	})
}
