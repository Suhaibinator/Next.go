package nextgo

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Suhaibinator/Next.go/ui"
)

func chainFixture(t *testing.T) *App {
	base := "/dash/:tenant"
	a := PrimarySlot{ID: "a.children", Base: base, Layout: 0}
	b := PrimarySlot{ID: "b.children", Base: base + "/reports", Layout: 1}
	outer := func(c *Context, body ui.Node) (ui.Node, error) {
		side, err := c.Slot("aside")
		return ui.El("main", nil, ui.Text("outer "+c.Request.URL.RequestURI()), body, side), err
	}
	inner := func(c *Context, body ui.Node) (ui.Node, error) {
		detail, err := c.Slot("detail")
		return ui.El("section", nil, ui.Text("inner layout "+c.Request.URL.RequestURI()), body, detail), err
	}
	aside := Slot{ID: "a.aside", Name: "aside", Base: base, Layout: 0, Default: static("aside default"), Routes: []Route{{Pattern: base + "/aside", Page: static("aside match")}}}
	detail := Slot{ID: "b.detail", Name: "detail", Base: b.Base, Layout: 1, Default: static("detail default"), Routes: []Route{{Pattern: b.Base + "/summary", Page: static("detail summary")}}}
	page := func(c *Context) (Page, error) {
		if c.Request.Header.Get("X-Deny") == "yes" {
			return Page{}, ErrForbidden
		}
		return Page{Title: "Inner page", Body: ui.Text("inner page " + c.Params["tenant"] + " " + c.Query.Get("q") + " " + c.Request.Header.Get("X-Fresh"))}, nil
	}
	fallbackB := b
	fallbackB.Fallback = true
	fallbackA := a
	fallbackA.Fallback = true
	return testApp(t, Config{PreserveLayouts: true, Routes: []Route{
		{Pattern: b.Base + "/page", Page: page, Layouts: []LayoutFunc{outer, inner}, LayoutIDs: []string{"a", "b"}, Primaries: []PrimarySlot{a, b}, Slots: []Slot{aside, detail}},
		{Pattern: b.Base + "/summary", Page: static("inner default"), Layouts: []LayoutFunc{outer, inner}, LayoutIDs: []string{"a", "b"}, Primaries: []PrimarySlot{a, fallbackB}, Slots: []Slot{aside, detail}},
		{Pattern: base + "/aside", Page: static("outer default"), Layouts: []LayoutFunc{outer}, LayoutIDs: []string{"a"}, Primary: &fallbackA, Slots: []Slot{aside}},
	}})
}
func TestPrimaryChainSelectsAndReauthorizesIndependentAncestorOutlets(t *testing.T) {
	app := chainFixture(t)
	direct := request(app, "/dash/one/reports/summary", false)
	if direct.Code != 200 || !strings.Contains(direct.Body.String(), "inner default") || strings.Contains(direct.Body.String(), "outer default") {
		t.Fatal(direct.Code, direct.Body.String())
	}
	initial := request(app, "/dash/one/reports/page?q=old", false)
	state := nestedCarrier(app, initial.Body.String())
	r := defaultNavigationRequest(t, "/dash/one/reports/summary?q=current", state)
	r.Header.Set("X-Fresh", "current")
	response := serveDefaultRequest(app, r)
	var envelope Envelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || response.Code != 200 || !strings.Contains(envelope.HTML, "inner page one old current") || !strings.Contains(envelope.HTML, "inner layout /dash/one/reports/summary?q=current") {
		t.Fatal(response.Code, response.Body.String(), err)
	}
	root, child := "", ""
	for _, marker := range renderedSlots(envelope.HTML) {
		if marker["data-nextgo-slot-name"] == "children" {
			if marker["data-nextgo-slot-parent"] == "" {
				root = marker["data-nextgo-slot"]
			} else {
				child = marker["data-nextgo-slot-parent"]
			}
		}
	}
	if root == "" || root != child {
		t.Fatal("primary chain lost parent scope", root, child, envelope.HTML)
	}
	state = nestedCarrier(app, envelope.HTML)
	r = defaultNavigationRequest(t, "/dash/one/aside", state)
	r.Header.Set("X-Fresh", "newer")
	retained := serveDefaultRequest(app, r)
	if err := json.Unmarshal(retained.Body.Bytes(), &envelope); err != nil || retained.Code != 200 || !strings.Contains(envelope.HTML, "inner page one old newer") || !strings.Contains(envelope.HTML, "outer /dash/one/aside") || !strings.Contains(envelope.HTML, "inner layout /dash/one/reports/summary?q=current") || !strings.Contains(envelope.HTML, "detail summary") {
		t.Fatal(retained.Code, retained.Body.String(), err)
	}
	denied := slotNavigate(t, app, "/dash/one/aside", state, true)
	if denied.Code != 403 || strings.Contains(denied.Body.String(), "inner page") {
		t.Fatal(denied.Code, denied.Body.String())
	}
	other := slotNavigate(t, app, "/dash/two/aside", state, false)
	if other.Code != 200 || strings.Contains(other.Body.String(), "inner page") || !strings.Contains(other.Body.String(), "outer default") {
		t.Fatal(other.Code, other.Body.String())
	}
}
func TestPrimaryChainRegistrationAndCompositionBounds(t *testing.T) {
	layout := func(_ *Context, n ui.Node) (ui.Node, error) { return n, nil }
	for _, chain := range [][]PrimarySlot{
		{{ID: "a", Base: "/", Layout: 1}, {ID: "b", Base: "/", Layout: 0}},
		{{ID: "a", Base: "/", Layout: 0}, {ID: "a", Base: "/", Layout: 1}},
		{{ID: "a", Base: "/", Layout: 0}, {ID: "b", Base: "/outside", Layout: 1}},
	} {
		_, err := New(Config{Routes: []Route{{Pattern: "/", Page: static("body"), Layouts: []LayoutFunc{layout, layout}, Primaries: chain}}})
		if err == nil {
			t.Fatal("invalid chain accepted", chain)
		}
	}
	_, err := New(Config{Routes: []Route{{Pattern: "/", Page: static("body"), Layouts: []LayoutFunc{layout}, Primary: &PrimarySlot{ID: "a", Base: "/", Layout: 0}, Primaries: []PrimarySlot{{ID: "a", Base: "/", Layout: 0}}}}})
	if err == nil {
		t.Fatal("two primary representations accepted")
	}
}
