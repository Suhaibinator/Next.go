package nextgo

import (
	"errors"
	"strings"
	"testing"

	"github.com/Suhaibinator/Next.go/ui"
)

func TestLayoutSelectionUsesMatchedParamsAndOwnsSnapshots(t *testing.T) {
	var observed Selection
	layout := func(c *Context, body ui.Node) (ui.Node, error) {
		value, err := c.Selection("")
		if err != nil {
			return nil, err
		}
		observed = value.Clone()
		value.Segments[0] = "mutated"
		parts, err := c.SelectedSegments("children")
		if err != nil {
			return nil, err
		}
		first, ok, err := c.SelectedSegment("children")
		if err != nil || !ok || first != c.Params["slug"] {
			t.Fatalf("first=%q ok=%v err=%v", first, ok, err)
		}
		return ui.Fragment(ui.Text(strings.Join(parts, "/")), body), nil
	}
	app := testApp(t, Config{Routes: []Route{{Pattern: "/posts/:slug", Page: static("body"), Layouts: []LayoutFunc{layout}, LayoutIDs: []string{"posts"}, LayoutBases: []string{"/posts"}}}})
	response := request(app, "/posts/caf%C3%A9?q=ignored", false)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "café") || strings.Contains(response.Body.String(), "mutated") || len(observed.Segments) != 1 || observed.Segments[0] != "café" {
		t.Fatal(response.Code, response.Body.String(), observed)
	}
	other := request(app, "/posts/other", false)
	if other.Code != 200 {
		t.Fatal(other.Code)
	}
	if (&Context{}).parallel != nil {
		t.Fatal("unexpected context")
	}
	if _, err := (&Context{}).Selection("children"); !errors.Is(err, ErrSlotScope) {
		t.Fatal(err)
	}
}

func TestNamedSelectionSharesSlotLoadAndTenantScope(t *testing.T) {
	loads := 0
	keys := []string{}
	layout := func(c *Context, body ui.Node) (ui.Node, error) {
		selected, err := c.Selection("panel")
		if err != nil {
			return nil, err
		}
		keys = append(keys, selected.Scope)
		if len(selected.Segments) != 1 || selected.Segments[0] != "view" {
			t.Fatal(selected)
		}
		panel, err := c.Slot("panel")
		if err != nil {
			return nil, err
		}
		if _, err := c.Selection("missing"); !errors.Is(err, ErrSlotScope) {
			t.Fatalf("unknown selection: %v", err)
		}
		return ui.Fragment(body, panel, ui.Text(strings.Join(selected.Segments, "/"))), nil
	}
	slot := Slot{ID: "panel", Name: "panel", Base: "/teams/:id", Layout: 0, Routes: []Route{{Pattern: "/teams/:id/view", Page: func(c *Context) (Page, error) {
		loads++
		if c.Query.Get("deny") == "yes" {
			return Page{}, ErrForbidden
		}
		return Page{Body: ui.Text("panel")}, nil
	}}}}
	app := testApp(t, Config{Routes: []Route{{Pattern: "/teams/:id/view", Page: static("body"), Layouts: []LayoutFunc{layout}, LayoutIDs: []string{"team"}, LayoutBases: []string{"/teams/:id"}, Slots: []Slot{slot}}}})
	for _, path := range []string{"/teams/one/view", "/teams/two/view"} {
		r := request(app, path, false)
		if r.Code != 200 {
			t.Fatal(r.Code, r.Body.String())
		}
	}
	if loads != 2 || len(keys) != 2 || keys[0] == keys[1] {
		t.Fatal(loads, keys)
	}
	denied := request(app, "/teams/one/view?deny=yes", false)
	if denied.Code != 403 || strings.Contains(denied.Body.String(), ">panel<") {
		t.Fatal(denied.Code, denied.Body.String())
	}
}
