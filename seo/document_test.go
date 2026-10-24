package seo

import (
	"math"
	"strings"
	"testing"
)

func TestTitleContributionsUseNearestTemplateAndAbsoluteOverrides(t *testing.T) {
	root := Metadata{Title: "Site", TitleTemplate: "%s | Site"}
	section, err := ResolveContribution(root, Metadata{Title: "Section", TitleTemplate: "%s / Section"})
	if err != nil || section.Title != "Section | Site" {
		t.Fatal(section, err)
	}
	page, err := ResolveContribution(section, Metadata{Title: "Article"})
	if err != nil || page.Title != "Article / Section" {
		t.Fatal(page, err)
	}
	inherited, _ := ResolveContribution(page, Metadata{})
	if inherited.Title != page.Title {
		t.Fatal("inherited title formatted again", inherited.Title)
	}
	absolute, _ := ResolveContribution(section, Metadata{Title: "Standalone", AbsoluteTitle: true})
	if absolute.Title != "Standalone" {
		t.Fatal(absolute.Title)
	}
	if merged := Merge(root, Metadata{Title: "Unformatted"}); merged.Title != "Unformatted" {
		t.Fatal("pure merge applied a template")
	}
	for _, value := range []string{"no placeholder", "%s %s", strings.Repeat("x", 2049) + "%s"} {
		if ValidateTitleTemplate(value) == nil {
			t.Fatal("invalid template accepted", value)
		}
	}
}

func TestViewportValidationRenderingAndSnapshotOwnership(t *testing.T) {
	zoom := true
	viewport := Viewport{Width: "device-width", InitialScale: 1.25, MinimumScale: 1, MaximumScale: 4, UserScalable: &zoom, Fit: "cover", InteractiveWidget: "resizes-content", ColorScheme: "light dark", ThemeColors: []ThemeColor{{Color: "#123456", Media: "(prefers-color-scheme: dark)"}}}
	document, err := ResolveDocument(Metadata{Language: "ar", Direction: "rtl", Viewport: &viewport})
	if err != nil || document.Language != "ar" || document.Direction != "rtl" || document.Viewport != "width=device-width,initial-scale=1.25,minimum-scale=1,maximum-scale=4,user-scalable=yes,viewport-fit=cover,interactive-widget=resizes-content" {
		t.Fatal(document, err)
	}
	merged := Merge(Metadata{Viewport: &viewport}, Metadata{})
	merged.Viewport.ThemeColors[0].Color = "red"
	*merged.Viewport.UserScalable = false
	if viewport.ThemeColors[0].Color != "#123456" || !zoom {
		t.Fatal("viewport aliases caller data")
	}
	for _, invalid := range []Viewport{{Width: "500px"}, {Width: "0"}, {Height: "device-width"}, {InitialScale: math.NaN()}, {InitialScale: -1}, {MaximumScale: 11}, {MinimumScale: 3, MaximumScale: 2}, {Fit: "unexpected"}, {InteractiveWidget: "unexpected"}, {ColorScheme: "unexpected"}, {ThemeColors: []ThemeColor{{Color: "bad\ncolor"}}}} {
		if _, err := invalid.Content(); err == nil {
			t.Fatal("invalid viewport accepted", invalid)
		}
	}
}
