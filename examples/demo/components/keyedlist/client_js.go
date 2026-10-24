//go:build js && wasm

package keyedlist

import (
	"encoding/json"
	"fmt"
	"github.com/Suhaibinator/Next.go/client"
	"github.com/Suhaibinator/Next.go/ui"
	"strconv"
)

func Mount(scope *client.Scope, raw json.RawMessage) error {
	p, err := client.DecodeProps[Props](raw)
	if err != nil {
		return err
	}
	host, err := scope.Require("[data-rows]")
	if err != nil {
		return err
	}
	status, err := scope.Require("[data-list-status]")
	if err != nil {
		return err
	}
	field, err := scope.Require("[data-new-label]")
	if err != nil {
		return err
	}
	items := append([]Item(nil), p.Items...)
	active, cleaned, nextID := 0, 0, 0
	stats := func() {
		scope.Root.Attribute("data-active-rows", strconv.Itoa(active))
		scope.Root.Attribute("data-cleaned-rows", strconv.Itoa(cleaned))
	}
	scope.Cleanup(func() { scope.Root.Attribute("data-list-closed", "true") })
	var list *client.KeyedList[Item]
	apply := func(next []Item) bool {
		if err := list.Set(next); err != nil {
			status.Text(err.Error())
			return false
		}
		items = next
		status.Text("Updated from Go")
		return true
	}
	badKey := ""
	list, err = client.HydrateKeyed(scope, host, items, client.KeyedOptions[Item]{MaxItems: 8, Key: func(item Item) string { return item.ID }, View: func(item Item) ui.Node {
		if item.ID == badKey {
			return ui.Keyed("li", item.ID, nil, ui.El("script", nil, ui.Text("unsafe")))
		}
		return row(item, p.Table)
	}, Bind: func(row *client.Scope, item Item) (func(Item), error) {
		label, err := row.Require("[data-label]")
		if err != nil {
			return nil, err
		}
		edit, err := row.Require("[data-edit]")
		if err != nil {
			return nil, err
		}
		output, err := row.Require("[data-clicks]")
		if err != nil {
			return nil, err
		}
		click, err := row.Require("[data-click]")
		if err != nil {
			return nil, err
		}
		remove, err := row.Require("[data-remove]")
		if err != nil {
			return nil, err
		}
		if label.Value.Get("textContent").String() != item.Label {
			return nil, fmt.Errorf("list row markup differs from props")
		}
		current := item
		counts := client.NewState(0)
		row.Cleanup(counts.Subscribe(func(n int) { output.Text(strconv.Itoa(n)) }))
		active++
		stats()
		row.Cleanup(func() { active--; cleaned++; stats() })
		// Preserve edits made while WASM was loading rather than replacing them.
		initialText := edit.Value.Get("value").String()
		if initialText != item.Label {
			current.Label = initialText
			label.Text(initialText)
			for i := range items {
				if items[i].ID == item.ID {
					items[i].Label = initialText
				}
			}
		}
		row.On(click, "click", func(client.Event) { counts.Set(counts.Get() + 1) })
		row.On(edit, "input", func(event client.Event) {
			current.Label = event.InputValue()
			label.Text(current.Label)
			for i := range items {
				if items[i].ID == current.ID {
					items[i].Label = current.Label
				}
			}
		})
		row.On(remove, "click", func(client.Event) {
			next := []Item{}
			for _, value := range items {
				if value.ID != current.ID {
					next = append(next, value)
				}
			}
			apply(next)
		})
		click.Property("disabled", false)
		remove.Property("disabled", false)
		if p.BindPanic && item.ID == "b" {
			panic("synthetic binding failure")
		}
		return func(value Item) {
			if p.UpdatePanic {
				panic("synthetic update failure")
			}
			current = value
			label.Text(value.Label)
			if edit.Value.Get("value").String() != value.Label {
				edit.Property("value", value.Label)
			}
		}, nil
	}})
	if err != nil {
		return err
	}
	controls := []struct {
		selector string
		run      func()
	}{
		{"[data-add]", func() {
			nextID++
			text := field.Value.Get("value").String()
			if text == "" {
				text = "Added in Go"
			}
			if apply(append(append([]Item(nil), items...), Item{ID: fmt.Sprintf("new-%d", nextID), Label: text})) {
				field.Property("value", "")
			}
		}},
		{"[data-update]", func() {
			if len(items) == 0 {
				return
			}
			next := append([]Item(nil), items...)
			next[0].Label = "Updated from collection"
			apply(next)
		}},
		{"[data-reverse]", func() {
			next := append([]Item(nil), items...)
			for i, j := 0, len(next)-1; i < j; i, j = i+1, j-1 {
				next[i], next[j] = next[j], next[i]
			}
			apply(next)
		}},
		{"[data-rotate]", func() {
			if len(items) < 2 {
				return
			}
			next := append(append([]Item(nil), items[1:]...), items[0])
			apply(next)
		}},
		{"[data-invalid]", func() {
			if len(items) > 0 {
				apply(append(append([]Item(nil), items...), items[0]))
			}
		}},
		{"[data-fail]", func() {
			nextID++
			badKey = fmt.Sprintf("bad-%d", nextID)
			apply(append(append([]Item(nil), items...), Item{ID: "prepared", Label: "Prepared but never attached"}, Item{ID: badKey, Label: "Rejected"}))
			badKey = ""
		}},
		{"[data-limit]", func() {
			next := make([]Item, 9)
			for i := range next {
				next[i] = Item{ID: fmt.Sprint(i)}
			}
			apply(next)
		}},
	}
	for _, control := range controls {
		button, err := scope.Require(control.selector)
		if err != nil {
			return err
		}
		run := control.run
		scope.On(button, "click", func(client.Event) { run() })
		button.Property("disabled", false)
	}
	status.Text("Ready in Go")
	stats()
	return nil
}
