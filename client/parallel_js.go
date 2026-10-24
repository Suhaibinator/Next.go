//go:build js && wasm

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"syscall/js"

	"github.com/Suhaibinator/Next.go/internal/navigation"
)

type slotHistoryKey struct{}

func slotNodes(root js.Value) (map[string]js.Value, error) {
	result := map[string]js.Value{}
	nodes := root.Call("querySelectorAll", "nextgo-slot[data-nextgo-slot]")
	if nodes.Length() > navigation.MaxSlots {
		return nil, fmt.Errorf("too many parallel slots")
	}
	for i := 0; i < nodes.Length(); i++ {
		node := nodes.Index(i)
		key := node.Call("getAttribute", "data-nextgo-slot").String()
		hash := node.Call("getAttribute", "data-nextgo-slot-hash").String()
		if !navigation.ValidConfigurationID(key) || !navigation.ValidConfigurationID(hash) {
			return nil, fmt.Errorf("invalid parallel slot marker")
		}
		if _, exists := result[key]; exists {
			return nil, fmt.Errorf("duplicate parallel slot")
		}
		retained := node.Call("getAttribute", "data-nextgo-slot-retain").String()
		if retained != "true" && retained != "false" {
			return nil, fmt.Errorf("invalid parallel retention marker")
		}
		state := navigation.SlotState{Configuration: documentConfiguration(), Selections: map[string]string{key: node.Call("getAttribute", "data-nextgo-slot-url").String()}}
		if err := state.Validate(); err != nil {
			return nil, err
		}
		parent := node.Get("parentElement").Call("closest", "nextgo-slot[data-nextgo-slot]")
		declared := slotParent(node)
		if parent.IsNull() {
			if declared != "" {
				return nil, fmt.Errorf("parallel slot has an absent parent")
			}
		} else if declared != parent.Call("getAttribute", "data-nextgo-slot").String() {
			return nil, fmt.Errorf("parallel slot parent mismatch")
		}
		depth := 1
		for !parent.IsNull() {
			depth++
			parent = parent.Get("parentElement").Call("closest", "nextgo-slot[data-nextgo-slot]")
		}
		if depth > navigation.MaxSlotDepth {
			return nil, fmt.Errorf("parallel slot depth bound exceeded")
		}
		result[key] = node
	}
	return result, nil
}
func slotParent(node js.Value) string {
	value := node.Call("getAttribute", "data-nextgo-slot-parent")
	if value.IsNull() {
		return ""
	}
	return value.String()
}
func currentSlotState() (string, error) {
	nodes, err := slotNodes(doc.Call("getElementById", "nextgo-app"))
	if err != nil {
		return "", err
	}
	state := navigation.SlotState{Configuration: documentConfiguration(), Selections: map[string]string{}}
	for key, node := range nodes {
		state.Selections[key] = node.Call("getAttribute", "data-nextgo-slot-url").String()
	}
	data, err := json.Marshal(state)
	if len(data) > navigation.MaxSlotStateBytes {
		return "", fmt.Errorf("parallel slot state exceeds byte bound")
	}
	return string(data), err
}
func slotRequestState(ctx context.Context) (string, error) {
	if saved, ok := ctx.Value(slotHistoryKey{}).(string); ok {
		if state, err := navigation.ParseSlotState(saved); err == nil && state.Configuration == documentConfiguration() {
			return saved, nil
		}
		// History can outlive an application deployment. Unknown ownership
		// must resolve through this build's deterministic defaults, not send
		// an incompatible carrier or retain the current entry's branches.
		data, err := json.Marshal(navigation.SlotState{Configuration: documentConfiguration(), Selections: map[string]string{}})
		return string(data), err
	}
	return currentSlotState()
}
func sameOwnedSlots(old, incoming js.Value) bool {
	own := func(layout js.Value) map[string]bool {
		result := map[string]bool{}
		id := layout.Call("getAttribute", "data-nextgo-layout").String()
		outlet := layoutOutlet(layout, id)
		nodes := layout.Call("querySelectorAll", "nextgo-slot[data-nextgo-slot]")
		for i := 0; i < nodes.Length(); i++ {
			node := nodes.Index(i)
			if node.Call("closest", "nextgo-layout[data-nextgo-layout]").Equal(layout) {
				result[node.Call("getAttribute", "data-nextgo-slot").String()] = !outlet.IsNull() && outlet.Call("contains", node).Bool()
			}
		}
		return result
	}
	a, b := own(old), own(incoming)
	if len(a) != len(b) {
		return false
	}
	for key, inside := range a {
		if other, ok := b[key]; !ok || inside != other {
			return false
		}
	}
	return true
}

func moveChildren(old, incoming js.Value) {
	fragment := doc.Call("createDocumentFragment")
	for incoming.Get("firstChild").Truthy() {
		fragment.Call("appendChild", incoming.Get("firstChild"))
	}
	old.Call("replaceChildren", fragment)
}

// Build the complete hierarchical plan before touching live DOM. Child roots
// move out of replacement regions before their parent effects are disposed.
type slotPatch struct {
	old, incoming, x, y  js.Value
	children             []slotPatch
	retain, move, remove bool
}

func reconcileSlots(root, parsed, oldRegion, incomingRegion js.Value) error {
	existing, err := slotNodes(root)
	if err != nil {
		return err
	}
	next, err := slotNodes(parsed)
	if err != nil {
		return err
	}
	contains := func(region, node js.Value) bool { return !region.IsNull() && region.Call("contains", node).Bool() }
	children := func(nodes map[string]js.Value, parent string) map[string]js.Value {
		result := map[string]js.Value{}
		for key, node := range nodes {
			if slotParent(node) == parent {
				result[key] = node
			}
		}
		return result
	}
	var plan func(string, js.Value, js.Value) ([]slotPatch, error)
	plan = func(parent string, x, y js.Value) ([]slotPatch, error) {
		oldNodes, newNodes := children(existing, parent), children(next, parent)
		patches := []slotPatch{}
		for key, old := range oldNodes {
			replacement, found := newNodes[key]
			if !found {
				if !contains(x, old) {
					patches = append(patches, slotPatch{old: old, remove: true})
				}
				continue
			}
			delete(newNodes, key)
			oldInside, newInside := contains(x, old), contains(y, replacement)
			if oldInside != newInside {
				return nil, fmt.Errorf("inconsistent parallel outlet ownership")
			}
			retained := replacement.Call("getAttribute", "data-nextgo-slot-retain").String() == "true" && old.Call("getAttribute", "data-nextgo-slot-hash").String() == replacement.Call("getAttribute", "data-nextgo-slot-hash").String() && old.Call("getAttribute", "data-nextgo-slot-url").String() == replacement.Call("getAttribute", "data-nextgo-slot-url").String()
			// A malformed envelope cannot add/remove children behind an equal hash.
			a, b := children(existing, key), children(next, key)
			if len(a) != len(b) {
				retained = false
			}
			for child := range a {
				if _, ok := b[child]; !ok {
					retained = false
				}
			}
			cx, cy := js.Null(), js.Null()
			if !retained {
				cx, cy = navigationRegion(old, replacement)
			}
			nested, err := plan(key, cx, cy)
			if err != nil {
				return nil, err
			}
			patches = append(patches, slotPatch{old: old, incoming: replacement, x: cx, y: cy, children: nested, retain: retained, move: newInside})
		}
		for _, node := range newNodes {
			if !contains(y, node) {
				return nil, fmt.Errorf("new parallel slot is outside its replacement outlet")
			}
		}
		return patches, nil
	}
	patches, err := plan("", oldRegion, incomingRegion)
	if err != nil {
		return err
	}
	// Dispose each changing branch's own effects before moving any sibling.
	// In particular, close native modals while their inert background is still
	// connected. WebKit can otherwise retain stale inertness on moved owners.
	// Nested slots have independent plans, so their retained islands stay live.
	var prepare func([]slotPatch)
	prepare = func(patches []slotPatch) {
		for _, p := range patches {
			prepare(p.children)
			region := p.x
			if p.remove {
				region = p.old
			} else if p.retain {
				continue
			}
			for _, scope := range mounted {
				if region.Call("contains", scope.Root.Value).Bool() && scope.Root.Value.Call("closest", "nextgo-slot[data-nextgo-slot]").Equal(p.old) {
					scope.dispose()
				}
			}
		}
	}
	prepare(patches)
	var apply func([]slotPatch)
	apply = func(patches []slotPatch) {
		for _, p := range patches {
			if p.remove {
				disposeWithin(p.old)
				p.old.Call("remove")
				continue
			}
			apply(p.children)
			if !p.retain {
				disposeWithin(p.x)
				moveChildren(p.x, p.y)
			}
			for _, name := range []string{"data-nextgo-slot-url", "data-nextgo-slot-hash", "data-nextgo-slot-retain", "data-nextgo-slot-name", "data-nextgo-slot-parent"} {
				p.old.Call("setAttribute", name, p.incoming.Call("getAttribute", name).String())
			}
			if p.move {
				p.incoming.Call("replaceWith", p.old)
			}
		}
	}
	apply(patches)
	return nil
}
