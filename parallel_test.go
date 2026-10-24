package nextgo

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/ui"
)

func parallelFixture(t *testing.T, loads *atomic.Int32) *App {
	layout := func(c *Context, body ui.Node) (ui.Node, error) {
		panel, err := c.Slot("panel")
		if err != nil {
			return nil, err
		}
		return ui.El("main", nil, body, ui.El("aside", nil, panel)), nil
	}
	slot := Slot{ID: "dashboard.panel", Name: "panel", Base: "/dash/:account", Layout: 0, Default: func(c *Context) (Page, error) { return Page{Body: ui.Text("default " + c.Params["account"])}, nil }, Routes: []Route{{Pattern: "/dash/:account/chart", Page: func(c *Context) (Page, error) {
		loads.Add(1)
		if c.Request.Header.Get("X-Deny") == "yes" {
			return Page{}, ErrForbidden
		}
		return Page{Body: ui.Text("chart " + c.Params["account"] + " " + c.Query.Get("q"))}, nil
	}}}}
	return testApp(t, Config{BuildID: "parallel", PreserveLayouts: true, Routes: []Route{{Pattern: "/dash/:account/chart", Page: static("Chart document"), Layouts: []LayoutFunc{layout}, LayoutIDs: []string{"dashboard"}, Slots: []Slot{slot}}, {Pattern: "/dash/:account/settings", Page: static("Settings document"), Layouts: []LayoutFunc{layout}, LayoutIDs: []string{"dashboard"}, Slots: []Slot{slot}}}})
}
func slotNavigate(t *testing.T, app *App, path string, state navigation.SlotState, deny bool) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("GET", path, nil)
	r.Header.Set("X-Nextgo-Navigate", "1")
	if state.Configuration != "" {
		encoded, _ := json.Marshal(state)
		r.Header.Set(navigation.SlotsHeader, string(encoded))
	}
	if deny {
		r.Header.Set("X-Deny", "yes")
	}
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	return w
}
func TestParallelServerSelectionDefaultsAndAuthenticatedRetention(t *testing.T) {
	var loads atomic.Int32
	app := parallelFixture(t, &loads)
	direct := request(app, "/dash/one/settings", false)
	if direct.Code != 200 || !strings.Contains(direct.Body.String(), "default one") {
		t.Fatal(direct.Code, direct.Body.String())
	}
	key, _ := slotScope(app.routes[0].Slots[0], "/dash/one/settings")
	state := navigation.SlotState{Configuration: app.configurationID, Selections: map[string]string{key: "/dash/one/chart?q=old"}}
	response := slotNavigate(t, app, "/dash/one/settings", state, false)
	var envelope Envelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || response.Code != 200 || !strings.Contains(envelope.HTML, "chart one old") || !strings.Contains(envelope.HTML, `data-nextgo-slot-retain="true"`) {
		t.Fatal(response.Code, response.Body.String(), err)
	}
	if loads.Load() != 1 {
		t.Fatal("retained branch was not reloaded", loads.Load())
	}
	denied := slotNavigate(t, app, "/dash/one/settings", state, true)
	if denied.Code != 403 || strings.Contains(denied.Body.String(), "chart one old") {
		t.Fatal("retention bypassed authorization", denied.Code, denied.Body.String())
	}
	other := slotNavigate(t, app, "/dash/two/settings", state, false)
	if other.Code != 200 || !strings.Contains(other.Body.String(), "default two") || strings.Contains(other.Body.String(), "chart one") {
		t.Fatal("slot carrier crossed owner parameters", other.Code, other.Body.String())
	}
	if response.Header().Get("X-Nextgo-Prefetch") != "" {
		t.Fatal("stateful page became reusable")
	}
	if _, err := app.StaticExportConfig(); err != nil {
		t.Fatal("parallel routes must not block publication of unrelated selected pages", err)
	}
}
func TestParallelMutationAndCarrierBounds(t *testing.T) {
	var loads atomic.Int32
	app := parallelFixture(t, &loads)
	bad := slotNavigate(t, app, "/dash/one/settings", navigation.SlotState{Configuration: strings.Repeat("a", 64)}, false)
	if bad.Code != 400 {
		t.Fatal(bad.Code)
	}
	app.routes[0].Slots[0].Routes[0].Page = func(c *Context) (Page, error) {
		_ = c.SetHeader("X-Unavailable", "value")
		return Page{Body: ui.Text("must not render")}, nil
	}
	response := request(app, "/dash/one/chart", false)
	if response.Code != 500 || strings.Contains(response.Body.String(), "must not render") || response.Header().Get("X-Unavailable") != "" {
		t.Fatal(response.Code, response.Body.String())
	}
}
func TestParallelScopeValidation(t *testing.T) {
	slot := Slot{ID: "panel", Name: "panel", Base: "/dash", Layout: 0, Routes: []Route{{Pattern: "/dash", Page: static("panel")}}}
	if _, err := New(Config{Routes: []Route{{Pattern: "/dash", Page: static("primary"), Slots: []Slot{slot}}}}); err == nil {
		t.Fatal("slot has no owning layout")
	}
	context := &Context{}
	if _, err := context.Slot("panel"); err != ErrSlotScope {
		t.Fatal(err)
	}
}

func TestParallelRootLayoutDoesNotBreakGlobalNotFoundComposition(t *testing.T) {
	layout := func(c *Context, body ui.Node) (ui.Node, error) {
		slot, err := c.Slot("panel")
		if err != nil {
			return nil, err
		}
		return ui.Fragment(body, slot), nil
	}
	app := testApp(t, Config{NotFound: func(c *Context) (Page, error) {
		body, err := layout(c, ui.Text("custom missing"))
		return Page{Body: body}, err
	}, Routes: []Route{{Pattern: "/", Page: static("home"), Layouts: []LayoutFunc{layout}, Slots: []Slot{{ID: "root.panel", Name: "panel", Base: "/", Layout: 0, Default: static("default panel")}}}}})
	response := request(app, "/missing", false)
	if response.Code != 404 || !strings.Contains(response.Body.String(), "custom missing") || strings.Contains(response.Body.String(), "default panel") {
		t.Fatal(response.Code, response.Body.String())
	}
}

func primaryFixture(t *testing.T, withDefault bool, loads *atomic.Int32) *App {
	owner := func(c *Context, body ui.Node) (ui.Node, error) {
		panel, err := c.Slot("panel")
		if err != nil {
			return nil, err
		}
		return ui.El("main", nil, ui.El("header", nil, ui.Text("outer "+c.Request.URL.RequestURI())), body, panel), nil
	}
	inner := func(c *Context, body ui.Node) (ui.Node, error) {
		return ui.El("article", nil, ui.Text("inner "+c.Request.URL.RequestURI()), body), nil
	}
	page := func(c *Context) (Page, error) {
		loads.Add(1)
		if c.Request.Header.Get("X-Deny") == "yes" {
			return Page{}, ErrForbidden
		}
		if err := c.SetHeader("X-Primary-Page", c.Request.URL.RequestURI()); err != nil {
			return Page{}, err
		}
		return Page{Title: "Retained " + c.Query.Get("q"), Body: ui.Text("primary " + c.Params["account"] + " " + c.Query.Get("q") + " " + c.Request.Header.Get("X-Fresh"))}, nil
	}
	fallback := func(*Context) (Page, error) { return Page{}, ErrNotFound }
	if withDefault {
		fallback = func(c *Context) (Page, error) {
			return Page{Title: "Primary default", Body: ui.Text("primary default " + c.Params["account"])}, nil
		}
	}
	slot := Slot{ID: "dash.panel", Name: "panel", Base: "/dash/:account", Layout: 0, Default: static("panel default"), Routes: []Route{{Pattern: "/dash/:account/only", Page: static("panel only")}}}
	return testApp(t, Config{BuildID: fmt.Sprint("primary-", withDefault), PreserveLayouts: true, Routes: []Route{
		{Pattern: "/dash/:account/main", Page: page, Layouts: []LayoutFunc{owner, inner}, LayoutIDs: []string{"owner", "inner"}, Slots: []Slot{slot}, Primary: &PrimarySlot{ID: "dash.children", Base: "/dash/:account", Layout: 0}},
		{Pattern: "/dash/:account/only", Page: fallback, Layouts: []LayoutFunc{owner}, LayoutIDs: []string{"owner"}, Slots: []Slot{slot}, Primary: &PrimarySlot{ID: "dash.children", Base: "/dash/:account", Layout: 0, Fallback: true}},
	}})
}
func TestPrimarySlotDefaultAndRevalidatedRetention(t *testing.T) {
	for _, withDefault := range []bool{false, true} {
		t.Run(fmt.Sprint(withDefault), func(t *testing.T) {
			var loads atomic.Int32
			app := primaryFixture(t, withDefault, &loads)
			direct := request(app, "/dash/one/only", false)
			want := 404
			if withDefault {
				want = 200
			}
			if direct.Code != want || withDefault && !strings.Contains(direct.Body.String(), "primary default one") {
				t.Fatal(direct.Code, direct.Body.String())
			}
			key, _ := slotScope(Slot{ID: "dash.children", Base: "/dash/:account"}, "/dash/one/main")
			state := navigation.SlotState{Configuration: app.configurationID, Selections: map[string]string{key: "/dash/one/main?q=old"}}
			r := httptest.NewRequest("GET", "/dash/one/only?q=new", nil)
			r.Header.Set("X-Nextgo-Navigate", "1")
			encoded, _ := json.Marshal(state)
			r.Header.Set(navigation.SlotsHeader, string(encoded))
			r.Header.Set("X-Fresh", "current")
			w := httptest.NewRecorder()
			app.ServeHTTP(w, r)
			var envelope Envelope
			if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || w.Code != 200 || !strings.Contains(envelope.HTML, "primary one old current") || !strings.Contains(envelope.HTML, "outer /dash/one/only?q=new") || !strings.Contains(envelope.HTML, "inner /dash/one/main?q=old") || !strings.Contains(envelope.HTML, `data-nextgo-slot-retain="true"`) || w.Header().Get("X-Primary-Page") != "/dash/one/main?q=old" || envelope.Title != "Retained old" {
				t.Fatal(w.Code, w.Body.String(), err)
			}
			if loads.Load() != 1 {
				t.Fatal("page not revalidated", loads.Load())
			}
			denied := slotNavigate(t, app, "/dash/one/only", state, true)
			if denied.Code != 403 || strings.Contains(denied.Body.String(), "primary one") {
				t.Fatal(denied.Code, denied.Body.String())
			}
			other := slotNavigate(t, app, "/dash/two/only", state, false)
			if other.Code != want || strings.Contains(other.Body.String(), "primary one") {
				t.Fatal(other.Code, other.Body.String())
			}
			matched := slotNavigate(t, app, "/dash/one/main?q=new", state, false)
			if matched.Code != 200 || !strings.Contains(matched.Body.String(), "Retained new") || strings.Contains(matched.Body.String(), `data-nextgo-slot-retain=\"true\"`) {
				t.Fatal(matched.Code, matched.Body.String())
			}
		})
	}
}
func TestPrimarySlotRegistrationBounds(t *testing.T) {
	for _, p := range []PrimarySlot{{ID: "children", Base: "/other", Layout: 0}, {ID: "children", Base: "/dash", Layout: 2}, {ID: "children", Base: "/*all", Layout: 0}} {
		_, err := New(Config{Routes: []Route{{Pattern: "/dash", Page: static("page"), Primary: &p, Layouts: []LayoutFunc{func(_ *Context, n ui.Node) (ui.Node, error) { return n, nil }}}}})
		if err == nil {
			t.Fatal("invalid primary slot accepted", p)
		}
	}
}
