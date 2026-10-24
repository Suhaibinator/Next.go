//go:build js && wasm

package client

import (
	"fmt"
	"syscall/js"
)

var navigationOpener js.Value
var modalReturnFocus js.Value

// Modal upgrades an existing server-rendered dialog into a native modal. The
// browser supplies focus containment and inert background semantics. Dismiss
// handles Escape; close buttons can call the same application callback.
func (s *Scope) Modal(dialog Element, dismiss func()) (err error) {
	if s.closed || dismiss == nil {
		return fmt.Errorf("modal requires an open scope and dismissal callback")
	}
	defer func() {
		if v := recover(); v != nil {
			err = fmt.Errorf("modal: %v", v)
		}
	}()
	node := dialog.Value
	if node.Get("tagName").String() != "DIALOG" || node.Get("showModal").Type() != js.TypeFunction {
		return fmt.Errorf("native dialog support required")
	}
	previous := doc.Get("activeElement")
	if !navigationOpener.IsUndefined() && !navigationOpener.IsNull() && navigationOpener.Get("isConnected").Bool() {
		previous = navigationOpener
	} else if !modalReturnFocus.IsUndefined() && !modalReturnFocus.IsNull() && modalReturnFocus.Get("isConnected").Bool() {
		previous = modalReturnFocus
	}
	if node.Get("open").Bool() {
		node.Call("close")
	}
	node.Call("showModal")
	s.Cleanup(func() {
		if node.Get("open").Bool() {
			node.Call("close")
		}
		if !previous.IsNull() && !previous.IsUndefined() && previous.Get("isConnected").Bool() {
			previous.Call("focus", map[string]any{"preventScroll": true})
		}
		// Retained owners may be detached while a slot patch is assembled.
		// Navigation restores this reference once the retained nodes reconnect.
		modalReturnFocus = previous
		if moduleMode {
			_ = moduleCall("returnFocus", previous)
		}
	})
	s.On(dialog, "cancel", func(event Event) { event.PreventDefault(); dismiss() })
	s.On(dialog, "keydown", func(event Event) {
		if event.Value.Get("key").String() != "Tab" {
			return
		}
		nodes := node.Call("querySelectorAll", `a[href],button,input,select,textarea,[tabindex]`)
		focusable := []js.Value{}
		for i := 0; i < nodes.Length(); i++ {
			candidate := nodes.Index(i)
			if candidate.Get("tabIndex").Int() < 0 || candidate.Get("disabled").Truthy() || candidate.Call("getClientRects").Length() == 0 {
				continue
			}
			if !candidate.Call("closest", "[hidden],[inert]").IsNull() {
				continue
			}
			if win.Call("getComputedStyle", candidate).Get("visibility").String() == "hidden" {
				continue
			}
			focusable = append(focusable, candidate)
		}
		if len(focusable) == 0 {
			event.PreventDefault()
			node.Call("setAttribute", "tabindex", "-1")
			node.Call("focus")
			return
		}
		active := doc.Get("activeElement")
		if event.Value.Get("shiftKey").Bool() && active.Equal(focusable[0]) {
			event.PreventDefault()
			focusable[len(focusable)-1].Call("focus")
		}
		if !event.Value.Get("shiftKey").Bool() && active.Equal(focusable[len(focusable)-1]) {
			event.PreventDefault()
			focusable[0].Call("focus")
		}
	})
	return nil
}
