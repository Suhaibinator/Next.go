//go:build js && wasm

package client

import (
	"net/url"
	"strings"
	"syscall/js"
	"time"
)

// Focus the destination without fighting history/fragment scroll restoration.
// A refresh or query update keeps focus when the active element was retained.
func completeAccessibleNavigation(previous, target string, active js.Value, title string) {
	before, after := win.Get("URL").New(previous), win.Get("URL").New(target)
	changedPath := before.Get("pathname").String() != after.Get("pathname").String()
	lostFocus := !active.IsNull() && !active.IsUndefined() && (!active.Get("isConnected").Bool() || hiddenRetainedNode(active))
	changedFragment := before.Get("hash").String() != after.Get("hash").String() && len(after.Get("hash").String()) > 1
	// A disposed native modal restores its connected opener, or an incoming
	// modal focuses its controls. Keep that browser-managed contextual focus.
	dialogFocus := false
	now := doc.Get("activeElement")
	if !modalReturnFocus.IsUndefined() && !modalReturnFocus.IsNull() && modalReturnFocus.Get("isConnected").Bool() && (!now.Truthy() || now.Call("closest", "dialog").IsNull()) {
		modalReturnFocus.Call("focus", map[string]any{"preventScroll": true})
		dialogFocus = true
	}
	if !active.IsNull() && !active.IsUndefined() && active.Get("closest").Type() == js.TypeFunction && !active.Call("closest", "dialog").IsNull() {
		now := doc.Get("activeElement")
		dialogFocus = dialogFocus || !now.IsNull() && now.Get("isConnected").Bool() && now.Get("tagName").String() != "BODY" && !now.Equal(active)
	}
	if (changedPath || lostFocus || changedFragment) && !dialogFocus {
		destination := js.Null()
		if hash := after.Get("hash").String(); len(hash) > 1 {
			if id, err := url.PathUnescape(hash[1:]); err == nil {
				destination = doc.Call("getElementById", id)
			}
		}
		if destination.IsNull() {
			destination = applicationQuery("[data-nextgo-focus]")
		}
		if destination.IsNull() {
			destination = applicationQuery("main,h1")
		}
		if destination.IsNull() || destination.Get("focus").Type() != js.TypeFunction {
			destination = doc.Call("getElementById", "nextgo-app")
		}
		tabIndex := destination.Get("tabIndex")
		if !destination.Call("hasAttribute", "tabindex").Bool() && (tabIndex.Type() != js.TypeNumber || tabIndex.Int() < 0) {
			destination.Call("setAttribute", "tabindex", "-1")
		}
		destination.Call("focus", map[string]any{"preventScroll": true})
	}
	if previous == target {
		return
	}
	announcer := doc.Call("getElementById", "nextgo-announcer")
	if announcer.IsNull() {
		return
	}
	message := strings.TrimSpace(title)
	if message == "" {
		heading := applicationQuery("h1")
		if !heading.IsNull() {
			message = strings.TrimSpace(heading.Get("textContent").String())
		}
	}
	if message == "" {
		message = after.Get("pathname").String()
	}
	announcer.Set("textContent", "")
	id := navigationID
	go func() {
		time.Sleep(10 * time.Millisecond)
		if id == navigationID && currentURL == target {
			announcer.Set("textContent", message)
		}
	}()
}
