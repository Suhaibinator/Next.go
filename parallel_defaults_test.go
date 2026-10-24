package nextgo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

func defaultRouteFixture(t *testing.T, metadataLoads *atomic.Int32) *App {
	t.Helper()
	owner := func(c *Context, body ui.Node) (ui.Node, error) {
		panel, err := c.Slot("panel")
		return ui.Fragment(body, panel), err
	}
	fallback := &Route{Pattern: "/dash/:account", Page: func(c *Context) (Page, error) {
		switch c.Query.Get("mode") {
		case "error":
			return Page{}, fmt.Errorf("private default loader detail")
		case "missing":
			return Page{}, ErrNotFound
		case "deny":
			return Page{}, ErrForbidden
		}
		return Page{Title: "Slot title must not replace primary", Body: ui.Text("default " + c.Params["account"] + " " + c.Query.Get("q") + " " + c.Request.Header.Get("X-Current"))}, nil
	}, Layouts: []LayoutFunc{func(c *Context, body ui.Node) (ui.Node, error) {
		if c.Query.Get("mode") == "poison" {
			_ = c.SetHeader("X-Slot-Control", "must not publish")
		}
		return ui.El("section", ui.Attrs{"data-default-layout": ""}, body), nil
	}, Template("default-template", "/dash/:account", func(_ *Context, body ui.Node) (ui.Node, error) {
		return ui.El("section", ui.Attrs{"data-default-template": ""}, body), nil
	})}, LayoutIDs: []string{"default-root", ""}, Metadata: []MetadataLayer{{Load: func(c *Context, _ seo.Metadata) (seo.Metadata, error) {
		metadataLoads.Add(1)
		if c.Request.Header.Get("X-Metadata-Deny") == "yes" {
			return seo.Metadata{}, ErrForbidden
		}
		return seo.Metadata{Description: "Slot metadata is evaluated but not merged into the document"}, nil
	}}}, NotFound: static("local missing"), Error: func(*Context, error) (Page, error) { return Page{Body: ui.Text("local default failure")}, nil }}
	slot := Slot{ID: "dash.panel", Name: "panel", Base: "/dash/:account", Layout: 0, DefaultRoute: fallback}
	return testApp(t, Config{PreserveLayouts: true, Forbidden: static("global denied"), NotFound: static("global missing"), Routes: []Route{{Pattern: "/dash/:account/view", Page: static("Primary"), Layouts: []LayoutFunc{owner}, LayoutIDs: []string{"owner"}, Slots: []Slot{slot}}}})
}

func TestNamedDefaultRouteInheritsWrappersAndRevalidatesSelectedInputs(t *testing.T) {
	var loads atomic.Int32
	app := defaultRouteFixture(t, &loads)
	direct := request(app, "/dash/one/view?q=new", false)
	text := direct.Body.String()
	if direct.Code != 200 || !strings.Contains(text, "default one new") || !strings.Contains(text, `data-default-layout=""`) || !strings.Contains(text, `data-nextgo-layout="template:default-template:/dash/one/view"`) || !strings.Contains(text, "<title>Primary</title>") || strings.Contains(text, "Slot title must not replace primary") {
		t.Fatal(direct.Code, text)
	}
	layout, template, body := strings.Index(text, "data-default-layout"), strings.Index(text, "data-default-template"), strings.Index(text, "default one new")
	if !(layout < template && template < body) {
		t.Fatal("wrong default wrapper order", text)
	}
	key, _ := slotScope(app.routes[0].Slots[0], "/dash/one/view")
	state := navigation.SlotState{Configuration: app.configurationID, Selections: map[string]string{key: "/dash/one/old?q=selected"}}
	r := defaultNavigationRequest(t, "/dash/one/view?q=current", state)
	r.Header.Set("X-Current", "fresh")
	response := serveDefaultRequest(app, r)
	var envelope Envelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || response.Code != 200 || !strings.Contains(envelope.HTML, "default one selected fresh") || !strings.Contains(envelope.HTML, `template:default-template:/dash/one/old`) || !strings.Contains(envelope.HTML, `data-nextgo-slot-retain="true"`) {
		t.Fatal(response.Code, response.Body.String(), err)
	}
	if loads.Load() != 2 {
		t.Fatal("default metadata was not evaluated on each request", loads.Load())
	}
	r = defaultNavigationRequest(t, "/dash/one/view", state)
	r.Header.Set("X-Metadata-Deny", "yes")
	denied := serveDefaultRequest(app, r)
	if denied.Code != 403 || strings.Contains(denied.Body.String(), "default one selected") || !strings.Contains(denied.Body.String(), "global denied") {
		t.Fatal(denied.Code, denied.Body.String())
	}
	other := slotNavigate(t, app, "/dash/two/view?q=current", state, false)
	if other.Code != 200 || strings.Contains(other.Body.String(), "selected") || !strings.Contains(other.Body.String(), "default two current") {
		t.Fatal(other.Code, other.Body.String())
	}
}

func TestNamedDefaultRouteLocalBoundariesAndResponseControls(t *testing.T) {
	for _, tc := range []struct {
		mode   string
		status int
		body   string
	}{{"error", 500, "local default failure"}, {"missing", 404, "local missing"}, {"deny", 403, "global denied"}, {"poison", 500, "Something went wrong"}} {
		t.Run(tc.mode, func(t *testing.T) {
			var loads atomic.Int32
			app := defaultRouteFixture(t, &loads)
			response := request(app, "/dash/one/view?mode="+tc.mode, false)
			text := response.Body.String()
			if response.Code != tc.status || !strings.Contains(text, tc.body) || strings.Contains(text, "private default loader detail") || response.Header().Get("X-Slot-Control") != "" {
				t.Fatal(response.Code, text)
			}
			if tc.mode == "error" || tc.mode == "missing" {
				if !strings.Contains(text, "data-default-layout") || !strings.Contains(text, "data-default-template") || !strings.Contains(text, "Primary") {
					t.Fatal("local boundary omitted wrappers or other branches", text)
				}
			}
		})
	}
}
func TestNamedDefaultRouteConfigurationValidationAndFreezing(t *testing.T) {
	owner := func(c *Context, n ui.Node) (ui.Node, error) {
		panel, e := c.Slot("panel")
		return ui.Fragment(n, panel), e
	}
	config := func(slot Slot) Config {
		return Config{Routes: []Route{{Pattern: "/dash/view", Page: static("primary"), Layouts: []LayoutFunc{owner}, LayoutIDs: []string{"owner"}, Slots: []Slot{slot}}}}
	}
	base := Slot{ID: "panel", Name: "panel", Base: "/dash", Layout: 0}
	for _, bad := range []Route{
		{Pattern: "/other", Page: static("wrong base")},
		{Pattern: "/dash"},
		{Pattern: "/dash", Page: static("p"), Cache: &CachePolicy{}},
		{Pattern: "/dash", Page: static("p"), Layouts: []LayoutFunc{nil}},
		{Pattern: "/dash", Page: static("p"), Metadata: []MetadataLayer{{}}},
		{Pattern: "/dash", Page: static("p"), Access: []AccessBoundary{{Forbidden: static("denied"), Layouts: 1}}},
		{Pattern: "/dash", Page: static("p"), LayoutIDs: []string{"missing wrapper"}},
	} {
		slot := base
		slot.DefaultRoute = &bad
		if app, err := New(config(slot)); err == nil {
			app.Close(context.Background())
			t.Fatal("invalid default accepted", bad)
		}
	}
	duplicate := base
	duplicate.Default = static("legacy")
	duplicate.DefaultRoute = &Route{Pattern: "/dash", Page: static("structured")}
	if app, err := New(config(duplicate)); err == nil {
		app.Close(context.Background())
		t.Fatal("two default registrations accepted")
	}
	fallback := &Route{Pattern: "/dash", Page: static("original"), Layouts: []LayoutFunc{func(_ *Context, n ui.Node) (ui.Node, error) { return ui.El("article", nil, n), nil }}, LayoutIDs: []string{"default-original"}, Metadata: []MetadataLayer{{Load: func(*Context, seo.Metadata) (seo.Metadata, error) { return seo.Metadata{}, nil }}}}
	slot := base
	slot.DefaultRoute = fallback
	app := testApp(t, config(slot))
	originalID := app.configurationID
	fallback.Page = static("mutated")
	fallback.LayoutIDs[0] = "changed"
	fallback.Metadata[0].Load = func(*Context, seo.Metadata) (seo.Metadata, error) { return seo.Metadata{}, errors.New("mutated") }
	fallback.Layouts[0] = func(*Context, ui.Node) (ui.Node, error) { return nil, errors.New("mutated") }
	response := request(app, "/dash/view", false)
	if response.Code != 200 || !strings.Contains(response.Body.String(), "original") || strings.Contains(response.Body.String(), "mutated") || app.configurationID != originalID {
		t.Fatal(response.Code, response.Body.String())
	}
}

func defaultNavigationRequest(t *testing.T, path string, state navigation.SlotState) *http.Request {
	t.Helper()
	r := httptest.NewRequest("GET", path, nil)
	r.Header.Set("X-Nextgo-Navigate", "1")
	data, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set(navigation.SlotsHeader, string(data))
	return r
}
func serveDefaultRequest(app *App, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	return w
}
