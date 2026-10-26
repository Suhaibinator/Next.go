//go:build !js

package metadataclear

import (
	nextgo "github.com/Suhaibinator/Next.go"
	metadatastream "github.com/Suhaibinator/Next.go/examples/demo/app/metadata-stream"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

// Page composes the existing native page factory: its bounded anonymous loader,
// counted stylesheet, frozen JSON-LD and Go controls keep the same ownership.
func Page(c *nextgo.Context) (nextgo.Page, error) {
	page, err := metadatastream.Page(c)
	if err != nil {
		return nextgo.Page{}, err
	}
	page.Metadata.Description = ""
	page.Metadata.Canonical = ""
	page.Metadata.Robots = ""
	load := page.DeferredMetadata
	page.DeferredMetadata = func(request *nextgo.Context, parent seo.Metadata) (seo.Metadata, error) {
		contribution, err := load(request, parent)
		if err != nil {
			return seo.Metadata{}, err
		}
		contribution.Title = finalTitle
		contribution.Description = ""
		contribution.Canonical = ""
		contribution.Robots = ""
		contribution.Clear = seo.ScalarClears{Description: true, Canonical: true, Robots: true}
		return contribution, nil
	}
	page.Body = ui.El("section", ui.Attrs{"data-metadata-clear": ""},
		ui.El("p", nil, ui.Text("This page inherits a description, canonical URL and robots policy. Its deferred contribution explicitly clears those three scalars while other typed social metadata resolves normally.")),
		ui.El("p", nil,
			ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/metadata-clear?cycle=next"), "data-metadata-clear-refresh-link": "", "data-nextgo-prefetch": "false"}, ui.Text("Clear inherited metadata again")),
			ui.Text(" · "),
			ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/about"), "data-metadata-clear-exit-link": "", "data-nextgo-prefetch": "false"}, ui.Text("Leave metadata clearing"))),
		page.Body,
	)
	return page, nil
}
