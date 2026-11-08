package navigation

import (
	"strings"
	"testing"
)

func TestRootSelectionUsesFirstMatchingRouteAndFailsClosed(t *testing.T) {
	a, b := strings.Repeat("a", 64), strings.Repeat("b", 64)
	m := Manifest{Routes: []Route{{Pattern: "/items/new", Root: a}, {Pattern: "/items/:id", Root: b}, {Pattern: "/api", Endpoint: true}, {Pattern: "/files/*path", Root: a}}}
	for _, tc := range []struct {
		path, root string
		want       bool
	}{{"/items/new", a, true}, {"/items/new", b, false}, {"/items/a%20b/", b, true}, {"/files/a/b", a, true}, {"/missing", a, false}, {"/api", a, false}, {"/items/new", "", false}, {"/items/new", "bad", false}} {
		if m.SameRoot(tc.path, tc.root) != tc.want {
			t.Fatal(tc)
		}
	}
	if !(Manifest{}).SameRoot("/unregistered", "") || (Manifest{}).SameRoot("/", a) {
		t.Fatal("legacy roots")
	}
}

func TestEndpointSelectionMirrorsServerSpecificity(t *testing.T) {
	m := Manifest{Routes: []Route{{Pattern: "/reports/new"}, {Pattern: "/reports/:id", Endpoint: true}, {Pattern: "/files/*path", Endpoint: true}}, Handlers: []string{"/robots.txt", "/downloads/", "/health"}}
	for _, tc := range []struct {
		escaped, decoded string
		want             bool
	}{
		{"/reports/new", "/reports/new", false}, {"/reports/a%20b", "/reports/a b", true}, {"/files/a/b", "/files/a/b", true}, {"/robots.txt", "/robots.txt", true}, {"/robots.txt/more", "/robots.txt/more", false}, {"/downloads/a", "/downloads/a", true}, {"/download", "/download", false}, {"/missing", "/missing", false}, {"/reports/a%20b/", "/reports/a b/", true}, {"/health/", "/health/", true}, {"/reports/new/", "/reports/new/", false},
	} {
		if got := m.IsEndpoint(tc.escaped, tc.decoded); got != tc.want {
			t.Errorf("%s=%v want %v", tc.escaped, got, tc.want)
		}
	}
}
