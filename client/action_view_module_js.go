//go:build js && wasm

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"syscall/js"
)

// The module ABI passes DOM-owned bodies directly. It does not copy FormData
// through JSON, install a second router or decode application output in core.
type moduleActionViewRequest struct {
	Method   string
	Headers  map[string]string
	MaxBytes int64
	Cache    string
	Redirect string
}

type moduleActionViewResult struct {
	Applied  bool
	Status   int
	Error    string
	PreError string
}

func actionViewFromHost(ctx context.Context, target string, request FetchOptions, validate func(Response) error) (outcome ActionViewOutcome, err error) {
	return mutationFromHost(ctx, target, request, validate, "actionView")
}

func dispatchMutationFromHost(ctx context.Context, target string, request FetchOptions, validate func(Response) error) error {
	// A confirmed ordinary mutation has no remaining view phase. Any failure
	// after source validation cannot turn that accepted result back into an
	// unknown mutation outcome.
	_, err := mutationFromHost(ctx, target, request, validate, "mutation")
	return err
}

func mutationFromHost(ctx context.Context, target string, request FetchOptions, validate func(Response) error, method string) (outcome ActionViewOutcome, err error) {
	confirmed := false
	defer func() {
		if failure := recover(); failure != nil {
			failureErr := fmt.Errorf("nextgo: action view host unavailable: %v", failure)
			if confirmed {
				outcome.Err, err = failureErr, nil
			} else {
				err = failureErr
			}
		}
		// Even a malformed/failed host return cannot revoke a result already
		// synchronously accepted by the originating typed validator.
		if confirmed && err != nil {
			outcome.Err, err = err, nil
		}
	}()
	if ctx == nil || validate == nil {
		return outcome, fmt.Errorf("nextgo: action view requires a context and validator")
	}
	if err := ctx.Err(); err != nil {
		return outcome, context.Cause(ctx)
	}
	if request.Multipart != nil && request.Body != nil {
		return outcome, fmt.Errorf("nextgo: multipart and byte body are mutually exclusive")
	}
	var body = js.Undefined()
	if request.Multipart != nil {
		for name := range request.Headers {
			if strings.EqualFold(name, "Content-Type") {
				return outcome, fmt.Errorf("nextgo: multipart Content-Type belongs to the browser")
			}
		}
		body, err = request.Multipart.formData()
		if err != nil {
			return outcome, err
		}
	} else if request.Body != nil {
		body = win.Get("Uint8Array").New(len(request.Body))
		js.CopyBytesToJS(body, request.Body)
	}
	metadata, err := json.Marshal(moduleActionViewRequest{Method: request.Method, Headers: request.Headers, MaxBytes: request.MaxBytes, Cache: request.Cache, Redirect: request.Redirect})
	if err != nil || len(metadata) > 64<<10 {
		return outcome, fmt.Errorf("nextgo: invalid module action request metadata")
	}
	var validationErr error
	validated := false
	validator := js.FuncOf(func(_ js.Value, args []js.Value) (result any) {
		defer func() {
			if failure := recover(); failure != nil {
				validationErr = fmt.Errorf("nextgo: action result validator failed: %v", failure)
				result = validationErr.Error()
			}
		}()
		// A queued dispatch probes the source synchronously after acquiring
		// core's lease. The asynchronous AbortSignal watcher may not have run
		// yet, so this check must not depend on it or consume result validation.
		if len(args) == 1 && args[0].IsNull() {
			if ctx.Err() != nil {
				validationErr = context.Cause(ctx)
				return validationErr.Error()
			}
			return ""
		}
		if validated {
			return "nextgo: module action result validator called twice"
		}
		validated = true
		if ctx.Err() != nil {
			validationErr = context.Cause(ctx)
			return validationErr.Error()
		}
		if len(args) != 1 || args[0].Type() != js.TypeString || len(args[0].String()) > 128<<10 {
			validationErr = fmt.Errorf("nextgo: invalid module action result")
			return validationErr.Error()
		}
		var response Response
		if decodeErr := json.Unmarshal([]byte(args[0].String()), &response); decodeErr != nil {
			validationErr = fmt.Errorf("nextgo: invalid module action result")
			return validationErr.Error()
		}
		validationErr = validate(response)
		if validationErr != nil {
			return validationErr.Error()
		}
		if response.StatusCode == 200 {
			confirmed = true
		}
		return ""
	})
	physicalPromise := false
	defer func() {
		if !physicalPromise {
			validator.Release()
		}
	}()
	controller := win.Get("AbortController").New()
	promise := moduleHost.Call(method, target, string(metadata), body, validator, controller.Get("signal"))
	if promise.Type() != js.TypeObject || promise.IsNull() || promise.Get("then").Type() != js.TypeFunction {
		return outcome, fmt.Errorf("nextgo: invalid action view host promise")
	}
	physicalPromise = true
	// Original-scope cancellation is signaled to core, which decides whether it
	// preceded confirmation/commit or was caused by its own successful DOM update.
	// Never return early/release validator merely because the source unmounted.
	settled := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			controller.Call("abort")
		case <-settled:
		}
	}()
	value, promiseErr := awaitBrowserPromise(context.Background(), promise, controller)
	close(settled)
	validator.Release()
	if validationErr != nil {
		return outcome, validationErr
	}
	if promiseErr != nil {
		if confirmed {
			outcome.Err = moduleMutationContextError(ctx, promiseErr)
			return outcome, nil
		}
		if ctx.Err() != nil {
			return outcome, context.Cause(ctx)
		}
		return outcome, promiseErr
	}
	var result moduleActionViewResult
	if value.Type() != js.TypeString || json.Unmarshal([]byte(value.String()), &result) != nil {
		if confirmed {
			outcome.Err = fmt.Errorf("nextgo: invalid action view host result")
			return outcome, nil
		}
		return outcome, fmt.Errorf("nextgo: invalid action view host result")
	}
	outcome.Applied, outcome.Status = result.Applied, result.Status
	if result.Error != "" {
		outcome.Err = moduleMutationContextError(ctx, fmt.Errorf("%s", result.Error))
	}
	if result.PreError != "" {
		if confirmed {
			outcome.Err = moduleMutationContextError(ctx, fmt.Errorf("%s", result.PreError))
		} else {
			return outcome, moduleMutationContextError(ctx, fmt.Errorf("%s", result.PreError))
		}
	}
	if !confirmed {
		return outcome, fmt.Errorf("nextgo: action view host did not confirm a typed result")
	}
	return outcome, nil
}

func moduleMutationContextError(ctx context.Context, err error) error {
	switch err.Error() {
	case ErrMutationQueueFull.Error():
		return ErrMutationQueueFull
	case ErrMutationQueueFrozen.Error():
		return ErrMutationQueueFrozen
	}
	if ctx.Err() != nil && (err.Error() == ctx.Err().Error() || err.Error() == context.Canceled.Error() || err.Error() == context.DeadlineExceeded.Error() || err.Error() == context.Cause(ctx).Error()) {
		return context.Cause(ctx)
	}
	return err
}

// actionViewHostPromise runs only in the core runtime. Promise settlement is
// the physical lifetime boundary for both the source validator and abort hook.
func actionViewHostPromise(args []js.Value) any {
	return mutationHostOperation(args, true)
}

func mutationHostPromise(args []js.Value) any {
	return mutationHostOperation(args, false)
}

func mutationHostOperation(args []js.Value, refresh bool) any {
	var executor js.Func
	executor = js.FuncOf(func(_ js.Value, promises []js.Value) any {
		resolve, reject := promises[0], promises[1]
		if len(args) != 5 || args[0].Type() != js.TypeString || args[1].Type() != js.TypeString || len(args[1].String()) > 64<<10 || args[3].Type() != js.TypeFunction {
			reject.Invoke("nextgo: invalid action view module request")
			return nil
		}
		signal := args[4]
		if signal.Type() != js.TypeObject || signal.IsNull() || signal.Get("addEventListener").Type() != js.TypeFunction || signal.Get("removeEventListener").Type() != js.TypeFunction || signal.Get("aborted").Type() != js.TypeBoolean {
			reject.Invoke("nextgo: invalid action view module request")
			return nil
		}
		var metadata moduleActionViewRequest
		if err := json.Unmarshal([]byte(args[1].String()), &metadata); err != nil {
			reject.Invoke("nextgo: invalid action view module metadata")
			return nil
		}
		ctx, cancel := context.WithCancel(context.Background())
		abort := js.FuncOf(func(js.Value, []js.Value) any { cancel(); return nil })
		signal.Call("addEventListener", "abort", abort)
		if signal.Get("aborted").Bool() {
			cancel()
		}
		go func() {
			defer cancel()
			defer abort.Release()
			defer signal.Call("removeEventListener", "abort", abort)
			defer func() {
				if failure := recover(); failure != nil {
					reject.Invoke(fmt.Sprintf("nextgo: action view module host failed: %v", failure))
				}
			}()
			request := FetchOptions{Method: metadata.Method, Headers: metadata.Headers, MaxBytes: metadata.MaxBytes, Cache: metadata.Cache, Redirect: metadata.Redirect, browserBody: args[2]}
			request.beforeMutationDispatch = func() error {
				result := args[3].Invoke(js.Null())
				if result.Type() != js.TypeString || result.String() != "" {
					return fmt.Errorf("nextgo: module mutation dispatch rejected")
				}
				return nil
			}
			validate := func(response Response) error {
				encoded, err := json.Marshal(response)
				if err != nil {
					return err
				}
				result := args[3].Invoke(string(encoded))
				if result.Type() != js.TypeString || result.String() != "" {
					return fmt.Errorf("nextgo: module action result rejected")
				}
				return nil
			}
			var outcome ActionViewOutcome
			var err error
			if refresh {
				outcome, err = ActionView(ctx, args[0].String(), request, validate)
			} else {
				err = DispatchMutation(ctx, args[0].String(), request, validate)
			}
			result := moduleActionViewResult{Applied: outcome.Applied, Status: outcome.Status}
			if err != nil {
				result.PreError = err.Error()
			}
			if outcome.Err != nil {
				result.Error = outcome.Err.Error()
			}
			encoded, _ := json.Marshal(result)
			resolve.Invoke(string(encoded))
		}()
		return nil
	})
	promise := win.Get("Promise").New(executor)
	executor.Release()
	return promise
}
