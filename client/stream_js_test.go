//go:build js && wasm

package client

import (
	"syscall/js"
	"testing"
)

func TestStreamCompletionRequiresAdjacentOutsideMarker(t *testing.T) {
	for _, tc := range []struct {
		name, id, markerID, tag string
		nodeType                int
		hidden, match           bool
	}{
		{"complete", "literal[id]", "literal[id]", "SPAN", 1, true, true},
		{"missing", "chunk", "chunk", "", 0, false, false},
		{"intervening-text", "chunk", "chunk", "", 3, false, false},
		{"wrong-id", "chunk", "different", "SPAN", 1, true, false},
		{"empty-id", "", "", "SPAN", 1, true, false},
		{"wrong-tag", "chunk", "chunk", "DIV", 1, true, false},
		{"visible-marker", "chunk", "chunk", "SPAN", 1, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			getID := js.FuncOf(func(js.Value, []js.Value) any { return tc.id })
			defer getID.Release()
			getReady := js.FuncOf(func(js.Value, []js.Value) any { return tc.markerID })
			defer getReady.Release()
			hasHidden := js.FuncOf(func(js.Value, []js.Value) any { return tc.hidden })
			defer hasHidden.Release()
			marker := js.Null()
			if tc.nodeType != 0 {
				marker = js.ValueOf(map[string]any{"nodeType": tc.nodeType, "tagName": tc.tag, "getAttribute": getReady, "hasAttribute": hasHidden})
			}
			template := js.ValueOf(map[string]any{"getAttribute": getID, "nextElementSibling": marker})
			got, ok := completedStreamTemplate(template)
			if ok != tc.match || ok && !got.Equal(marker) {
				t.Fatal("partial or mismatched chunk accepted", ok, tc.match)
			}
		})
	}
}
