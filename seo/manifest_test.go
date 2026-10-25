package seo

import (
	"strings"
	"testing"

	"github.com/Suhaibinator/Next.go/ui"
)

func TestManifestInheritanceDisableAndOwnership(t *testing.T) {
	parentValue, childValue := "/parent.webmanifest", "/child.webmanifest"
	parent := Metadata{Manifest: &parentValue}
	inherited, err := ResolveContribution(parent, Metadata{})
	if err != nil || inherited.Manifest == parent.Manifest || *inherited.Manifest != parentValue {
		t.Fatal("manifest not independently inherited", inherited.Manifest, err)
	}
	parentValue = "/changed.webmanifest"
	if *inherited.Manifest != "/parent.webmanifest" {
		t.Fatal("inherited pointer aliases parent")
	}
	override := Merge(parent, Metadata{Manifest: &childValue})
	childValue = "/changed-child.webmanifest"
	if override.Manifest == &childValue || *override.Manifest != "/child.webmanifest" {
		t.Fatal("override pointer aliases child")
	}
	empty := ""
	disabled := Merge(parent, Metadata{Manifest: &empty})
	if disabled.Manifest == nil || disabled.Manifest == &empty || *disabled.Manifest != "" {
		t.Fatal("explicit disable lost")
	}
	elements, err := Elements(disabled)
	if err != nil || len(elements) != 0 {
		t.Fatal("disabled manifest emitted", elements, err)
	}
	if Merge(Metadata{}, Metadata{}).Manifest != nil {
		t.Fatal("absent manifest became an override")
	}
}

func TestManifestElementsURLsAndEscaping(t *testing.T) {
	for _, value := range []string{"/app/manifest.webmanifest", "https://cdn.example.test/manifest.json", "http://localhost:3000/m?name=a&other=b"} {
		elements, err := Elements(Metadata{BaseURL: "https://example.test/base", Manifest: &value})
		if err != nil || len(elements) != 1 || elements[0].Tag != "link" || len(elements[0].Attrs) != 2 || elements[0].Attrs["rel"] != "manifest" || elements[0].Attrs["href"] != value {
			t.Fatalf("%q: unexpected head %#v, %v", value, elements, err)
		}
	}
	for _, value := range []string{"manifest.json", "//elsewhere.test/m", "https://user:pass@example.test/m", "javascript:alert(1)", "/\\evil.test/m", "/bad\nmanifest", "/bad manifest", "/bad\x00manifest", "https://example.test:bad/m", "https:///m", strings.Repeat("a", (32<<10)+1)} {
		if _, err := Elements(Metadata{Manifest: &value}); err == nil {
			t.Fatalf("unsafe manifest accepted: %q", value)
		}
	}
	value := `/manifest?name="quoted"&next=<safe>`
	html, err := ui.HTML(Node(Metadata{Manifest: &value}))
	if err != nil || !strings.Contains(html, "&amp;") || strings.Contains(html, `name="quoted"`) || strings.Contains(html, "<safe>") {
		t.Fatal("manifest attribute not escaped", html, err)
	}
}
