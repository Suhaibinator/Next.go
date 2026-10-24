package nextgo

import (
	"encoding/json"
	"fmt"

	"github.com/Suhaibinator/Next.go/routeinfo"
)

// Selection is shared with browser components through the routeinfo package.
type Selection = routeinfo.Selection

// Selection reads the active logical segments for this layout's children or a
// named slot. Empty name means children. Named reads resolve/render the slot at
// most once, sharing the same loaders and authorization as Slot; metadata is not
// an authorization decision. Call only while the owning Layout executes.
func (c *Context) Selection(name string) (Selection, error) {
	if c.parallel == nil || c.parallel.owner < 0 || c.notFoundLayout {
		return Selection{}, ErrSlotScope
	}
	if name == "" || name == "children" {
		return c.primarySelection(c.parallel.owner)
	}
	for _, slot := range c.parallel.route.Slots {
		if slot.Name != name || slot.Layout != c.parallel.owner {
			continue
		}
		key, ok := scopedSlot(slot, c.Request.URL.EscapedPath(), c.slotParent)
		if !ok {
			return Selection{}, ErrSlotScope
		}
		if _, err := c.Slot(name); err != nil {
			return Selection{}, err
		}
		value, ok := c.parallel.infos[key]
		if !ok {
			return Selection{}, fmt.Errorf("selection was not resolved")
		}
		return value.Clone(), nil
	}
	return Selection{}, fmt.Errorf("%w: unknown selection %q", ErrSlotScope, name)
}
func (c *Context) SelectedSegments(name string) ([]string, error) {
	value, err := c.Selection(name)
	return value.Segments, err
}
func (c *Context) SelectedSegment(name string) (string, bool, error) {
	value, err := c.Selection(name)
	if err != nil {
		return "", false, err
	}
	first, ok := value.Segment()
	return first, ok, nil
}

func (state *parallelState) primaryAt(index int) bool {
	for _, frame := range state.frames {
		if frame.primary.Layout == index {
			return true
		}
	}
	return false
}
func (state *parallelState) selectionLeaf(defaultBase string, defaulted bool) (string, string) {
	if len(state.frames) > 0 {
		frame := state.frames[len(state.frames)-1]
		if frame.defaulted {
			prefix, _ := slotOwnerPrefix(frame.primary.Base, frame.selected.Request.URL.EscapedPath())
			return frame.primary.Base, prefix
		}
	} else if defaulted || state.current.slotDefault {
		if defaultBase == "" {
			defaultBase = state.route.Pattern
		}
		prefix, _ := slotOwnerPrefix(defaultBase, state.leaf.Request.URL.EscapedPath())
		return defaultBase, prefix
	}
	return state.route.Pattern, state.leaf.Request.URL.EscapedPath()
}
func (c *Context) primarySelection(index int) (Selection, error) {
	state := c.parallel
	base, key := "", ""
	for _, frame := range state.frames {
		if frame.primary.Layout == index {
			base = frame.primary.Base
			key = frame.key
			break
		}
	}
	if key == "" {
		r := state.route
		if index < 0 || len(r.LayoutBases) != len(r.Layouts) || len(r.LayoutIDs) != len(r.Layouts) || r.LayoutIDs[index] == "" {
			return Selection{}, fmt.Errorf("%w: primary selection requires layout identity/base", ErrSlotScope)
		}
		base = r.LayoutBases[index]
		var ok bool
		key, ok = scopedSlot(Slot{ID: r.LayoutIDs[index] + "#selection:children", Base: base}, c.Request.URL.EscapedPath(), c.slotParent)
		if !ok {
			return Selection{}, ErrSlotScope
		}
	}
	pattern, path := state.selectionLeaf("", false)
	segments, err := routeinfo.Segments(pattern, path, base)
	if err != nil {
		return Selection{}, err
	}
	result := Selection{Scope: key, Name: "children", Segments: segments}
	return result, result.Validate()
}
func selectionJSON(selection Selection) (string, error) {
	if err := selection.Validate(); err != nil {
		return "", err
	}
	data, err := json.Marshal(selection)
	return string(data), err
}
