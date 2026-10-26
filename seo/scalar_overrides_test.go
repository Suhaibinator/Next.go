package seo

import (
	"reflect"
	"strings"
	"testing"
)

func scalarClearAll() ScalarClears {
	return ScalarClears{Description: true, Canonical: true, Robots: true}
}

func TestScalarClearsThreeLevelInheritanceAndLiteralReplacement(t *testing.T) {
	parent := Metadata{Description: "parent", Canonical: "https://example.test/parent", Robots: "index"}
	cleared := Merge(parent, Metadata{Clear: scalarClearAll()})
	grandchild := Merge(cleared, Metadata{})
	if grandchild.Description != "" || grandchild.Canonical != "" || grandchild.Robots != "" || grandchild.Clear != scalarClearAll() {
		t.Fatal("empty descendant resurrected inherited metadata", grandchild)
	}
	replaced := Merge(grandchild, parent)
	if replaced.Description != parent.Description || replaced.Canonical != parent.Canonical || replaced.Robots != parent.Robots || replaced.Clear != (ScalarClears{}) {
		t.Fatal("later literals did not reset clears", replaced)
	}
}

func TestScalarClearsSelectiveAndSameContributionLiteralWins(t *testing.T) {
	parent := Metadata{Description: "parent", Canonical: "https://example.test/parent", Robots: "index"}
	for _, tc := range []struct {
		name  string
		clear ScalarClears
		want  Metadata
	}{
		{"description", ScalarClears{Description: true}, Metadata{Canonical: parent.Canonical, Robots: parent.Robots}},
		{"canonical", ScalarClears{Canonical: true}, Metadata{Description: parent.Description, Robots: parent.Robots}},
		{"robots", ScalarClears{Robots: true}, Metadata{Description: parent.Description, Canonical: parent.Canonical}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Merge(parent, Metadata{Clear: tc.clear})
			if got.Description != tc.want.Description || got.Canonical != tc.want.Canonical || got.Robots != tc.want.Robots || got.Clear != tc.clear {
				t.Fatal(got)
			}
		})
	}
	child := parent
	child.Clear = scalarClearAll()
	got := Merge(Metadata{}, child)
	if got.Description != parent.Description || got.Canonical != parent.Canonical || got.Robots != parent.Robots || got.Clear != (ScalarClears{}) {
		t.Fatal("same-contribution literals lost to flags", got)
	}
}

func TestScalarClearsZeroValuePreservesLegacyMetadataAndTitleTemplates(t *testing.T) {
	parent := Metadata{Title: "Parent", TitleTemplate: "%s | Example", Description: "description", Canonical: "https://example.test/", Robots: "index", Language: "fr", Direction: "ltr", OpenGraph: OpenGraph{Description: "social"}, Twitter: Twitter{Description: "tweet"}}
	if got := Merge(parent, Metadata{}); !reflect.DeepEqual(got, parent) {
		t.Fatal("zero contribution changed legacy inheritance", got)
	}
	got, err := ResolveContribution(parent, Metadata{Title: "Child", Clear: scalarClearAll()})
	if err != nil || got.Title != "Child | Example" || got.OpenGraph.Description != "social" || got.Twitter.Description != "tweet" || got.Language != "fr" || got.Direction != "ltr" {
		t.Fatal("clears affected unrelated metadata", got, err)
	}
}

func TestScalarClearsDefensiveSnapshotsAndInputOwnership(t *testing.T) {
	manifest := "/app.webmanifest"
	parent := Metadata{Clear: scalarClearAll(), Manifest: &manifest, Alternates: map[string]string{"en": "https://example.test/en"}, OpenGraph: OpenGraph{Images: []Image{{URL: "https://example.test/image.png"}}}}
	got := Merge(Metadata{}, parent)
	for i := 0; i < 4; i++ {
		got = Merge(Metadata{}, Merge(got, Metadata{}))
	}
	if got.Clear != scalarClearAll() {
		t.Fatal("snapshot lost explicit clear", got)
	}
	got.Alternates["en"] = "https://other.test/"
	got.OpenGraph.Images[0].URL = "https://other.test/image.png"
	*got.Manifest = "/changed.webmanifest"
	if parent.Alternates["en"] != "https://example.test/en" || parent.OpenGraph.Images[0].URL != "https://example.test/image.png" || manifest != "/app.webmanifest" {
		t.Fatal("snapshot aliases parent")
	}
}

func TestScalarClearsElementsOmitOnlySelectedMetadataAndValidateReplacement(t *testing.T) {
	parent := Metadata{Description: "private description", Canonical: "https://example.test/private", Robots: "index", OpenGraph: OpenGraph{Description: "public social"}}
	resolved := Merge(parent, Metadata{Clear: scalarClearAll()})
	elements, err := Elements(resolved)
	if err != nil {
		t.Fatal(err)
	}
	if len(elements) != 1 || elements[0].Attrs["property"] != "og:description" {
		t.Fatal(elements)
	}
	for _, bad := range []string{"javascript:alert(1)", "//foreign.test/", "https://user:password@example.test/"} {
		if _, err := Elements(Merge(resolved, Metadata{Canonical: bad, Clear: ScalarClears{Canonical: true}})); err == nil {
			t.Fatal("clear masked unsafe literal", bad)
		}
	}
	markup, err := uiHTMLMetadata(Metadata{Description: `<script>&"`, Clear: ScalarClears{Description: true}})
	if err != nil || strings.Contains(markup, "<script>") || !strings.Contains(markup, "&lt;script&gt;") {
		t.Fatal(markup, err)
	}
}

func uiHTMLMetadata(m Metadata) (string, error) {
	var out strings.Builder
	err := Node(m).Render(&out)
	return out.String(), err
}

func TestScalarClearsCanonicalBaseURLResolutionAndRepeatedSnapshots(t *testing.T) {
	base := Metadata{BaseURL: "https://example.test/docs", Canonical: "old"}
	cleared, err := ResolveContribution(base, Metadata{Clear: ScalarClears{Canonical: true}})
	if err != nil || cleared.Canonical != "" || !cleared.Clear.Canonical {
		t.Fatal(cleared, err)
	}
	cleared = Merge(Metadata{}, cleared)
	replaced, err := ResolveContribution(cleared, Metadata{Canonical: "new", Clear: ScalarClears{Canonical: true}})
	if err != nil || replaced.Canonical != "new" || replaced.Clear.Canonical {
		t.Fatal(replaced, err)
	}
	elements, err := Elements(replaced)
	if err != nil || len(elements) != 1 || elements[0].Attrs["rel"] != "canonical" || elements[0].Attrs["href"] != "https://example.test/docs/new" {
		t.Fatal("rendering must resolve the replacement against the inherited base", elements, err)
	}
	if _, err := Elements(Metadata{BaseURL: "javascript:invalid", Clear: scalarClearAll()}); err == nil {
		t.Fatal("clear bypassed base validation")
	}
}
