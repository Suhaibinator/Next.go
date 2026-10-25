package nextgo

import (
	"strings"
	"testing"

	"github.com/Suhaibinator/Next.go/seo"
)

func TestSharedPageManifestHeadRoundTripAndValidation(t *testing.T) {
	for _, href := range []string{"/app/site.webmanifest", "https://cdn.example.test/site.webmanifest"} {
		for _, typed := range []bool{false, true} {
			attrs := map[string]string{"rel": "manifest", "href": href}
			if typed {
				attrs["type"] = "application/manifest+json"
			}
			dto, err := encodeSharedPage(sharedCodecFixture(t), "binding")
			if err != nil {
				t.Fatal(err)
			}
			dto.Head = []seo.Element{{Tag: "link", Attrs: attrs}}
			decoded, err := decodeSharedPage(dto, "binding")
			if err != nil || !strings.Contains(decoded.Page.preparedHead.markup, `rel="manifest"`) || decoded.Page.preparedHead.elements[0].Attrs["href"] != href {
				t.Fatal("safe manifest rejected", href, typed, err)
			}
		}
	}
	for _, attrs := range []map[string]string{
		{"rel": "manifest", "href": "//other.test/m"},
		{"rel": "manifest", "href": "https://user@example.test/m"},
		{"rel": "manifest", "href": "/\\other.test/m"},
		{"rel": "manifest", "href": "/bad\nmanifest"},
		{"rel": "manifest", "href": "javascript:alert(1)"},
		{"rel": "manifest", "href": "/m", "type": "text/html"},
		{"rel": "manifest", "href": "/m", "type": ""},
		{"rel": "manifest", "href": "/m", "crossorigin": "anonymous"},
		{"rel": "manifest", "href": "/m", "integrity": "sha256-AAAA"},
		{"rel": "manifest", "href": "/m", "sizes": "any"},
		{"rel": "manifest", "href": "/m", "onload": "alert(1)"},
	} {
		dto, err := encodeSharedPage(sharedCodecFixture(t), "binding")
		if err != nil {
			t.Fatal(err)
		}
		dto.Head = []seo.Element{{Tag: "link", Attrs: attrs}}
		if _, err := decodeSharedPage(dto, "binding"); err == nil {
			t.Fatal("unsafe manifest snapshot accepted", attrs)
		}
	}
}
