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

// A parser can expose an opening template and partial content before receiving
// its closing tag. Only its next outside element marker proves it complete.
func completedStreamTemplate(template js.Value) (js.Value, bool) {
	id := template.Call("getAttribute", "data-nextgo-chunk").String()
	marker := template.Get("nextElementSibling")
	if id == "" || marker.IsNull() || marker.IsUndefined() || marker.Get("nodeType").Int() != 1 || marker.Get("tagName").String() != "SPAN" || !marker.Call("hasAttribute", "hidden").Bool() {
		return js.Undefined(), false
	}
	ready := marker.Call("getAttribute", "data-nextgo-chunk-ready")
	if ready.IsNull() || ready.String() != id {
		return js.Undefined(), false
	}
	return marker, true
}

func startStreaming() {
	streamingActive = true
	if err := startInitialMetadata(); err != nil {
		report("Invalid initial page metadata.")
	}
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
	stopInitialMetadata()
	applyStreamChunks()
}

func applyStreamChunks() {
	applyMetadataCarriers()
	templates := doc.Call("querySelectorAll", "template[data-nextgo-chunk]")
	for i := 0; i < templates.Length(); i++ {
		template := templates.Index(i)
		if !streamingActive {
			template.Call("remove")
			continue
		}
		marker, complete := completedStreamTemplate(template)
		if !complete {
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
			marker.Call("remove")
			hydrate()
			break
		}
	}
	if !streamingActive {
		markers := doc.Call("querySelectorAll", "[data-nextgo-chunk-ready]")
		for i := 0; i < markers.Length(); i++ {
			markers.Index(i).Call("remove")
		}
	}
}
