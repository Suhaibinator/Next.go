//go:build !js

package override

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(*nextgo.Context) (nextgo.Page, error) {
	manifest := "/manifest.webmanifest?explicit=1"
	return nextgo.Page{Title: "Explicit metadata", Metadata: seo.Metadata{
		Icons: []seo.Icon{{URL: "/apple-icon.png?explicit=1", Rel: "icon", Type: "image/png", Sizes: "180x180"}}, Manifest: &manifest,
	}, Body: ui.El("article", nil, ui.El("h1", nil, ui.Text("An explicit choice wins.")), ui.El("p", nil, ui.Text("This page replaces inherited icon defaults with the larger original raster and supplies an explicit manifest link. Query markers make the override easy to inspect; the endpoint still serves the same frozen public bytes.")))}, nil
}
