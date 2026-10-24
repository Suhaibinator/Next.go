package nextgo

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/ui"
)

var slotOpen = regexp.MustCompile(`<nextgo-slot [^>]+>`)
var slotAttribute = regexp.MustCompile(`([a-z-]+)="([^"]*)"`)

func renderedSlots(content string) []map[string]string {
	result := []map[string]string{}
	for _, tag := range slotOpen.FindAllString(content, -1) {
		attrs := map[string]string{}
		for _, pair := range slotAttribute.FindAllStringSubmatch(tag, -1) {
			attrs[pair[1]] = html.UnescapeString(pair[2])
		}
		result = append(result, attrs)
	}
	return result
}
func nestedCarrier(app *App, content string) navigation.SlotState {
	state := navigation.SlotState{Configuration: app.configurationID, Selections: map[string]string{}}
	for _, attrs := range renderedSlots(content) {
		state.Selections[attrs["data-nextgo-slot"]] = attrs["data-nextgo-slot-url"]
	}
	return state
}
func nestedFixture(t *testing.T, loads *atomic.Int32) *App {
	base := "/dash/:account"
	leaf := Slot{ID: "shared-detail", Name: "detail", Base: base, Layout: 0, Default: static("detail default"), Routes: []Route{{Pattern: base + "/one", Page: func(c *Context) (Page, error) {
		loads.Add(1)
		if c.Request.Header.Get("X-Deny") == "yes" {
			return Page{}, ErrForbidden
		}
		return Page{Body: ui.Text("detail " + c.Params["account"] + " " + c.Query.Get("q") + " " + c.Request.Header.Get("X-Fresh"))}, nil
	}}}}
	inner := func(c *Context, body ui.Node) (ui.Node, error) {
		child, err := c.Slot("detail")
		return ui.Fragment(body, child), err
	}
	parent := Slot{ID: "panel", Name: "panel", Base: base, Layout: 0, Routes: []Route{{Pattern: base + "/one", Page: static("parent one"), Layouts: []LayoutFunc{inner}, LayoutIDs: []string{"inner"}, Slots: []Slot{leaf}, Primary: &PrimarySlot{ID: "panel.children", Base: base, Layout: 0}}}, DefaultRoute: &Route{Pattern: base, Page: static("parent default"), Layouts: []LayoutFunc{inner}, LayoutIDs: []string{"inner"}, Slots: []Slot{leaf}, Primary: &PrimarySlot{ID: "panel.children", Base: base, Layout: 0, Fallback: true}}}
	side := Slot{ID: "side", Name: "side", Base: base, Layout: 0, DefaultRoute: parent.DefaultRoute}
	outer := func(c *Context, body ui.Node) (ui.Node, error) {
		p, e := c.Slot("panel")
		if e != nil {
			return nil, e
		}
		s, e := c.Slot("side")
		return ui.Fragment(body, p, s), e
	}
	routes := []Route{}
	for _, suffix := range []string{"one", "two"} {
		routes = append(routes, Route{Pattern: base + "/" + suffix, Page: static(suffix), Layouts: []LayoutFunc{outer}, LayoutIDs: []string{"outer"}, Slots: []Slot{parent, side}})
	}
	return testApp(t, Config{Routes: routes, PreserveLayouts: true})
}
func TestNestedSlotsKeepParentAndTenantSelectionIsolation(t *testing.T) {
	var loads atomic.Int32
	app := nestedFixture(t, &loads)
	initial := request(app, "/dash/tenant/one?q=old", false)
	if initial.Code != 200 {
		t.Fatal(initial.Code, initial.Body.String())
	}
	markers := renderedSlots(initial.Body.String())
	if len(markers) != 6 {
		t.Fatal(markers)
	}
	roots := map[string]string{}
	children := map[string]string{}
	for _, attrs := range markers {
		if attrs["data-nextgo-slot-parent"] == "" {
			roots[attrs["data-nextgo-slot-name"]] = attrs["data-nextgo-slot"]
		} else {
			children[attrs["data-nextgo-slot-parent"]] = attrs["data-nextgo-slot"]
		}
	}
	if children[roots["panel"]] == "" || children[roots["side"]] == "" || children[roots["panel"]] == children[roots["side"]] {
		t.Fatal("child identity escaped parent scope", markers)
	}
	state := nestedCarrier(app, initial.Body.String())
	r := defaultNavigationRequest(t, "/dash/tenant/two?q=current", state)
	r.Header.Set("X-Fresh", "current")
	response := serveDefaultRequest(app, r)
	var envelope Envelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || response.Code != 200 || !strings.Contains(envelope.HTML, "detail tenant old current") {
		t.Fatal(response.Code, response.Body.String(), err)
	}
	for _, attrs := range renderedSlots(envelope.HTML) {
		if attrs["data-nextgo-slot-retain"] != "true" {
			t.Fatal("retained ancestor rematched child at its old URL", attrs)
		}
	}
	denied := slotNavigate(t, app, "/dash/tenant/two", state, true)
	if denied.Code != http.StatusForbidden || strings.Contains(denied.Body.String(), "parent one") || strings.Contains(denied.Body.String(), "detail tenant") {
		t.Fatal(denied.Code, denied.Body.String())
	}
	other := slotNavigate(t, app, "/dash/other/two", state, false)
	if other.Code != 200 || strings.Contains(other.Body.String(), "detail tenant") || !strings.Contains(other.Body.String(), "detail default") {
		t.Fatal(other.Code, other.Body.String())
	}
	if loads.Load() != 5 {
		t.Fatal("nested leaves were not reloaded", loads.Load())
	}
}
func TestNestedHashExcludesChildUpdatesAndEvaluatesDeferredWorkOnce(t *testing.T) {
	var calls atomic.Int32
	build := func(key, text string) map[string]string {
		child, err := renderSlotNode(context.Background(), key, "child", "/old", true, strings.Repeat("b", 64), ui.Defer("loaded", nil, func(context.Context) (ui.Node, error) { calls.Add(1); return ui.Text(text), nil }))
		if err != nil {
			t.Fatal(err)
		}
		parent, err := renderSlotNode(context.Background(), strings.Repeat("b", 64), "parent", "/old", true, "", ui.Fragment(ui.Text("parent-owned"), child))
		if err != nil {
			t.Fatal(err)
		}
		content, err := ui.HTML(parent)
		if err != nil {
			t.Fatal(err)
		}
		return renderedSlots(content)[0]
	}
	one := build(strings.Repeat("a", 64), "one")
	two := build(strings.Repeat("a", 64), "two")
	three := build(strings.Repeat("c", 64), "two")
	if one["data-nextgo-slot-hash"] != two["data-nextgo-slot-hash"] || one["data-nextgo-slot-hash"] == three["data-nextgo-slot-hash"] || calls.Load() != 3 {
		t.Fatal(one, two, three, calls.Load())
	}
}
func TestNestedSlotGraphRejectsCyclesAndComposedBounds(t *testing.T) {
	layout := func(_ *Context, n ui.Node) (ui.Node, error) { return n, nil }
	makeRoute := func() Route {
		return Route{Pattern: "/", Page: static("page"), Layouts: []LayoutFunc{layout}, LayoutIDs: []string{"owner"}}
	}
	cycle := makeRoute()
	cycle.Slots = []Slot{{ID: "cycle", Name: "cycle", Base: "/", Layout: 0, DefaultRoute: &cycle}}
	if _, err := New(Config{Routes: []Route{cycle}}); err == nil {
		t.Fatal("cycle accepted")
	}
	chain := makeRoute()
	for i := 0; i < navigation.MaxSlotDepth+1; i++ {
		child := chain
		chain = makeRoute()
		chain.Slots = []Slot{{ID: fmt.Sprint("node", i), Name: "child", Base: "/", Layout: 0, DefaultRoute: &child}}
	}
	if _, err := New(Config{Routes: []Route{chain}}); err == nil {
		t.Fatal("excessive depth accepted")
	}
	wide := makeRoute()
	branch := makeRoute()
	for i := 0; i < 8; i++ {
		branch.Slots = append(branch.Slots, Slot{ID: fmt.Sprint("leaf", i), Name: fmt.Sprint("leaf", i), Base: "/", Layout: 0, Default: static("leaf")})
	}
	wide.Slots = []Slot{{ID: "a", Name: "a", Base: "/", Layout: 0, DefaultRoute: &branch}, {ID: "b", Name: "b", Base: "/", Layout: 0, DefaultRoute: &branch}}
	if _, err := New(Config{Routes: []Route{wide}}); err == nil {
		t.Fatal("composed count exceeds the document carrier bound")
	}
}
