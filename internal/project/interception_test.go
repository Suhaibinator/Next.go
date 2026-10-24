package project

import (
	"strings"
	"testing"
)

func TestInterceptionDiscoveryIsSeparateFromCanonicalURLs(t *testing.T) {
	root := fixture(t, map[string]string{
		"app/layout.go":                     "package app",
		"app/gallery/page.go":               "package gallery",
		"app/gallery/layout.go":             "package gallery",
		"app/gallery/slot_modal/default.go": "package modal",
		"app/gallery/slot_modal/intercept_root/photos/param_id/page.go": "package photo",
		"app/gallery/slot_modal/intercept_root/layout.go":               "package intercept_root",
		"app/photos/param_id/page.go":                                   "package photo",
	})
	p, err := Discover(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Routes) != 2 {
		t.Fatal("interceptor became a standalone URL", p.Routes)
	}
	var owner Route
	for _, r := range p.Routes {
		if r.Pattern == "/gallery" {
			owner = r
		}
	}
	if owner.Primary == nil || len(owner.Slots) != 1 || len(owner.Slots[0].Intercepts) != 1 {
		t.Fatalf("missing primary/slot/interceptor: %+v", owner)
	}
	branch := owner.Slots[0].Intercepts[0]
	if branch.Pattern != "/photos/:id" || len(branch.LayoutBases) != 1 || branch.LayoutBases[0] != "/" || !strings.Contains(branch.Import, "/slot_modal/intercept_root/") {
		t.Fatal(branch)
	}
	source := serverSource(p, "intercept", false)
	if !strings.Contains(source, "Intercepts:[]nextgo.Route{") || !strings.Contains(source, branch.Import) {
		t.Fatal(source)
	}
}

func TestInterceptionDirectoriesRejectMisplacedAndNestedOwners(t *testing.T) {
	for _, files := range []map[string]string{
		{"app/page.go": "package app", "app/intercept_root/photo/page.go": "package photo"},
		{"app/page.go": "package app", "app/layout.go": "package app", "app/slot_outer/default.go": "package outer", "app/slot_outer/layout.go": "package outer", "app/slot_outer/slot_modal/default.go": "package modal", "app/slot_outer/slot_modal/intercept_root/photo/page.go": "package photo"},
		{"app/gallery/page.go": "package gallery", "app/gallery/layout.go": "package gallery", "app/gallery/slot_modal/default.go": "package modal", "app/gallery/slot_modal/intercept_root/photo/page.go": "package photo"},
	} {
		if _, err := Discover(fixture(t, files)); err == nil {
			t.Fatal("invalid interception directories accepted")
		}
	}
}

func TestInterceptionDiscoveryChecksCompleteCatchallCoverage(t *testing.T) {
	for _, normal := range []string{"app/photos/optional_path/page.go", "app/photos/param_id/page.go"} {
		root := fixture(t, map[string]string{
			"app/gallery/page.go": "package gallery", "app/gallery/layout.go": "package gallery", "app/gallery/slot_modal/default.go": "package modal",
			"app/gallery/slot_modal/intercept_root/photos/optional_rest/page.go": "package photo", normal: "package photo",
		})
		_, err := Discover(root)
		if strings.Contains(normal, "optional_") {
			if err != nil {
				t.Fatal(err)
			}
		} else if err == nil {
			t.Fatal("single-segment page accepted an optional catchall interception")
		}
	}
}
