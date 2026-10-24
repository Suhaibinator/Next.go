//go:build !js

package media

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/assets"
	demomedia "github.com/Suhaibinator/Next.go/examples/demo/media"
	"github.com/Suhaibinator/Next.go/images"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	canonical := demomedia.SiteURL + nextgo.MustURL(c, "/media")
	structured, err := seo.JSONLD(map[string]any{"@context": "https://schema.org", "@type": "WebPage", "name": "Responsive media — Next.go", "url": canonical})
	if err != nil {
		return nextgo.Page{}, err
	}
	return nextgo.Page{
		JSONLD:    []seo.StructuredData{structured},
		Resources: []assets.Resource{assets.Stylesheet("/assets/media.css")},
		Metadata: seo.Metadata{
			Title: "Responsive media — Next.go", AbsoluteTitle: true,
			Description: "Local image optimization and structured metadata rendered by Go.",
			Canonical:   canonical,
			Robots:      "index, follow",
			OpenGraph:   seo.OpenGraph{Title: "Responsive media — Next.go", Description: "A local image transformed by Go, with a responsive srcset and intrinsic dimensions.", URL: canonical, Type: "website", SiteName: "Next.go demo", Images: []seo.Image{{URL: demomedia.SiteURL + nextgo.MustURL(c, "/_nextgo/image?src=%2Fdemo.png&w=960&q=75"), Width: 960, Height: 540, Alt: "Smooth teal and green waves"}}},
			Twitter:     seo.Twitter{Card: "summary_large_image", Title: "Responsive media — Next.go", Images: []string{demomedia.SiteURL + nextgo.MustURL(c, "/_nextgo/image?src=%2Fdemo.png&w=960&q=75")}},
		},
		Body: ui.El("article", ui.Attrs{"class": "prose"},
			ui.El("p", ui.Attrs{"class": "eyebrow accent"}, ui.Text("LOCAL ASSETS / STRUCTURED HEAD")),
			ui.El("h1", nil, ui.Text("Useful pixels.\nMeaningful metadata.")),
			ui.El("p", ui.Attrs{"class": "lead"}, ui.Text("The browser chooses an image width. Go transforms a local, embedded source and caches the encoded result.")),
			ui.El("figure", nil,
				demomedia.Handler().Image(images.ImageOptions{Endpoint: nextgo.MustURL(c, "/_nextgo/image"), Src: "/demo.png", Alt: "Smooth teal and green waves", Width: 960, Height: 540, Sizes: "(max-width: 750px) 100vw, 750px", Class: "responsive-media", Priority: true}),
				ui.El("figcaption", ui.Attrs{"class": "small muted"}, ui.Text("Original procedural artwork. PNG input, responsive PNG output; no remote image fetching."))),
			ui.El("h2", ui.Attrs{"data-resource-example": ""}, ui.Text("A document head you can inspect")),
			ui.El("p", nil, ui.Text("This heading uses a page-specific stylesheet registered through Page.Resources. Navigation adds the stylesheet and removes its head link when you leave.")),
			ui.El("p", nil, ui.Text("This page has a canonical URL, description, robots directive, Open Graph image, and Twitter card. The same structured metadata updates during client navigation.")),
			ui.El("p", nil, ui.Text("The demo uses https://example.com as an illustrative origin. Replace media.SiteURL with the public origin of your deployment before publishing.")),
			ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/sitemap.xml"), "data-nextgo-reload": "true"}, ui.Text("Inspect sitemap.xml →")),
			ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/robots.txt"), "data-nextgo-reload": "true"}, ui.Text("Inspect robots.txt →"))),
	}, nil
}
