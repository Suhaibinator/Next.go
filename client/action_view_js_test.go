//go:build js && wasm && !nextgo_module

package client

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"syscall/js"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/internal/actionview"
	"github.com/Suhaibinator/Next.go/internal/navigation"
)

func actionViewEnvironment(t *testing.T) map[string]string {
	t.Helper()
	fakeActionEnvironment(t)
	oldURL, oldManifest, oldCancel, oldActive, oldSeq, oldID, oldFrozen, oldDraft, oldStream, oldRetry := currentURL, routeManifest, cancelNavigation, activeActionView, frameworkActionSequence, navigationID, actionViewFrozen, draftFrozen, activeNavigationStream, streamRetry
	currentURL = "https://site.test/app/current?q=one#keep"
	routeManifest = navigation.Manifest{}
	cancelNavigation = nil
	activeActionView = nil
	frameworkActionSequence = 0
	navigationID = 10
	actionViewFrozen = false
	draftFrozen = false
	activeNavigationStream = nil
	streamRetry = nil
	attrs := map[string]string{"data-nextgo-build": "build", "data-nextgo-configuration": strings.Repeat("a", 64), "data-nextgo-draft": ""}
	get := js.FuncOf(func(_ js.Value, args []js.Value) any { return attrs[args[0].String()] })
	doc.Set("body", map[string]any{"getAttribute": get})
	win.Set("location", map[string]any{"origin": "https://site.test", "href": currentURL})
	win.Set("history", map[string]any{"state": map[string]any{"nextgoEntry": "entry"}})
	win.Set("JSON", js.Global().Get("JSON"))
	t.Cleanup(func() {
		if activeActionView != nil {
			activeActionView.cancel()
		}
		invalidateCache()
		currentURL, routeManifest, cancelNavigation, activeActionView, frameworkActionSequence, navigationID, actionViewFrozen, draftFrozen, activeNavigationStream, streamRetry = oldURL, oldManifest, oldCancel, oldActive, oldSeq, oldID, oldFrozen, oldDraft, oldStream, oldRetry
		get.Release()
	})
	return attrs
}
func actionViewResponse(body string, status int, stream bool) js.Value {
	headers := map[string]any{"Content-Type": "application/json", "X-Nextgo-Action-Protocol": "1", "X-Nextgo-Action-Revision": "v1"}
	if stream {
		headers["Content-Type"] = actionview.MediaType
		headers[actionview.Header] = "1"
		headers[navigation.ProtocolHeader] = fmt.Sprint(navigation.ProtocolVersion)
		headers[navigation.ConfigurationHeader] = strings.Repeat("a", 64)
		headers[navigation.DraftHeader] = ""
	}
	response := js.Global().Get("Response").New(body, map[string]any{"status": status, "headers": headers})
	js.Global().Get("Object").Call("defineProperty", response, "url", map[string]any{"value": "https://site.test/app/_nextgo/actions/add/v1"})
	return js.Global().Get("Promise").Call("resolve", response)
}

const confirmedActionFrame = `{"version":1,"type":"result","result":{"data":{"count":7}}}` + "\n"
const failedActionViewFrame = `{"version":1,"type":"view-error","error":{"code":"busy","message":"Try refreshing the view.","status":503}}` + "\n"

func TestActionViewConfirmsBeforeLaterViewFailureAndOwnsRequestScope(t *testing.T) {
	actionViewEnvironment(t)
	requests, confirmed := 0, 0
	mockBrowserFetch(t, func(args []js.Value) any {
		requests++
		headers := args[1].Get("headers")
		for key, want := range map[string]string{actionview.URLHeader: "/app/current?q=one", actionview.BuildHeader: "build", actionview.Header: "1", navigation.ConfigurationHeader: strings.Repeat("a", 64), "Accept": actionview.MediaType} {
			if got := headers.Call("get", key).String(); got != want {
				t.Errorf("%s=%q want%q", key, got, want)
			}
		}
		if headers.Call("has", navigation.ShellHeader).Bool() || headers.Call("has", navigation.ShellProtocolHeader).Bool() {
			t.Error("public shell negotiation leaked")
		}
		return actionViewResponse(confirmedActionFrame+failedActionViewFrame, 200, true)
	})
	view, err := ActionView(context.Background(), "/app/_nextgo/actions/add/v1", FetchOptions{Headers: map[string]string{"x-nextgo-action-url": "/forged", "X-Nextgo-Shell": "cached"}}, func(value Response) error {
		confirmed++
		if string(value.Body) != `{"data":{"count":7}}` || value.StatusCode != 200 {
			t.Error(value)
		}
		return nil
	})
	if err != nil || confirmed != 1 || view.Applied || view.Status != 503 || view.Err == nil || requests != 1 {
		t.Fatal(view, err, confirmed, requests)
	}
}

func TestActionViewTypedRejectionNeverConfirmsOrConsumesView(t *testing.T) {
	actionViewEnvironment(t)
	want := errors.New("typed output mismatch")
	calls := 0
	mockBrowserFetch(t, func([]js.Value) any { return actionViewResponse(confirmedActionFrame+failedActionViewFrame, 200, true) })
	view, err := ActionView(context.Background(), "/app/_nextgo/actions/add/v1", FetchOptions{}, func(Response) error { calls++; return want })
	if !errors.Is(err, want) || view.Err != nil || view.Applied || calls != 1 {
		t.Fatal(view, err, calls)
	}
}

func TestActionViewOrdinaryJSONErrorsAndOldSuccessRemainDistinct(t *testing.T) {
	for _, success := range []bool{false, true} {
		t.Run(fmt.Sprint(success), func(t *testing.T) {
			actionViewEnvironment(t)
			status := 422
			body := `{"error":{"code":"validation_error","message":"Invalid."}}`
			want := errors.New("typed validation error")
			if success {
				status = 200
				body = `{"data":{"count":7}}`
			}
			mockBrowserFetch(t, func([]js.Value) any { return actionViewResponse(body, status, false) })
			calls := 0
			view, err := ActionView(context.Background(), "/app/_nextgo/actions/add/v1", FetchOptions{}, func(value Response) error {
				calls++
				if string(value.Body) != body || value.StatusCode != status {
					t.Error(value)
				}
				if !success {
					return want
				}
				return nil
			})
			if calls != 1 || view.Applied {
				t.Fatal(view, err, calls)
			}
			if success {
				if err != nil || view.Err == nil {
					t.Fatal("ordinary success lost confirmation", view, err)
				}
			} else if !errors.Is(err, want) || view.Err != nil {
				t.Fatal("typed error lost identity", view, err)
			}
		})
	}
}

func TestActionViewConfirmedResultSurvivesTruncationSupersessionAndCallerDisposal(t *testing.T) {
	for _, kind := range []string{"truncated", "new-action", "navigation", "caller-cancel", "self-commit-cancel"} {
		t.Run(kind, func(t *testing.T) {
			actionViewEnvironment(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			wire := confirmedActionFrame
			if kind != "truncated" {
				wire += failedActionViewFrame
			}
			mockBrowserFetch(t, func([]js.Value) any { return actionViewResponse(wire, 200, true) })
			view, err := ActionView(ctx, "/app/_nextgo/actions/add/v1", FetchOptions{}, func(Response) error {
				calls++
				switch kind {
				case "new-action":
					beginFrameworkAction()
				case "navigation":
					navigationID++
				case "caller-cancel":
					cancel()
				case "self-commit-cancel":
					if e := activeActionView.commitGuard(); e != nil {
						t.Fatal(e)
					}
					cancel()
				}
				return nil
			})
			if err != nil || view.Err == nil || calls != 1 {
				t.Fatal("confirmed result revoked", view, err, calls)
			}
			if kind == "self-commit-cancel" && (view.Status != 503 || errors.Is(view.Err, context.Canceled)) {
				t.Fatal("own unmount canceled view transport", view)
			}
		})
	}
}

func TestActionViewGuardChecksLatestOwnershipInsideCommit(t *testing.T) {
	for _, kind := range []string{"action", "navigation", "URL", "build", "configuration", "history", "draft", "frozen"} {
		t.Run(kind, func(t *testing.T) {
			attrs := actionViewEnvironment(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			owner := &actionViewOwner{ctx: ctx, cancel: cancel, caller: ctx, sequence: frameworkActionSequence, navigation: navigationID, source: currentURL, build: "build", configuration: attrs["data-nextgo-configuration"], history: "entry"}
			activeActionView = owner
			if err := owner.check(); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "action":
				frameworkActionSequence++
			case "navigation":
				navigationID++
			case "URL":
				win.Get("location").Set("href", currentURL+"-new")
			case "build":
				attrs["data-nextgo-build"] = "other"
			case "configuration":
				attrs["data-nextgo-configuration"] = "other"
			case "history":
				win.Get("history").Set("state", map[string]any{"nextgoEntry": "other"})
			case "draft":
				attrs["data-nextgo-draft"] = "1"
			case "frozen":
				actionViewFrozen = true
			}
			if err := owner.commitGuard(); err == nil || owner.detached {
				t.Fatal("stale owner detached/committed", kind, err)
			}
		})
	}
}

func TestActionViewOlderOrdinarySettlementDoesNotFenceNewerConfirmedView(t *testing.T) {
	actionViewEnvironment(t)
	old := newControlledPromise()
	started, settled := make(chan struct{}, 1), make(chan error, 1)
	mockBrowserFetch(t, func(args []js.Value) any {
		if strings.Contains(args[0].String(), "older") {
			started <- struct{}{}
			return old.value
		}
		return actionViewResponse(confirmedActionFrame+failedActionViewFrame, 200, true)
	})
	go func() {
		_, err := Fetch(context.Background(), "/app/_nextgo/actions/older/v1", FetchOptions{Method: "POST"})
		settled <- err
	}()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("older request did not start")
	}
	view, err := ActionView(context.Background(), "/app/_nextgo/actions/add/v1", FetchOptions{}, func(Response) error {
		seq := frameworkActionSequence
		old.resolve.Invoke(js.Global().Get("Response").New("older request completed"))
		select {
		case err := <-settled:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Fatal("older response did not settle")
		}
		if frameworkActionSequence != seq || activeActionView.ctx.Err() != nil {
			t.Error("older settlement fenced newer intent")
		}
		return nil
	})
	if err != nil || view.Status != 503 || view.Err == nil || errors.Is(view.Err, context.Canceled) {
		t.Fatal(view, err)
	}
}

func TestActionViewGuardRejectsChangedParallelSlotSelection(t *testing.T) {
	attrs := actionViewEnvironment(t)
	values := map[string]string{"data-nextgo-slot": strings.Repeat("b", 64), "data-nextgo-slot-hash": strings.Repeat("c", 64), "data-nextgo-slot-retain": "true", "data-nextgo-slot-url": "/app/a"}
	get := js.FuncOf(func(_ js.Value, args []js.Value) any {
		if value, ok := values[args[0].String()]; ok {
			return value
		}
		return nil
	})
	defer get.Release()
	closest := js.FuncOf(func(js.Value, []js.Value) any { return nil })
	defer closest.Release()
	node := js.ValueOf(map[string]any{"getAttribute": get, "parentElement": map[string]any{"closest": closest}})
	query := js.FuncOf(func(js.Value, []js.Value) any { return []any{node} })
	defer query.Release()
	getRoot := js.FuncOf(func(js.Value, []js.Value) any { return map[string]any{"querySelectorAll": query} })
	defer getRoot.Release()
	doc.Set("getElementById", getRoot)
	routeManifest.Parallel = "enabled"
	slots, err := currentSlotState()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	owner := &actionViewOwner{ctx: ctx, cancel: cancel, caller: ctx, sequence: frameworkActionSequence, navigation: navigationID, source: currentURL, build: "build", configuration: attrs["data-nextgo-configuration"], history: "entry", slots: slots}
	activeActionView = owner
	if err := owner.check(); err != nil {
		t.Fatal(err)
	}
	values["data-nextgo-slot-url"] = "/app/b"
	if owner.commitGuard() == nil || owner.detached {
		t.Fatal("changed parallel branch committed")
	}
}

func TestActionViewRefreshCapturesLiveScrollOnlyInsideGuardedCommit(t *testing.T) {
	for _, superseded := range []bool{false, true} {
		t.Run(fmt.Sprint(superseded), func(t *testing.T) {
			attrs := actionViewEnvironment(t)
			historyState := win.Get("history").Get("state")
			historyState.Set("nextgoX", 0)
			historyState.Set("nextgoY", 0)
			historyState.Set("userPreference", "keep")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			owner := &actionViewOwner{ctx: ctx, cancel: cancel, caller: ctx, sequence: frameworkActionSequence, navigation: navigationID, source: currentURL, build: "build", configuration: attrs["data-nextgo-configuration"], history: "entry"}
			activeActionView = owner
			carrier := owner.prepareCommitState(true)
			// The saved history coordinates predate this live user scroll while
			// the action/transition remains pending.
			win.Set("scrollX", 25)
			win.Set("scrollY", 650)
			if superseded {
				navigationID++
			}
			err := owner.commitGuard()
			if historyState.Get("nextgoY").Int() != 0 || historyState.Get("nextgoX").Int() != 0 {
				t.Fatal("old history mutated before commit")
			}
			if superseded {
				if err == nil || carrier.Get("nextgoY").Int() != 0 || owner.detached {
					t.Fatal("superseded commit stamped scroll", err)
				}
				return
			}
			if err != nil || carrier.Get("nextgoY").Int() != 650 || carrier.Get("nextgoX").Int() != 25 || carrier.Get("nextgoEntry").String() != "entry" || carrier.Get("userPreference").String() != "keep" || carrier.Equal(historyState) {
				t.Fatal("live scroll/history ownership lost", carrier, err)
			}
		})
	}
}
