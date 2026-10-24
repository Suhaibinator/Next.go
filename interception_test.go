package nextgo

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/ui"
)

func interceptionFixture(t *testing.T) (*App, *int, *int) {
	backgroundLoads, modalLoads := 0, 0
	layout := func(c *Context, body ui.Node) (ui.Node, error) {
		modal, err := c.Slot("modal")
		if err != nil {
			return nil, err
		}
		active, err := c.SelectedSegments("modal")
		if err != nil {
			return nil, err
		}
		return ui.El("main", nil, body, modal, ui.Text("selected "+strings.Join(active, "/"))), nil
	}
	page := func(c *Context) (Page, error) {
		backgroundLoads++
		if c.Request.Header.Get("X-Background-Deny") == "yes" {
			return Page{}, ErrForbidden
		}
		return Page{Title: "Gallery", Body: ui.Text("Gallery " + c.Params["tenant"] + " " + c.Query.Get("q") + " " + c.Request.Header.Get("X-Current"))}, nil
	}
	modal := func(c *Context) (Page, error) {
		modalLoads++
		if c.Request.Header.Get("X-Modal-Deny") == "yes" {
			return Page{}, ErrForbidden
		}
		return Page{Body: ui.Text("Modal " + c.Params["tenant"] + " " + c.Params["id"] + " " + c.Query.Get("q") + " " + c.Request.Header.Get("X-Current"))}, nil
	}
	return testApp(t, Config{PreserveLayouts: true, Forbidden: static("Denied"), Routes: []Route{
		{Pattern: "/gallery/:tenant", Page: page, Layouts: []LayoutFunc{layout}, LayoutIDs: []string{"gallery"}, LayoutBases: []string{"/gallery/:tenant"}, Primary: &PrimarySlot{ID: "gallery.children", Base: "/gallery/:tenant", Layout: 0}, Slots: []Slot{{ID: "gallery.modal", Name: "modal", Base: "/gallery/:tenant", Layout: 0, Default: static("empty modal"), Intercepts: []Route{{Pattern: "/photos/:tenant/:id", Page: modal}}}}},
		{Pattern: "/photos/:tenant/:id", Page: static("Standalone photo"), Cache: &CachePolicy{TTL: time.Minute}},
	}}), &backgroundLoads, &modalLoads
}

func TestInterceptionDirectSoftHistoryAndCurrentAuthorization(t *testing.T) {
	app, backgroundLoads, modalLoads := interceptionFixture(t)
	initial := request(app, "/gallery/one?q=background", false)
	if initial.Code != 200 || !strings.Contains(initial.Body.String(), "empty modal") {
		t.Fatal(initial.Code, initial.Body.String())
	}
	carrier := nestedCarrier(app, initial.Body.String())
	for _, navigate := range []bool{false, true} {
		direct := request(app, "/photos/one/42?q=photo", navigate)
		if direct.Code != 200 || strings.Contains(direct.Body.String(), "Gallery one") || !strings.Contains(direct.Body.String(), "Standalone photo") {
			t.Fatal(direct.Code, direct.Body.String())
		}
		if navigate && direct.Header().Get("X-Nextgo-Prefetch") != "" {
			t.Fatal("context-dependent target entered URL-only navigation cache")
		}
	}
	r := defaultNavigationRequest(t, "/photos/one/42?q=photo", carrier)
	r.Header.Set("X-Current", "fresh")
	response := serveDefaultRequest(app, r)
	var envelope Envelope
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || response.Code != 200 || envelope.URL != "/photos/one/42?q=photo" || !strings.Contains(envelope.HTML, "Gallery one background fresh") || !strings.Contains(envelope.HTML, "Modal one 42 photo fresh") || !strings.Contains(envelope.HTML, "selected photos/one/42") {
		t.Fatal(response.Code, response.Body.String(), err)
	}
	if *backgroundLoads != 2 || *modalLoads != 1 {
		t.Fatal(*backgroundLoads, *modalLoads)
	}
	modalCarrier := nestedCarrier(app, envelope.HTML)
	closed := slotNavigate(t, app, "/gallery/one?q=background", carrier, false)
	if closed.Code != 200 || strings.Contains(closed.Body.String(), "Modal one") {
		t.Fatal(closed.Code, closed.Body.String())
	}
	forward := slotNavigate(t, app, "/photos/one/42?q=photo", modalCarrier, false)
	if forward.Code != 200 || !strings.Contains(forward.Body.String(), "Modal one 42") {
		t.Fatal(forward.Code, forward.Body.String())
	}
	for _, header := range []string{"X-Background-Deny", "X-Modal-Deny"} {
		r := defaultNavigationRequest(t, "/photos/one/42", modalCarrier)
		r.Header.Set(header, "yes")
		denied := serveDefaultRequest(app, r)
		if denied.Code != 403 || strings.Contains(denied.Body.String(), "Gallery one") || strings.Contains(denied.Body.String(), "Modal one") || !strings.Contains(denied.Body.String(), "Denied") {
			t.Fatal(header, denied.Code, denied.Body.String())
		}
	}
	other := slotNavigate(t, app, "/photos/two/42", carrier, false)
	if other.Code != 200 || !strings.Contains(other.Body.String(), "Standalone photo") || strings.Contains(other.Body.String(), "Gallery one") {
		t.Fatal(other.Code, other.Body.String())
	}
	// Full documents ignore navigation hints even if a client sends old state.
	hard := httptest.NewRequest("GET", "/photos/one/42", nil)
	encoded, _ := json.Marshal(modalCarrier)
	hard.Header.Set("X-Nextgo-Slots", string(encoded))
	full := serveDefaultRequest(app, hard)
	if full.Code != 200 || !strings.Contains(full.Body.String(), "Standalone photo") || strings.Contains(full.Body.String(), "Gallery one") {
		t.Fatal(full.Code, full.Body.String())
	}
}

func TestInterceptionRegistrationAndCarrierValidation(t *testing.T) {
	app, _, _ := interceptionFixture(t)
	for _, raw := range []string{"invalid", `{"configuration":"` + strings.Repeat("f", 64) + `","selections":{}}`} {
		r := httptest.NewRequest("GET", "/photos/one/42", nil)
		r.Header.Set("X-Nextgo-Navigate", "1")
		r.Header.Set("X-Nextgo-Slots", raw)
		response := serveDefaultRequest(app, r)
		if response.Code != 500 || !strings.Contains(response.Header().Get("Cache-Control"), "no-store") || strings.Contains(response.Body.String(), "Gallery") {
			t.Fatal(response.Code, response.Body.String())
		}
	}
	for _, mutate := range []func(*Route){
		func(r *Route) { r.Primary = nil },
		func(r *Route) { r.Slots[0].Default = nil },
		func(r *Route) { r.Slots[0].Intercepts[0].Pattern = "/unregistered/:id" },
		func(r *Route) { r.Slots[0].Intercepts = append(r.Slots[0].Intercepts, r.Slots[0].Intercepts[0]) },
	} {
		fresh, _, _ := interceptionFixture(t)
		routes := fresh.routes
		mutate(&routes[0])
		if _, err := New(Config{Routes: routes}); err == nil {
			t.Fatal("invalid interception registration accepted")
		}
	}
}

func TestInterceptionWithinOwnerNeverRunsOnDirectRequests(t *testing.T) {
	fixture, _, _ := interceptionFixture(t)
	owner := fixture.routes[0]
	owner.Slots[0].Intercepts[0].Pattern = "/gallery/:tenant/photos/:id"
	canonical := owner
	canonical.Pattern = "/gallery/:tenant/photos/:id"
	canonical.Page = static("Standalone nested photo")
	app := testApp(t, Config{PreserveLayouts: true, Routes: []Route{owner, canonical}})
	direct := request(app, "/gallery/one/photos/42", false)
	if direct.Code != 200 || !strings.Contains(direct.Body.String(), "Standalone nested photo") || strings.Contains(direct.Body.String(), "Modal one 42") {
		t.Fatal(direct.Code, direct.Body.String())
	}
	initial := request(app, "/gallery/one", false)
	soft := slotNavigate(t, app, "/gallery/one/photos/42", nestedCarrier(app, initial.Body.String()), false)
	if soft.Code != 200 || !strings.Contains(soft.Body.String(), "Modal one 42") || strings.Contains(soft.Body.String(), "Standalone nested photo") {
		t.Fatal(soft.Code, soft.Body.String())
	}
}
