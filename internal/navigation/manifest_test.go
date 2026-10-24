package navigation

import "testing"

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
