//go:build js && wasm && nextgo_module

package client

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"syscall/js"
	"testing"
	"time"
)

func moduleActionHostFixture(t *testing.T, run func([]js.Value, js.Value, js.Value)) {
	moduleTransportHostFixture(t, "actionView", run)
}

func moduleTransportHostFixture(t *testing.T, method string, run func([]js.Value, js.Value, js.Value)) {
	t.Helper()
	oldHost, oldWin := moduleHost, win
	win = js.Global()
	moduleHost = js.Global().Get("Object").New()
	callback := js.FuncOf(func(_ js.Value, args []js.Value) any {
		var executor js.Func
		executor = js.FuncOf(func(_ js.Value, promises []js.Value) any {
			run(args, promises[0], promises[1])
			return nil
		})
		promise := js.Global().Get("Promise").New(executor)
		executor.Release()
		return promise
	})
	moduleHost.Set(method, callback)
	t.Cleanup(func() { moduleHost, win = oldHost, oldWin; callback.Release() })
}

func moduleValidateResponse(t *testing.T, callback js.Value, status int, body string) string {
	t.Helper()
	encoded, err := json.Marshal(Response{URL: "https://site.test/_nextgo/actions/save/v1", StatusCode: status, Header: Headers{"x-nextgo-action-protocol": {"1"}}, Body: []byte(body)})
	if err != nil {
		t.Fatal(err)
	}
	return callback.Invoke(string(encoded)).String()
}

func moduleFinishAction(resolve js.Value, result moduleActionViewResult) {
	encoded, _ := json.Marshal(result)
	resolve.Invoke(string(encoded))
}

func TestModuleTargetActionViewDelegatesBodyAndSynchronousTypedValidation(t *testing.T) {
	validated := false
	moduleActionHostFixture(t, func(args []js.Value, resolve, _ js.Value) {
		if len(args) != 5 || args[0].String() != "/_nextgo/actions/save/v1" {
			t.Error("action host target changed")
		}
		var metadata moduleActionViewRequest
		if err := json.Unmarshal([]byte(args[1].String()), &metadata); err != nil || metadata.Method != "POST" || metadata.Headers["X-Example"] != "typed" || metadata.Redirect != "error" {
			t.Error("action transport metadata changed", metadata, err)
		}
		body := make([]byte, args[2].Get("byteLength").Int())
		js.CopyBytesToGo(body, args[2])
		if string(body) != `{"count":2}` {
			t.Error("action input body changed", string(body))
		}
		if rejected := moduleValidateResponse(t, args[3], 200, `{"data":{"count":3}}`); rejected != "" || !validated {
			t.Error("validator was not synchronous", rejected, validated)
		}
		moduleFinishAction(resolve, moduleActionViewResult{Applied: true, Status: 200})
	})
	outcome, err := actionViewFromHost(context.Background(), "/_nextgo/actions/save/v1", FetchOptions{Method: "POST", Headers: map[string]string{"X-Example": "typed"}, Body: []byte(`{"count":2}`), Redirect: "error"}, func(response Response) error {
		validated = true
		if response.StatusCode != 200 || string(response.Body) != `{"data":{"count":3}}` || response.Header.Get("X-Nextgo-Action-Protocol") != "1" {
			t.Error("result JSON or response headers changed", response)
		}
		return nil
	})
	if err != nil || !outcome.Applied || outcome.Status != 200 || outcome.Err != nil || !validated {
		t.Fatal(outcome, err, validated)
	}
}

func TestModuleTargetActionViewPreservesOriginalValidationError(t *testing.T) {
	want := errors.New("original typed validation failure")
	moduleActionHostFixture(t, func(args []js.Value, resolve, _ js.Value) {
		if rejected := moduleValidateResponse(t, args[3], 422, `{"error":{"code":"validation_error"}}`); rejected != want.Error() {
			t.Error("typed failure did not cross synchronous validator", rejected)
		}
		moduleFinishAction(resolve, moduleActionViewResult{PreError: "opaque core rejection"})
	})
	outcome, err := actionViewFromHost(context.Background(), "/save", FetchOptions{}, func(Response) error { return want })
	if !errors.Is(err, want) || outcome.Applied {
		t.Fatal("typed error identity lost", outcome, err)
	}
}

func TestModuleTargetActionViewSourceCancellationRetainsValidatorUntilPhysicalSettlement(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	wantCause := errors.New("source lifetime superseded by another view")
	defer cancel(nil)
	var validator, resolve js.Value
	aborted, finish, started := make(chan struct{}), make(chan struct{}), make(chan struct{})
	moduleActionHostFixture(t, func(args []js.Value, done, _ js.Value) {
		validator, resolve = args[3], done
		abort := js.FuncOf(func(js.Value, []js.Value) any { close(aborted); return nil })
		args[4].Call("addEventListener", "abort", abort, map[string]any{"once": true})
		t.Cleanup(abort.Release)
		if rejected := moduleValidateResponse(t, validator, 200, `{"data":7}`); rejected != "" {
			t.Error("initial typed confirmation failed", rejected)
		}
		close(started)
		go func() {
			<-finish
			if rejected := moduleValidateResponse(t, validator, 200, `{"data":8}`); !strings.Contains(rejected, "twice") {
				t.Error("released/lost validator or repeated typed decode after cancellation", rejected)
			}
			moduleFinishAction(resolve, moduleActionViewResult{Error: context.Canceled.Error()})
		}()
	})
	called := make(chan struct{})
	result := make(chan struct {
		outcome ActionViewOutcome
		err     error
	}, 1)
	go func() {
		outcome, err := actionViewFromHost(ctx, "/save", FetchOptions{}, func(Response) error { close(called); return nil })
		result <- struct {
			outcome ActionViewOutcome
			err     error
		}{outcome, err}
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("action bridge was not installed")
	}
	cancel(wantCause)
	select {
	case <-aborted:
	case <-time.After(time.Second):
		t.Fatal("source cancellation did not reach core signal")
	}
	select {
	case got := <-result:
		t.Fatal("returned before physical host settlement", got)
	default:
	}
	close(finish)
	select {
	case got := <-result:
		if got.err != nil || !errors.Is(got.outcome.Err, wantCause) {
			t.Fatal("confirmed result was lost after cancellation", got)
		}
	case <-time.After(time.Second):
		t.Fatal("host settlement did not finish")
	}
	<-called
}

func TestModuleTargetActionViewOwnCommitMayUnmountSourceWithoutLosingSuccess(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	moduleActionHostFixture(t, func(args []js.Value, resolve, _ js.Value) {
		if rejected := moduleValidateResponse(t, args[3], 200, `{"data":9}`); rejected != "" {
			t.Fatal(rejected)
		}
		// Core's successful commit disposes the initiating component and then
		// settles its independent view lifetime. The module must keep waiting.
		cancel()
		moduleFinishAction(resolve, moduleActionViewResult{Applied: true, Status: 200})
	})
	outcome, err := actionViewFromHost(ctx, "/save", FetchOptions{}, func(Response) error { return nil })
	if err != nil || outcome.Err != nil || !outcome.Applied {
		t.Fatal("self-commit cancellation replaced confirmed success", outcome, err)
	}
}

func TestModuleTargetActionViewLateHostRejectionIsViewFailure(t *testing.T) {
	moduleActionHostFixture(t, func(args []js.Value, _, reject js.Value) {
		if rejected := moduleValidateResponse(t, args[3], 200, `{"data":1}`); rejected != "" {
			t.Fatal(rejected)
		}
		reject.Invoke("post-result stream interrupted")
	})
	outcome, err := actionViewFromHost(context.Background(), "/save", FetchOptions{}, func(Response) error { return nil })
	if err != nil || outcome.Err == nil || !strings.Contains(outcome.Err.Error(), "interrupted") {
		t.Fatal("confirmed result was replaced by late transport error", outcome, err)
	}
}

func TestModuleTargetActionViewRejectsConfirmationAfterSourceCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	validated := false
	moduleActionHostFixture(t, func(args []js.Value, resolve, _ js.Value) {
		cancel()
		if rejected := moduleValidateResponse(t, args[3], 200, `{"data":1}`); rejected != context.Canceled.Error() {
			t.Error("accepted result after source cancellation", rejected)
		}
		moduleFinishAction(resolve, moduleActionViewResult{PreError: "result rejected"})
	})
	outcome, err := actionViewFromHost(ctx, "/save", FetchOptions{}, func(Response) error { validated = true; return nil })
	if !errors.Is(err, context.Canceled) || validated || outcome.Applied {
		t.Fatal("preconfirmation cancellation lost its original meaning", outcome, err, validated)
	}
}

func TestModuleTargetActionViewMalformedHostReturnAfterConfirmationKeepsDataKnown(t *testing.T) {
	oldHost, oldWin := moduleHost, win
	win = js.Global()
	moduleHost = js.Global().Get("Object").New()
	callback := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if rejected := moduleValidateResponse(t, args[3], 200, `{"data":4}`); rejected != "" {
			t.Error(rejected)
		}
		return "invalid non-Promise return"
	})
	moduleHost.Set("actionView", callback)
	t.Cleanup(func() { moduleHost, win = oldHost, oldWin; callback.Release() })
	known := false
	outcome, err := actionViewFromHost(context.Background(), "/save", FetchOptions{}, func(Response) error { known = true; return nil })
	if !known || err != nil || outcome.Err == nil {
		t.Fatal("malformed host return revoked an already-known result", outcome, err, known)
	}
}

func TestModuleTargetActionViewMultipartForwardsFreshBrowserFormData(t *testing.T) {
	form, err := NewMultipart(4096)
	if err != nil {
		t.Fatal(err)
	}
	defer form.Close()
	if err := form.AddField("amount", "2"); err != nil {
		t.Fatal(err)
	}
	moduleActionHostFixture(t, func(args []js.Value, resolve, _ js.Value) {
		if !args[2].InstanceOf(js.Global().Get("FormData")) || args[2].Call("get", "amount").String() != "2" {
			t.Error("multipart was serialized or changed across host ABI")
		}
		if rejected := moduleValidateResponse(t, args[3], 200, `{"data":2}`); rejected != "" {
			t.Error(rejected)
		}
		moduleFinishAction(resolve, moduleActionViewResult{Applied: true, Status: 200})
	})
	outcome, err := actionViewFromHost(context.Background(), "/save", FetchOptions{Multipart: form}, func(Response) error { return nil })
	if err != nil || !outcome.Applied || outcome.Err != nil {
		t.Fatal(outcome, err)
	}
}

func TestModuleTargetOrdinaryActionFetchFencesDispatchAndOnlyInvalidatesSettlement(t *testing.T) {
	fakeActionEnvironment(t)
	oldHost := moduleHost
	moduleHost = js.Global().Get("Object").New()
	fences, invalidations := 0, 0
	fence := js.FuncOf(func(js.Value, []js.Value) any { fences++; return "" })
	invalidate := js.FuncOf(func(js.Value, []js.Value) any { invalidations++; return "" })
	moduleHost.Set("actionFence", fence)
	moduleHost.Set("invalidate", invalidate)
	t.Cleanup(func() { moduleHost = oldHost; fence.Release(); invalidate.Release() })
	mockBrowserFetch(t, func([]js.Value) any {
		if fences != 1 || invalidations != 0 {
			t.Error("ordinary module action did not fence core before Fetch")
		}
		return browserResponse([]byte("ok"), 200, nil)
	})
	if _, err := Fetch(context.Background(), "/app/_nextgo/actions/save/v1", FetchOptions{Method: "POST"}); err != nil || fences != 1 || invalidations != 1 {
		t.Fatal("settlement must clear reuse without superseding a newer action view", err, fences, invalidations)
	}
}

func TestModuleTargetTypedMutationDelegatesCompleteTransportAndValidation(t *testing.T) {
	localFetch := false
	mockBrowserFetch(t, func([]js.Value) any { localFetch = true; return browserResponse([]byte("unexpected"), 500, nil) })
	validated := false
	moduleTransportHostFixture(t, "mutation", func(args []js.Value, resolve, _ js.Value) {
		if rejected := args[3].Invoke(js.Null()).String(); rejected != "" || validated {
			t.Error("dispatch probe consumed application validation", rejected, validated)
		}
		if args[0].String() != "/_nextgo/actions/save/v1" {
			t.Error("typed mutation target changed")
		}
		input := make([]byte, args[2].Get("byteLength").Int())
		js.CopyBytesToGo(input, args[2])
		if string(input) != `{"amount":2}` {
			t.Error("typed mutation body changed", string(input))
		}
		if rejected := moduleValidateResponse(t, args[3], 200, `{"data":{"count":2}}`); rejected != "" || !validated {
			t.Error("core did not synchronously validate in the source runtime", rejected, validated)
		}
		moduleFinishAction(resolve, moduleActionViewResult{})
	})
	err := DispatchMutation(context.Background(), "/_nextgo/actions/save/v1", FetchOptions{Method: "POST", Body: []byte(`{"amount":2}`)}, func(response Response) error {
		validated = true
		if string(response.Body) != `{"data":{"count":2}}` {
			t.Error("typed output changed")
		}
		return nil
	})
	if err != nil || !validated || localFetch {
		t.Fatal("module performed local transport or lost typed result", err, validated, localFetch)
	}
}

func TestModuleTargetTypedMutationPreservesOriginalErrors(t *testing.T) {
	t.Run("validation", func(t *testing.T) {
		want := errors.New("typed mutation validation identity")
		moduleTransportHostFixture(t, "mutation", func(args []js.Value, resolve, _ js.Value) {
			if rejected := moduleValidateResponse(t, args[3], 422, `{"error":{"code":"validation_error"}}`); rejected != want.Error() {
				t.Error("typed validation error was lost", rejected)
			}
			moduleFinishAction(resolve, moduleActionViewResult{PreError: "source validator rejected"})
		})
		if err := DispatchMutation(context.Background(), "/save", FetchOptions{}, func(Response) error { return want }); !errors.Is(err, want) {
			t.Fatal("ordinary typed validation identity was serialized away", err)
		}
	})
	for _, phase := range []string{"before-dispatch", "queued-before-validator"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			want := errors.New("original cancellation cause: " + phase)
			called := false
			moduleTransportHostFixture(t, "mutation", func(_ []js.Value, resolve, _ js.Value) {
				called = true
				cancel(want)
				moduleFinishAction(resolve, moduleActionViewResult{PreError: context.Canceled.Error()})
			})
			if phase == "before-dispatch" {
				cancel(want)
			}
			err := DispatchMutation(ctx, "/save", FetchOptions{}, func(Response) error { t.Error("canceled request reached validator"); return nil })
			if !errors.Is(err, want) || called != (phase == "queued-before-validator") {
				t.Fatal("module replaced original cancellation cause", err, called)
			}
		})
	}
}

func TestModuleTargetTypedMutationsHaveOnlyOneCoreOwnedQueue(t *testing.T) {
	type pending struct {
		args    []js.Value
		resolve js.Value
	}
	entered := make(chan pending, 2)
	moduleTransportHostFixture(t, "mutation", func(args []js.Value, resolve, _ js.Value) { entered <- pending{args, resolve} })
	completed := make(chan error, 2)
	start := func(body string) {
		go func() {
			completed <- DispatchMutation(context.Background(), "/save", FetchOptions{Body: []byte(body)}, func(Response) error { return nil })
		}()
	}
	start("first")
	var first, second pending
	select {
	case first = <-entered:
	case <-time.After(time.Second):
		t.Fatal("first mutation never reached core host")
	}
	start("second")
	// Both submissions must enter core while the first is pending. A private
	// queue in this module would bypass ordering with other document runtimes.
	select {
	case second = <-entered:
	case <-time.After(time.Second):
		t.Fatal("module installed a private queue instead of delegating to core")
	}
	select {
	case err := <-completed:
		t.Fatal("module completed without the core's transport/validation", err)
	default:
	}
	for _, call := range []pending{first, second} {
		if rejected := moduleValidateResponse(t, call.args[3], 200, `{"data":1}`); rejected != "" {
			t.Error(rejected)
		}
		moduleFinishAction(call.resolve, moduleActionViewResult{})
		select {
		case err := <-completed:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Fatal("core settlement did not return the typed call")
		}
	}
}

func TestModuleTargetQueuedTypedMutationCancellationKeepsPhysicalCallbackLifetime(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	wantCause := errors.New("queued typed mutation owner was closed")
	defer cancel(nil)
	started, aborted, finish := make(chan struct{}), make(chan struct{}), make(chan struct{})
	moduleTransportHostFixture(t, "mutation", func(args []js.Value, resolve, _ js.Value) {
		abort := js.FuncOf(func(js.Value, []js.Value) any { close(aborted); return nil })
		args[4].Call("addEventListener", "abort", abort, map[string]any{"once": true})
		t.Cleanup(abort.Release)
		close(started)
		go func() {
			<-finish
			if rejected := moduleValidateResponse(t, args[3], 200, `{"data":1}`); rejected != wantCause.Error() {
				t.Error("released validator or accepted canceled queued mutation", rejected)
			}
			moduleFinishAction(resolve, moduleActionViewResult{PreError: context.Canceled.Error()})
		}()
	})
	done := make(chan error, 1)
	validated := false
	go func() {
		done <- DispatchMutation(ctx, "/save", FetchOptions{}, func(Response) error { validated = true; return nil })
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("queued mutation did not reach core")
	}
	cancel(wantCause)
	select {
	case <-aborted:
	case <-time.After(time.Second):
		t.Fatal("queued mutation cancellation did not reach core")
	}
	select {
	case err := <-done:
		t.Fatal("module released its validator before physical core settlement", err)
	default:
	}
	close(finish)
	select {
	case err := <-done:
		if !errors.Is(err, wantCause) || validated {
			t.Fatal("canceled queued call lost its context cause", err, validated)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled queued call did not settle")
	}
}

func TestModuleTargetDispatchProbeRejectsClosedSourceBeforeAbortWatcherRuns(t *testing.T) {
	for _, method := range []string{"mutation", "actionView"} {
		t.Run(method, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			want := errors.New("queued module scope was synchronously disposed")
			validated, dispatched := false, false
			moduleTransportHostFixture(t, method, func(args []js.Value, resolve, _ js.Value) {
				// This runs synchronously inside moduleHost.Call, before the
				// source creates its watcher goroutine. No timing/sleep needed.
				cancel(want)
				if args[4].Get("aborted").Bool() {
					t.Error("test did not isolate the pre-watcher cancellation race")
				}
				if rejected := args[3].Invoke(js.Null()).String(); rejected == "" {
					dispatched = true
				} else if rejected != want.Error() {
					t.Error("dispatch guard lost the source cause", rejected)
				}
				moduleFinishAction(resolve, moduleActionViewResult{PreError: "source dispatch guard rejected"})
			})
			validate := func(Response) error { validated = true; return nil }
			var err error
			if method == "mutation" {
				err = DispatchMutation(ctx, "/save", FetchOptions{}, validate)
			} else {
				_, err = ActionView(ctx, "/save", FetchOptions{}, validate)
			}
			if !errors.Is(err, want) || validated || dispatched {
				t.Fatal("closed queued scope reached mutation dispatch", err, validated, dispatched)
			}
		})
	}
}

func TestModuleTargetMutationAdmissionSentinelsPreserveIdentityAcrossHostABI(t *testing.T) {
	for _, method := range []string{"mutation", "actionView"} {
		for _, tc := range []struct {
			name string
			err  error
		}{{"full", ErrMutationQueueFull}, {"frozen", ErrMutationQueueFrozen}} {
			t.Run(method+"/"+tc.name, func(t *testing.T) {
				validated, fetched := false, false
				mockBrowserFetch(t, func([]js.Value) any { fetched = true; return browserResponse(nil, 500, nil) })
				moduleTransportHostFixture(t, method, func(_ []js.Value, resolve, _ js.Value) {
					moduleFinishAction(resolve, moduleActionViewResult{PreError: tc.err.Error()})
				})
				validate := func(Response) error { validated = true; return nil }
				var err error
				if method == "mutation" {
					err = DispatchMutation(context.Background(), "/save", FetchOptions{}, validate)
				} else {
					_, err = ActionView(context.Background(), "/save", FetchOptions{}, validate)
				}
				if !errors.Is(err, tc.err) || fetched || validated {
					t.Fatal("known no-dispatch admission error lost its identity", err, fetched, validated)
				}
			})
		}
	}
}
