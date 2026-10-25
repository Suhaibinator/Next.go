//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	var links []ui.Node
	for _, name := range []string{"robots.txt", "sitemap.xml", "favicon.ico", "icon.png", "apple-icon.png", "manifest.webmanifest"} {
		links = append(links, ui.El("li", nil, ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/"+name), "data-nextgo-reload": "true"}, ui.Text(name))))
	}
	return nextgo.Page{Title: "Public metadata from Go", Body: ui.El("article", nil,
		ui.El("h1", nil, ui.Text("Small files. Clear ownership.")),
		ui.El("p", ui.Attrs{"class": "lead"}, ui.Text("These crawler, icon and manifest resources belong to the application root. Go snapshots public startup producers and original raster files before serving any visitor.")),
		ui.El("ul", ui.Attrs{"aria-label": "Public metadata files"}, links...),
		ui.El("p", nil, ui.Text("Inspect the document head for default icon and manifest links. Follow the pages above to inherit, replace, or explicitly clear those defaults while the Go counter retains its state.")),
		ui.El("p", nil, ui.Text("The manifest is browser metadata, not an offline application. https://example.test is an illustrative public origin; change it before publishing.")),
	)}, nil
}
