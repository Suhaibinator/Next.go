//go:build js && wasm

package client

import (
	"syscall/js"
	"testing"
)

type linkStatusFixture struct {
	t         *testing.T
	scope     *Scope
	links     []Element
	functions []js.Func
}

func linkStatusEnvironment(t *testing.T) *linkStatusFixture {
	t.Helper()
	oldDoc, oldWin, oldLocation, oldURL := doc, win, committedLocation, currentURL
	oldObservers, oldNext := linkStatusObservers, nextLinkStatusObserver
	oldID, oldNode, oldWaiting := linkNavigationID, linkNavigationNode, linkNavigationWaiting
	oldCount := moduleLinkCallbackCount
	oldFrozen := draftFrozen
	linkStatusObservers, nextLinkStatusObserver = map[uint64]*linkStatusObserver{}, 0
	linkNavigationID, linkNavigationNode, linkNavigationWaiting = 0, js.Undefined(), false
	moduleLinkCallbackCount, draftFrozen = 0, false
	currentURL = "https://site.test/current"
	committedLocation = Location{URL: currentURL, Pathname: "/current"}
	f := &linkStatusFixture{t: t}
	root := js.Global().Get("Object").New()
	root.Set("contains", f.function(func(_ js.Value, args []js.Value) any { return args[0].Get("fixtureOwner").Equal(root) }))
	root.Set("closest", f.function(func(js.Value, []js.Value) any { return root }))
	root.Set("isConnected", true)
	f.scope = &Scope{Root: Element{root}}
	for range 3 {
		node := js.Global().Get("Object").New()
		node.Set("tagName", "A")
		node.Set("fixtureOwner", root)
		node.Set("isConnected", true)
		node.Set("closest", f.function(func(this js.Value, _ []js.Value) any { return this.Get("fixtureOwner") }))
		f.links = append(f.links, Element{node})
	}
	status := js.Global().Get("Object").New()
	doc = js.ValueOf(map[string]any{"getElementById": f.function(func(js.Value, []js.Value) any { return status })})
	win = js.ValueOf(map[string]any{"console": map[string]any{"error": f.function(func(js.Value, []js.Value) any { return nil })}})
	t.Cleanup(func() {
		f.scope.dispose()
		for id := range linkStatusObservers {
			stopLinkStatusObserver(id)
		}
		for _, function := range f.functions {
			function.Release()
		}
		doc, win, committedLocation, currentURL = oldDoc, oldWin, oldLocation, oldURL
		linkStatusObservers, nextLinkStatusObserver = oldObservers, oldNext
		linkNavigationID, linkNavigationNode, linkNavigationWaiting = oldID, oldNode, oldWaiting
		moduleLinkCallbackCount, draftFrozen = oldCount, oldFrozen
	})
	return f
}
func (f *linkStatusFixture) function(fn func(js.Value, []js.Value) any) js.Func {
	callback := js.FuncOf(fn)
	f.functions = append(f.functions, callback)
	return callback
}
