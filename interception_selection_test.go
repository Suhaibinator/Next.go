package nextgo

import (
	"github.com/Suhaibinator/Next.go/ui"
	"strings"
	"testing"
)

func TestInterceptionRejectsEquallyEligibleBackgroundsBeforeLoading(t *testing.T) {
	fixture, loads, _ := interceptionFixture(t)
	routes := fixture.routes
	routes[0].Slots[0].Intercepts[0].Pattern = "/photos/:id"
	routes[1].Pattern = "/photos/:id"
	app := testApp(t, Config{PreserveLayouts: true, Routes: routes})
	one := nestedCarrier(app, request(app, "/gallery/one", false).Body.String())
	two := nestedCarrier(app, request(app, "/gallery/two", false).Body.String())
	for key, value := range two.Selections {
		one.Selections[key] = value
	}
	before := *loads
	// Map iteration must never choose one of two equally eligible concrete
	// background scopes, even when both are valid registered routes.
	for i := 0; i < 20; i++ {
		response := slotNavigate(t, app, "/photos/42", one, false)
		if response.Code != 500 || strings.Contains(response.Body.String(), "Gallery one") || strings.Contains(response.Body.String(), "Gallery two") {
			t.Fatal(response.Code, response.Body.String())
		}
	}
	if *loads != before {
		t.Fatal("ambiguous carrier executed a background loader", *loads, before)
	}
}

func TestInterceptionStaticTargetsOutrankParametersAcrossSlots(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		fixture, _, _ := interceptionFixture(t)
		routes := fixture.routes
		featured := routes[0].Slots[0]
		featured.ID = "gallery.featured"
		featured.Name = "featured"
		featured.Intercepts = []Route{{Pattern: "/photos/:tenant/special", Page: static("Featured modal")}}
		routes[0].Slots = append(routes[0].Slots, featured)
		if reverse {
			routes[0].Slots[0], routes[0].Slots[1] = routes[0].Slots[1], routes[0].Slots[0]
		}
		app := testApp(t, Config{Routes: routes})
		carrier := nestedCarrier(app, request(app, "/gallery/one", false).Body.String())
		view, err := app.selectInterception(defaultNavigationRequest(t, "/photos/one/special", carrier))
		if err != nil || view == nil || view.slot != "gallery.featured" {
			t.Fatalf("reverse=%v view=%+v err=%v", reverse, view, err)
		}
	}
}

func TestInterceptionOptionalCatchallRequiresCoveringStandalonePage(t *testing.T) {
	for _, canonical := range []string{"/photos/*parts?", "/photos/:id"} {
		fixture, _, _ := interceptionFixture(t)
		routes := fixture.routes
		routes[0].Slots[0].Intercepts[0].Pattern = "/photos/*parts?"
		routes[1].Pattern = canonical
		app, err := New(Config{Routes: routes})
		if canonical == "/photos/:id" {
			if err == nil {
				t.Fatal("single segment standalone page accepted an unbounded target")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		carrier := nestedCarrier(app, request(app, "/gallery/one", false).Body.String())
		for _, path := range []string{"/photos", "/photos/a/b"} {
			response := slotNavigate(t, app, path, carrier, false)
			if response.Code != 200 {
				t.Fatal(path, response.Code, response.Body.String())
			}
		}
	}
}

func TestInterceptionDeepestOwnerDisambiguatesShallowerOrigins(t *testing.T) {
	fixture, _, _ := interceptionFixture(t)
	routes := fixture.routes
	routes[0].Slots[0].Intercepts[0].Pattern = "/photos/:id"
	routes[1].Pattern = "/photos/:id"
	child := routes[0]
	child.Pattern = "/gallery/:tenant/album"
	child.Layouts = append(append([]LayoutFunc(nil), child.Layouts...), func(_ *Context, body ui.Node) (ui.Node, error) { return body, nil })
	child.LayoutIDs = []string{"gallery", "album"}
	child.LayoutBases = []string{"/gallery/:tenant", "/gallery/:tenant/album"}
	child.Primaries = []PrimarySlot{*child.Primary, {ID: "album.children", Base: "/gallery/:tenant/album", Layout: 1}}
	child.Primary = nil
	nested := child.Slots[0]
	nested.ID = "album.modal"
	nested.Name = "albumModal"
	nested.Base = "/gallery/:tenant/album"
	nested.Layout = 1
	child.Slots = append(append([]Slot(nil), child.Slots...), nested)
	app := testApp(t, Config{Routes: append(routes, child)})
	one := nestedCarrier(app, request(app, "/gallery/one", false).Body.String())
	two := nestedCarrier(app, request(app, "/gallery/two", false).Body.String())
	deep := nestedCarrier(app, request(app, "/gallery/one/album", false).Body.String())
	for key, value := range two.Selections {
		one.Selections[key] = value
	}
	for key, value := range deep.Selections {
		one.Selections[key] = value
	}
	view, err := app.selectInterception(defaultNavigationRequest(t, "/photos/42", one))
	if err != nil || view == nil || view.slot != "album.modal" || view.url.RequestURI() != "/gallery/one/album" {
		t.Fatalf("deep owner: %+v %v", view, err)
	}
}
