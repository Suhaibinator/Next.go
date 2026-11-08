//go:build !js

package localfontspage

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/assets"
	"github.com/Suhaibinator/Next.go/examples/demo/localfonts"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	set := localfonts.Set()
	manifest := set.Manifest()
	return nextgo.Page{Title: "Frozen local fonts — Next.go",
		Description: "Native public font snapshots with matching immutable file and stylesheet URLs.",
		Resources:   []assets.Resource{set.Stylesheet(), manifest.Faces[0].Preload(), manifest.Faces[1].Preload(), assets.Stylesheet("/assets/local-fonts.css")},
		Body: ui.El("article", ui.Attrs{"class": "prose", "data-local-fonts-page": ""},
			ui.El("h1", nil, ui.Text("Font bytes and declarations, frozen together.")),
			ui.El("p", nil, ui.Text("Go snapshots two local font files and publishes content-addressed bytes plus a stylesheet. These original fixtures contain only G, O and a space; their purpose is delivery and decoding, not production typography.")),
			ui.El("p", ui.Attrs{"class": "font-fixture font-fixture-woff2", "data-font-woff2": "", "aria-label": "Original WOFF2 font sample"}, ui.Text("GO GO")),
			ui.El("p", ui.Attrs{"class": "font-fixture font-fixture-woff", "data-font-woff": "", "aria-label": "Original WOFF font sample"}, ui.Text("GO GO")),
			ui.El("p", nil, ui.Text("Font URLs derive from bytes. Source edits cannot change a running snapshot; constructing a new snapshot gives edited bytes a new URL. Head links preload only these deliberately selected faces.")),
			ui.El("p", nil, ui.Text("Managed declarations publish the same hashed files through native HTTP, Lambda and static export. Automatic downloading, subsetting and measured fallback metrics remain open.")),
			ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/about"), "data-local-fonts-exit": "", "data-nextgo-prefetch": "false"}, ui.Text("Leave the font sample")))}, nil
}
