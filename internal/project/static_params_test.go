package project

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestStaticParamsAncestorDiscoveryAndRegistration(t *testing.T) {
	p, err := Discover(fixture(t, map[string]string{
		"app/catalog/param_category/staticparams.go":                        "package category",
		"app/catalog/param_category/layout.go":                              "package category",
		"app/catalog/param_category/summary/page.go":                        "package summary",
		"app/catalog/param_category/group_items/param_item/page.go":         "package item",
		"app/catalog/param_category/group_items/param_item/staticparams.go": "package item",
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range p.Routes {
		if len(r.StaticParamsLayers) != 1 || r.StaticParamsLayers[0].Pattern != "/catalog/:category" || !strings.HasSuffix(r.StaticParamsLayers[0].Import, "param_category") {
			t.Fatal(r)
		}
	}
	source := serverSource(p, "code", false)
	if !strings.Contains(source, "StaticParamsLayers:[]nextgo.StaticParamsLayer{") || !strings.Contains(source, "StaticParamsID:") || !strings.Contains(source, "Load:") {
		t.Fatal(source)
	}
	if err := writeGo(filepath.Join(t.TempDir(), "main.go"), source); err != nil {
		t.Fatal(err)
	}
}
func TestStaticParamsAncestorDiscoveryRejectsOrphansAndSlots(t *testing.T) {
	for _, files := range []map[string]string{
		{"app/param_x/staticparams.go": "package x", "app/page.go": "package app"},
		{"app/products/staticparams.go": "package products", "app/products/param_x/page.go": "package x"},
		{"app/layout.go": "package app", "app/page.go": "package app", "app/slot_info/param_x/staticparams.go": "package x", "app/slot_info/param_x/summary/page.go": "package summary"},
	} {
		if _, err := Discover(fixture(t, files)); err == nil {
			t.Fatal("accepted invalid ancestor", files)
		}
	}
}
