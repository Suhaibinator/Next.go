package offline

import (
	"errors"
	"strings"
	"testing"
)

func TestPreparationURLBindsFrozenTransportWithoutChangingPageKeys(t *testing.T) {
	for _, base := range []string{"", "/guide"} {
		spec := sampleSpec(1000)
		spec.Config.BasePath = base
		for i := range spec.Assets {
			spec.Assets[i].URL = strings.Replace(spec.Assets[i].URL, "https://example.test/guide/", "https://example.test"+base+"/", 1)
		}
		catalog := mustCatalog(t, spec)
		generation := catalog.Generation()
		for _, doc := range catalog.Spec().Documents {
			prepared, err := catalog.PreparationURL(doc.Path)
			want := spec.Config.Origin + base + PreparedDocumentsPath + generation + "/" + doc.SHA256
			if err != nil || prepared != want {
				t.Fatal(prepared, want, err)
			}
			pageURL, err := catalog.Policy().DocumentURL(doc.Path)
			if err != nil || pageURL == prepared {
				t.Fatal("transport URL replaced page cache key", pageURL, err)
			}
			found := false
			for _, entry := range catalog.Entries() {
				if entry.URL == pageURL {
					found = true
				}
				if entry.URL == prepared {
					t.Fatal("transport URL entered catalog inventory")
				}
			}
			if !found {
				t.Fatal("original page key missing")
			}
		}
		if catalog.Generation() != generation {
			t.Fatal("transport lookup changed generation")
		}
	}
}

func TestPreparationURLRejectsInvalidAndUndeclaredPages(t *testing.T) {
	catalog := mustCatalog(t, sampleSpec(1000))
	for _, path := range []string{"", "/unknown", "/?private=1", "/offline/", "/%6fffline", "/../offline", "/_nextgo/offline/documents/"} {
		if value, err := catalog.PreparationURL(path); !errors.Is(err, ErrInvalid) || value != "" {
			t.Fatal("undeclared path acquired prepared bytes", path, value, err)
		}
	}
	for _, invalid := range []*Catalog{nil, new(Catalog)} {
		if value, err := invalid.PreparationURL("/"); !errors.Is(err, ErrInvalid) || value != "" {
			t.Fatal("invalid catalog acquired prepared bytes", value, err)
		}
	}
}
