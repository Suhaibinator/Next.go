//go:build !js

package metadatastream

import (
	"errors"
	"sync/atomic"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/assets"
	"github.com/Suhaibinator/Next.go/examples/demo/components/counter"
	"github.com/Suhaibinator/Next.go/examples/demo/media"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

const fallbackTitle = "Loading public metadata — Next.go"
const fallbackDescription = "Fallback metadata while the public description is loading."
const resourcePath = "/assets/metadata-stream.css?resource-call=1"

// countedStylesheet is deliberately not SEO metadata. The head plan evaluates
// its callback once, then preserves that same stylesheet node through a late
// metadata update. Re-evaluation is a sample error, rather than hidden IO.
type countedStylesheet struct{ calls atomic.Int32 }

func (r *countedStylesheet) Elements() ([]seo.Element, error) {
	if r.calls.Add(1) != 1 {
		return nil, errors.New("metadata sample resource evaluated more than once")
	}
	return assets.Stylesheet(resourcePath).Elements()
}

func Page(c *nextgo.Context) (nextgo.Page, error) {
	settings, err := environmentSettings()
	if err != nil {
		return nextgo.Page{}, err
	}
	structured, err := seo.JSONLD(map[string]any{
		"@context": "https://schema.org", "@type": "WebPage",
		"name": "Frozen metadata stream companion", "identifier": "metadata-stream-jsonld-v1",
	})
	if err != nil {
		return nextgo.Page{}, err
	}
	resource := &countedStylesheet{}
	return nextgo.Page{
		// Legacy Title/Description remain empty: derived fallback values must
		// not acquire legacy precedence over the resolved typed contribution.
		Metadata: seo.Metadata{
			Title: fallbackTitle, Description: fallbackDescription, AbsoluteTitle: true,
			Canonical: media.SiteURL + "/metadata-stream?phase=fallback",
			OpenGraph: seo.OpenGraph{Title: "Public metadata is loading", Description: fallbackDescription, Type: "website"},
			Twitter:   seo.Twitter{Card: "summary", Title: "Public metadata is loading", Description: fallbackDescription},
		},
		Stream: true, Resources: []assets.Resource{resource}, JSONLD: []seo.StructuredData{structured},
		DeferredMetadata: func(request *nextgo.Context, _ seo.Metadata) (seo.Metadata, error) {
			if resource.calls.Load() != 1 {
				return seo.Metadata{}, errors.New("metadata sample resource was not prepared exactly once")
			}
			value, err := loadValue(request.Context, settings)
			if err != nil {
				return seo.Metadata{}, err
			}
			contribution := value.metadata()
			switch settings.testMode {
			case "fail":
				return seo.Metadata{}, errors.New("private deferred metadata fixture diagnostic")
			case "invalid":
				contribution.Canonical = "javascript:metadata-fixture-invalid"
			case "readonly":
				// The ignored error must still invalidate this contribution. A
				// deferred loader cannot reopen response headers or set a cookie.
				_ = request.SetHeader("X-Metadata-Late", "must-not-appear")
			}
			return contribution, nil
		},
		Body: ui.El("section", ui.Attrs{"data-metadata-stream": "", "class": "metadata-stream-demo"},
			ui.El("h1", nil, ui.Text("Metadata can finish after the body.")),
			ui.El("p", nil, ui.Text("The initial document has complete fallback metadata. An opted-in browser response can later update the typed head while these existing Go controls and native input keep their DOM, state, and focus.")),
			ui.El("p", nil,
				ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/metadata-stream?cycle=next"), "data-metadata-refresh-link": "", "data-nextgo-prefetch": "false"}, ui.Text("Load another metadata stream")),
				ui.Text(" · "),
				ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/about"), "data-metadata-exit-link": "", "data-nextgo-prefetch": "false"}, ui.Text("Leave for Architecture"))),
			ui.El("label", ui.Attrs{"for": "metadata-stream-draft"}, ui.Text("Public local draft")),
			ui.El("input", ui.Attrs{"id": "metadata-stream-draft", "type": "text", "value": "Public draft", "autocomplete": "off", "maxlength": "80", "data-metadata-input": ""}),
			ui.El("div", ui.Attrs{"data-metadata-counter": ""}, counter.View(counter.Props{Label: "Metadata stream counter", Initial: 3})),
			ui.El("p", ui.Attrs{"class": "muted small"}, ui.Text("Only typed SEO metadata changes. The custom stylesheet and one JSON-LD companion remain the same head nodes. This route stores no private account data or pending writes.")),
		),
	}, nil
}
