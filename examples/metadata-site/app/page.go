//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	var links []ui.Node
	for _, name := range []string{"robots.txt", "sitemap.xml", "favicon.ico", "icon.png", "apple-icon.png", "manifest.webmanifest", "opengraph-image.png", "twitter-image.png", "opengraph-image.alt.txt", "twitter-image.alt.txt"} {
		links = append(links, ui.El("li", nil, ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/"+name), "data-nextgo-reload": "true"}, ui.Text(name))))
	}
	return nextgo.Page{Title: "Public metadata from Go", Body: ui.El("article", nil,
		ui.El("h1", nil, ui.Text("Small files. Clear ownership.")),
		ui.El("p", ui.Attrs{"class": "lead"}, ui.Text("These crawler, icon and manifest resources belong to the application root. Go snapshots public startup producers and original raster files before serving any visitor.")),
		ui.El("ul", ui.Attrs{"aria-label": "Public metadata files"}, links...),
		ui.El("p", nil, ui.Text("Inspect the document head for default icons, manifest links and generated social images. Follow the pages above to inherit, replace, or explicitly clear those defaults while the Go counter retains its state.")),
		ui.El("figure", nil, ui.El("img", ui.Attrs{"src": nextgo.MustURL(c, "/opengraph-image.png"), "alt": "Original Go bitmap social card", "width": "1200", "height": "630", "data-social-metadata-preview": ""}), ui.El("figcaption", nil, ui.Text("This PNG is composed once at startup and published unchanged by static export. It uses an original ASCII bitmap face, not general font shaping or CSS layout."))),
		ui.El("figure", nil, ui.El("img", ui.Attrs{"src": nextgo.MustURL(c, "/twitter-image.png"), "alt": "Original Go bitmap Twitter card", "width": "1200", "height": "630", "data-social-metadata-twitter": ""}), ui.El("figcaption", nil, ui.Text("A separate Go producer supplies the Twitter card; its public alt-text file also enters the frozen metadata inventory."))),
		ui.El("p", nil, ui.Text("The manifest is browser metadata, not an offline application. https://example.test is an illustrative public origin; change it before publishing.")),
	)}, nil
}
