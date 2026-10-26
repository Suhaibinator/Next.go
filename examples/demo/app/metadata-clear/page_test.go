//go:build !js

package metadataclear

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/assets"
	"github.com/Suhaibinator/Next.go/seo"
)

func TestPageInheritsBeforeClearingItsThreeScalars(t *testing.T) {
	for _, name := range []string{"NEXTGO_DEMO_METADATA_SOURCE", "NEXTGO_DEMO_METADATA_SOURCE_TIMEOUT", "NEXTGO_DEMO_METADATA_FIXTURE", "NEXTGO_DEMO_METADATA_TEST_MODE"} {
		t.Setenv(name, "")
	}
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("unexpected authenticated source request")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":1,"title":"Source title","description":"Source description","canonical":"https://example.com/source","socialTitle":"Resolved social title","socialDescription":"Resolved social description"}`))
	}))
	defer source.Close()
	t.Setenv("NEXTGO_DEMO_METADATA_SOURCE", source.URL+"/value")
	c := &nextgo.Context{Context: nextgo.WithRequestMemo(context.Background()), Request: httptest.NewRequest("GET", "https://example.test/metadata-clear", nil)}
	inherited, err := Metadata(c, seo.Metadata{})
	if err != nil {
		t.Fatal(err)
	}
	page, err := Page(c)
	if err != nil {
		t.Fatal(err)
	}
	if !page.Stream || page.DeferredMetadata == nil || page.Title != "" || page.Description != "" || page.Metadata.Description != "" || page.Metadata.Canonical != "" || page.Metadata.Robots != "" || page.Metadata.Clear != (seo.ScalarClears{}) {
		t.Fatal("page must inherit the provider before its callback", page)
	}
	fallback := seo.Merge(inherited, page.Metadata)
	if fallback.Description != inheritedDescription || fallback.Canonical != "https://example.com/metadata-clear?phase=inherited" || fallback.Robots != inheritedRobots {
		t.Fatal("provider values were not inherited", fallback)
	}
	if len(page.Resources) != 1 || len(page.JSONLD) != 1 {
		t.Fatal("frozen companion ownership changed")
	}
	if _, err := assets.Elements(page.Resources...); err != nil {
		t.Fatal(err)
	}
	contribution, err := page.DeferredMetadata(c, fallback)
	if err != nil {
		t.Fatal(err)
	}
	if contribution.Clear != (seo.ScalarClears{Description: true, Canonical: true, Robots: true}) || contribution.Description != "" || contribution.Canonical != "" || contribution.Robots != "" {
		t.Fatal("clear must carry flags plus empty literals", contribution)
	}
	final := seo.Merge(fallback, contribution)
	if final.Title != finalTitle || final.Description != "" || final.Canonical != "" || final.Robots != "" || final.OpenGraph.Title != "Resolved social title" || final.Twitter.Title != "Resolved social title" {
		t.Fatal("scalar clear damaged other resolved metadata", final)
	}
	elements, err := seo.Elements(final)
	if err != nil {
		t.Fatal(err)
	}
	for _, element := range elements {
		if element.Tag == "link" && element.Attrs["rel"] == "canonical" || element.Tag == "meta" && (element.Attrs["name"] == "robots" || element.Attrs["name"] == "description") {
			t.Fatal("cleared typed descriptor remained", element)
		}
	}
	var body strings.Builder
	if err := page.Body.Render(&body); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`data-metadata-clear=""`, `href="/metadata-clear?cycle=next"`, `data-metadata-clear-refresh-link=""`, `data-metadata-input=""`, `data-count="">3`} {
		if !strings.Contains(body.String(), want) {
			t.Fatal("sample lost meaningful Go body", want)
		}
	}
	if strings.Contains(body.String(), source.URL) {
		t.Fatal("native source endpoint leaked into body/props")
	}
	if _, err := assets.Elements(page.Resources...); err == nil {
		t.Fatal("second resource callback was not detected")
	}
}
