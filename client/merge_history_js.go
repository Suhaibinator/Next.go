//go:build js && wasm

package client

import "syscall/js"

type mergeEntryFocus struct {
	entry, url string
	scope      *Scope
	instance   uint64
	node       js.Value
	start, end int
	direction  string
	selection  bool
	scroll     []retainedScroll
}

var mergeEntryHistory = map[string]*mergeEntryFocus{}
var mergeEntryOrder []string

const mergeEntryLimit = 128
const mergeScrollReferences = 128

func mergeEntryKey(state js.Value, target string) string {
	if _, ok := trackedHistory(state, target); !ok {
		return ""
	}
	key := state.Get("nextgoEntry")
	if key.Type() != js.TypeString {
		return ""
	}
	return key.String()
}
func saveMergeHistory(state js.Value, target string) {
	current := mergeCurrent
	if current == nil || current.revoked {
		return
	}
	key := mergeEntryKey(state, target)
	if key == "" {
		return
	}
	entry := &mergeEntryFocus{entry: key, url: target, node: js.Undefined()}
	active := doc.Get("activeElement")
	for _, record := range current.boundaries {
		if !record.valid || record.scope == nil || !record.scope.propsLive() {
			continue
		}
		if !active.IsNull() && !active.IsUndefined() && record.root.Call("contains", active).Bool() {
			entry.scope = record.scope
			entry.instance = record.instance
			entry.node = active
			if active.Get("selectionStart").Type() == js.TypeNumber && active.Get("selectionEnd").Type() == js.TypeNumber {
				entry.selection = true
				entry.start = active.Get("selectionStart").Int()
				entry.end = active.Get("selectionEnd").Int()
				entry.direction = active.Get("selectionDirection").String()
			}
		}
		if _, err := mergeBoundedNodes(record.root, retainedViewNodes); err != nil {
			continue
		}
		nodes := record.root.Call("querySelectorAll", "*")
		for i := -1; i < nodes.Length() && len(entry.scroll) < mergeScrollReferences; i++ {
			node := record.root
			if i >= 0 {
				node = nodes.Index(i)
			}
			x, y := node.Get("scrollLeft"), node.Get("scrollTop")
			if x.Type() == js.TypeNumber && y.Type() == js.TypeNumber && (x.Float() != 0 || y.Float() != 0) {
				entry.scroll = append(entry.scroll, retainedScroll{node: node, x: x.Float(), y: y.Float()})
			}
		}
	}
	if _, exists := mergeEntryHistory[key]; !exists {
		mergeEntryOrder = append(mergeEntryOrder, key)
	}
	mergeEntryHistory[key] = entry
	total := 0
	for _, item := range mergeEntryHistory {
		total += len(item.scroll)
	}
	for len(mergeEntryOrder) > mergeEntryLimit || total > mergeScrollReferences {
		if len(mergeEntryOrder) == 0 {
			break
		}
		old := mergeEntryOrder[0]
		mergeEntryOrder = mergeEntryOrder[1:]
		if item := mergeEntryHistory[old]; item != nil {
			total -= len(item.scroll)
		}
		delete(mergeEntryHistory, old)
	}
}
func restoreMergeHistory(pop bool, state js.Value, refresh bool) {
	current := mergeCurrent
	if !pop || refresh || current == nil || current.revoked {
		return
	}
	key := mergeEntryKey(state, currentURL)
	entry := mergeEntryHistory[key]
	if entry == nil || entry.url != currentURL {
		return
	}
	for _, position := range entry.scroll {
		if position.node.Get("isConnected").Bool() {
			position.node.Set("scrollLeft", position.x)
			position.node.Set("scrollTop", position.y)
		}
	}
	if entry.scope == nil || !entry.scope.propsLive() || entry.scope.mergeInstance() != entry.instance || entry.node.IsUndefined() || !entry.node.Get("isConnected").Bool() {
		return
	}
	valid := false
	for _, boundary := range current.boundaries {
		if boundary.valid && boundary.scope == entry.scope && boundary.instance == entry.instance && boundary.root.Call("contains", entry.node).Bool() {
			valid = true
			break
		}
	}
	if !valid {
		return
	}
	if !doc.Get("activeElement").Equal(entry.node) {
		entry.node.Call("focus", map[string]any{"preventScroll": true})
	}
	if entry.selection && entry.node.Get("setSelectionRange").Type() == js.TypeFunction {
		start, end, direction := entry.node.Get("selectionStart"), entry.node.Get("selectionEnd"), entry.node.Get("selectionDirection")
		// Connected ClientView controls preserve their native selection. Match the
		// legacy retained path: writing an identical selection can schedule native
		// caret scrolling after synchronous document/container restoration.
		unchanged := start.Type() == js.TypeNumber && end.Type() == js.TypeNumber && direction.Type() == js.TypeString && start.Int() == entry.start && end.Int() == entry.end && direction.String() == entry.direction
		if !unchanged {
			entry.node.Call("setSelectionRange", entry.start, entry.end, entry.direction)
		}
	}
}
func retireMergeHistory(current *mergeDocument) {
	if current == nil {
		return
	}
	keep := mergeEntryOrder[:0]
	for _, key := range mergeEntryOrder {
		entry := mergeEntryHistory[key]
		valid := entry != nil && entry.scope == nil
		if entry != nil && entry.scope != nil {
			for _, boundary := range current.boundaries {
				if boundary.scope == entry.scope && boundary.instance == entry.instance && boundary.valid {
					valid = true
					break
				}
			}
		}
		if valid {
			positions := entry.scroll[:0]
			for _, position := range entry.scroll {
				live := false
				if position.node.Get("isConnected").Bool() {
					for _, boundary := range current.boundaries {
						if boundary.valid && boundary.root.Call("contains", position.node).Bool() {
							live = true
							break
						}
					}
				}
				if live {
					positions = append(positions, position)
				}
			}
			entry.scroll = positions
			keep = append(keep, key)
		} else {
			delete(mergeEntryHistory, key)
		}
	}
	mergeEntryOrder = keep
}
