//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Static social image formats", Body: ui.El("article", nil,
		ui.El("h1", nil, ui.Text("Original pixels. More formats.")),
		ui.El("p", nil, ui.Text("These original procedural JPEG and animated GIF files are frozen at startup and published unchanged by static export. Metadata uses a trusted illustrative origin; replace it before publishing.")),
		ui.El("figure", nil, ui.El("img", ui.Attrs{"src": nextgo.MustURL(c, "/opengraph-image.jpg"), "width": "320", "height": "180", "alt": "Original mint checkerboard JPEG", "data-variant-jpeg": ""}), ui.El("figcaption", nil, ui.Text("JPEG: one fully decoded bounded raster."))),
		ui.El("figure", nil, ui.El("img", ui.Attrs{"src": nextgo.MustURL(c, "/twitter-image.gif"), "width": "320", "height": "180", "alt": "Original two-frame mint checkerboard GIF", "data-variant-gif": ""}), ui.El("figcaption", nil, ui.Text("GIF: two original frames; frame count and aggregate raster work are checked before decoding."))),
		ui.El("p", nil, ui.Text("Use one image filename per metadata role. Conflicting file variants or a file plus a PNG Go producer reject the build rather than choosing silently.")))}, nil
}
