//go:build js && wasm

package client

import (
	"context"
	"fmt"
	"syscall/js"
	"time"

	"github.com/Suhaibinator/Next.go/internal/navigation"
)

var draftFrozen bool
var draftRestoreID uint64
var cancelDraftRestore context.CancelFunc
var draftRefreshPending bool
var draftRetryScope *Scope
var draftRestoreFocus js.Value
var draftScrollX, draftScrollY float64
var draftFrozenURL string
var draftStatusHome, draftStatusSibling js.Value

func clearDraftRetry() {
	if draftRetryScope != nil {
		draftRetryScope.dispose()
		draftRetryScope = nil
	}
}

func freezeDraftDocument() {
	first := !draftFrozen
	draftFrozen = true
	syncLazyModules()
	if first {
		draftRestoreFocus = doc.Get("activeElement")
		draftScrollX, draftScrollY = win.Get("scrollX").Float(), win.Get("scrollY").Float()
		draftFrozenURL = currentURL
	}
	draftRestoreID++
	if cancelDraftRestore != nil {
		cancelDraftRestore()
		cancelDraftRestore = nil
	}
	clearDraftRetry()
	cancelPendingNavigation()
	invalidateCache()
	// History mutations during pagehide can evict an already-freezing frame.
	// Keep the last scroll in Go memory; normal active scroll events own history.
	doc.Get("body").Call("setAttribute", "data-nextgo-frozen", "")
}

func installDraftLifecycle() {
	if !routeManifest.Draft {
		return
	}
	freeze := js.FuncOf(func(js.Value, []js.Value) any { freezeDraftDocument(); return nil })
	resume := js.FuncOf(func(js.Value, []js.Value) any { resumeDraftDocument(); return nil })
	// These two callbacks belong to the document, like the router listeners.
	globalFunctions = append(globalFunctions, freeze, resume)
	win.Set("__nextgoFreeze", freeze)
	win.Set("__nextgoResume", resume)
}

func resumeDraftDocument() {
	freezeDraftDocument()
	id := draftRestoreID
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	cancelDraftRestore = cancel
	status := doc.Call("getElementById", "nextgo-status")
	placeDraftStatus(status)
	status.Set("hidden", false)
	status.Set("textContent", "Checking preview access…")
	go func() {
		defer cancel()
		// Leave the synchronous pageshow activation task before issuing network
		// or focus/scroll work against a renderer coming out of the native cache.
		task := time.NewTimer(time.Millisecond)
		defer task.Stop()
		select {
		case <-task.C:
		case <-ctx.Done():
			if id == draftRestoreID {
				cancelDraftRestore = nil
				draftRestoreFailure()
			}
			return
		}
		response, expected, err := fetchDraftProbe(ctx)
		if id != draftRestoreID {
			return
		}
		cancelDraftRestore = nil
		if err != nil {
			draftRestoreFailure()
			return
		}
		if !validDraftProbe(response, expected) {
			// Keep the retained private tree hidden until a fresh document arrives.
			win.Get("location").Call("reload")
			return
		}
		restoreDraftStatus(status)
		draftFrozen = false
		syncLazyModules()
		doc.Get("body").Call("removeAttribute", "data-nextgo-frozen")
		restoreDraftFocus()
		status.Set("hidden", true)
		status.Set("textContent", "")
		actual := win.Get("location").Get("href").String()
		if actual == draftFrozenURL {
			win.Call("scrollTo", map[string]any{"left": draftScrollX, "top": draftScrollY, "behavior": "instant"})
		}
		refresh := draftRefreshPending
		draftRefreshPending = false
		if actual != currentURL {
			startNavigation(actual, true, win.Get("history").Get("state"))
		} else if refresh {
			Refresh()
		}
	}()
}

func placeDraftStatus(status js.Value) {
	dialog := js.Null()
	if draftRestoreFocus.Truthy() && draftRestoreFocus.Get("isConnected").Bool() && draftRestoreFocus.Get("closest").Type() == js.TypeFunction {
		dialog = draftRestoreFocus.Call("closest", "dialog[open]")
	}
	if dialog.IsNull() {
		dialog = doc.Call("querySelector", "dialog[open]")
	}
	if dialog.IsNull() {
		return
	}
	if !draftStatusHome.Truthy() {
		draftStatusHome = status.Get("parentNode")
		draftStatusSibling = status.Get("nextSibling")
	}
	dialog.Call("appendChild", status)
}

func restoreDraftStatus(status js.Value) {
	if draftStatusHome.Truthy() && draftStatusHome.Get("isConnected").Bool() {
		if draftStatusSibling.Truthy() && draftStatusSibling.Get("parentNode").Equal(draftStatusHome) {
			draftStatusHome.Call("insertBefore", status, draftStatusSibling)
		} else {
			draftStatusHome.Call("appendChild", status)
		}
	}
	draftStatusHome, draftStatusSibling = js.Undefined(), js.Undefined()
}

func restoreDraftFocus() {
	defer func() { draftRestoreFocus = js.Undefined(); _ = recover() }()
	if draftRestoreFocus.Truthy() && draftRestoreFocus.Get("isConnected").Bool() && draftRestoreFocus.Get("focus").Type() == js.TypeFunction {
		draftRestoreFocus.Call("focus", map[string]any{"preventScroll": true})
	}
}

func fetchDraftProbe(ctx context.Context) (response Response, expected string, err error) {
	defer func() {
		if failure := recover(); failure != nil {
			err = fmt.Errorf("preview restore setup: %v", failure)
		}
	}()
	target, err := URL("/_nextgo/draft")
	if err != nil {
		return response, "", err
	}
	u := win.Get("URL").New(target, win.Get("location").Get("href"))
	expected = u.Get("href").String()
	response, err = Fetch(ctx, expected, FetchOptions{Cache: "no-store", Redirect: "error", MaxBytes: 1024})
	return
}

func validDraftProbe(response Response, expected string) bool {
	return response.StatusCode == 204 && len(response.Body) == 0 && response.URL == expected &&
		response.Header.Get(navigation.ProtocolHeader) == fmt.Sprint(navigation.ProtocolVersion) &&
		response.Header.Get(navigation.ConfigurationHeader) == documentConfiguration() &&
		response.Header.Get(navigation.BuildHeader) == doc.Get("body").Call("getAttribute", "data-nextgo-build").String() &&
		response.Header.Get(navigation.DraftHeader) == documentDraft()
}

func draftRestoreFailure() {
	clearDraftRetry()
	status := doc.Call("getElementById", "nextgo-status")
	status.Set("hidden", false)
	status.Set("textContent", "Unable to verify preview access. ")
	button := doc.Call("createElement", "button")
	button.Set("type", "button")
	button.Set("textContent", "Retry verification")
	status.Call("appendChild", button)
	draftRetryScope = &Scope{Root: Element{status}}
	draftRetryScope.On(Element{button}, "click", func(Event) { resumeDraftDocument() })
	button.Call("focus", map[string]any{"preventScroll": true})
}
