package project

import (
	"strings"
	"testing"
)

func TestIndependentGroupedRootLayoutAncestry(t *testing.T) {
	files := map[string]string{"app/group_a/layout.go": "package a", "app/group_a/page.go": "package a", "app/group_a/guide/page.go": "package guide", "app/group_b/layout.go": "package b", "app/group_b/dashboard/page.go": "package dashboard", "app/plain/page.go": "package plain"}
	p, err := Discover(fixture(t, files))
	if err != nil {
		t.Fatal(err)
	}
	roots := map[string]string{}
	for _, route := range p.Routes {
		for _, id := range route.LayoutIDs {
			if id != "" {
				roots[route.Pattern] = id
				break
			}
		}
	}
	if len(p.Routes) != 4 || roots["/"] == "" || roots["/"] != roots["/guide"] || roots["/dashboard"] == "" || roots["/dashboard"] == roots["/"] || roots["/plain"] != "" {
		t.Fatal("group root ancestry", roots)
	}
	files["app/layout.go"] = "package app"
	p, err = Discover(fixture(t, files))
	if err != nil {
		t.Fatal(err)
	}
	for _, route := range p.Routes {
		if len(route.LayoutIDs) == 0 || !strings.HasSuffix(route.LayoutIDs[0], "/app") {
			t.Fatal("global root not outermost", route)
		}
	}
}
