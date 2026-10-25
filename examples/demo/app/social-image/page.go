//go:build !js

package socialimagepage

import (
	"net/url"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/assets"
	"github.com/Suhaibinator/Next.go/examples/demo/media"
	"github.com/Suhaibinator/Next.go/examples/demo/socialcard"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	if err := c.RequireServer(); err != nil {
		return nextgo.Page{}, err
	}
	title, description, err := socialcard.Query(c.Query)
	if err != nil {
		title, description = socialcard.Title, socialcard.Description
	}
	query := url.Values{"title": {title}, "description": {description}}.Encode()
	imageURL := nextgo.MustURL(c, "/api/social?"+query)
	return nextgo.Page{
		Resources: []assets.Resource{assets.Stylesheet("/assets/social-image.css")},
		Title:     "Social images in Go", Description: "A bounded, server-rendered PNG social card composed with typed Go layouts.",
		Metadata: seo.Metadata{
			OpenGraph: seo.OpenGraph{Title: title, Description: description, Type: "website", Images: []seo.Image{{URL: media.SiteURL + imageURL, Alt: title, Width: 1200, Height: 630}}},
			Twitter:   seo.Twitter{Card: "summary_large_image", Images: []string{media.SiteURL + imageURL}},
		},
		Body: ui.El("article", ui.Attrs{"class": "prose", "data-social-image-example": ""},
			ui.El("p", ui.Attrs{"class": "eyebrow accent"}, ui.Text("GO COMPOSITION / PNG RESPONSE")),
			ui.El("h1", nil, ui.Text("Useful words. Shareable pixels.")),
			ui.El("p", ui.Attrs{"class": "lead"}, ui.Text("This 1200 x 630 card is composed on the server using typed rows, columns, panels and bitmap text. Edit the public title and description to generate another image.")),
			ui.El("figure", nil,
				ui.El("img", ui.Attrs{"src": imageURL, "alt": title + " — " + description, "width": "1200", "height": "630"}),
				ui.El("figcaption", nil, ui.Text("Original 5x7 bitmap font: printable ASCII, lowercase rendered as uppercase. This is a deliberate pixel style with no Unicode shaping or CSS layout."))),
			ui.El("form", ui.Attrs{"method": "get", "action": nextgo.MustURL(c, "/social-image")},
				ui.El("p", nil, ui.El("label", ui.Attrs{"for": "social-title"}, ui.Text("Title (printable ASCII)")), ui.El("br", nil), ui.El("input", ui.Attrs{"id": "social-title", "name": "title", "value": title, "maxlength": "96", "required": ""})),
				ui.El("p", nil, ui.El("label", ui.Attrs{"for": "social-description"}, ui.Text("Description (printable ASCII)")), ui.El("br", nil), ui.El("input", ui.Attrs{"id": "social-description", "name": "description", "value": description, "maxlength": "180", "required": ""})),
				ui.El("button", ui.Attrs{"type": "submit", "class": "button primary"}, ui.Text("Generate card"))),
			ui.El("p", nil, ui.El("a", ui.Attrs{"href": imageURL, "data-nextgo-reload": "true"}, ui.Text("Open the generated PNG"))),
			ui.El("p", ui.Attrs{"class": "small muted"}, ui.Text("PNG responses use bounded process-local caching and ETags. This public example needs a server; plain static exports do not provide its API endpoint. Metadata uses the illustrative https://example.com origin; replace it before publishing."))),
	}, nil
}
