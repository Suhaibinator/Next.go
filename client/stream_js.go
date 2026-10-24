//go:build js && wasm

package client

import (
	"fmt"
	"syscall/js"
)

// Initial HTTP streams can append inert templates after WASM has started. The
// observer moves their existing nodes into the matching boundary; Go remains
// responsible for hydration and island cleanup.
var streamingActive bool

func startStreaming() {
	streamingActive = true
	applyStreamChunks()
	callback := js.FuncOf(func(_ js.Value, _ []js.Value) any {
		defer func() {
			if v := recover(); v != nil {
				report(fmt.Sprintf("streaming error: %v", v))
			}
		}()
		applyStreamChunks()
		return nil
	})
	globalFunctions = append(globalFunctions, callback)
	observer := win.Get("MutationObserver").New(callback)
	observer.Call("observe", doc.Get("body"), map[string]any{"childList": true, "subtree": true})
}

func stopStreaming() {
	streamingActive = false
	applyStreamChunks()
}

func applyStreamChunks() {
	templates := doc.Call("querySelectorAll", "template[data-nextgo-chunk]")
	for i := 0; i < templates.Length(); i++ {
		template := templates.Index(i)
		if !streamingActive {
			template.Call("remove")
			continue
		}
		id := template.Call("getAttribute", "data-nextgo-chunk").String()
		boundaries := doc.Call("getElementById", "nextgo-app").Call("querySelectorAll", "[data-nextgo-boundary]")
		for j := 0; j < boundaries.Length(); j++ {
			boundary := boundaries.Index(j)
			if id == "" || boundary.Call("getAttribute", "data-nextgo-boundary").String() != id {
				continue
			}
			disposeWithin(boundary)
			boundary.Call("replaceChildren", template.Get("content"))
			boundary.Call("removeAttribute", "aria-busy")
			template.Call("remove")
			hydrate()
			break
		}
	}
}
