package seo

import (
	"encoding/json"
	"github.com/Suhaibinator/Next.go/ui"
	"strings"
	"testing"
)

func TestJSONLDIsInertEscapedAndSnapshotsInput(t *testing.T) {
	attack := `</script><img src=x onerror="alert(1)">`
	value := map[string]any{"@context": "https://schema.org", "@type": "Article", "name": attack}
	data, err := JSONLD(value)
	if err != nil {
		t.Fatal(err)
	}
	value["name"] = "changed"
	for _, data := range []StructuredData{data, mustJSONLD(t, json.RawMessage(`{"name":"</script><img src=x onerror=alert(1)>"}`))} {
		markup, err := ui.HTML(data.Node())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(markup, "</script>") != 1 || strings.Contains(markup, "<img") || !strings.Contains(markup, `type="application/ld+json"`) {
			t.Fatal(markup)
		}
	}
	element, err := data.Element()
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(element.Text), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["name"] != attack {
		t.Fatal("snapshot lost original value")
	}
	element.Attrs["type"] = "text/javascript"
	fresh, _ := data.Element()
	if fresh.Attrs["type"] != "application/ld+json" {
		t.Fatal("mutable attributes leaked")
	}
	if _, err := ui.HTML(ui.El("script", ui.Attrs{"type": "application/ld+json"}, ui.Text("{}"))); err == nil {
		t.Fatal("ordinary script restriction weakened")
	}
}

func mustJSONLD(t *testing.T, value any) StructuredData {
	t.Helper()
	data, err := JSONLD(value)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestJSONLDValidationAndBounds(t *testing.T) {
	cycle := map[string]any{}
	cycle["self"] = cycle
	for _, value := range []any{nil, "text", 1, []string{"text"}, make(chan int), cycle, map[string]string{"huge": strings.Repeat("a", 256<<10)}, json.RawMessage(`{"broken":`)} {
		if _, err := JSONLD(value); err == nil {
			t.Fatalf("accepted invalid data %T", value)
		}
	}
	entities := make([]map[string]string, 65)
	for i := range entities {
		entities[i] = map[string]string{"@type": "Thing"}
	}
	if _, err := JSONLD(entities); err == nil {
		t.Fatal("entity cap ignored")
	}
	var deep any = map[string]any{}
	for i := 0; i < 64; i++ {
		deep = map[string]any{"child": deep}
	}
	if _, err := JSONLD(deep); err == nil {
		t.Fatal("depth cap ignored")
	}
	var zero StructuredData
	if _, err := zero.Element(); err == nil {
		t.Fatal("zero structured data accepted")
	}
	if _, err := ui.HTML(zero.Node()); err == nil {
		t.Fatal("zero node rendered")
	}
}
