//go:build !js

package posts

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/data"
	"github.com/Suhaibinator/Next.go/examples/demo/routes"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	if err := data.Preview(c); err != nil {
		return nextgo.Page{}, err
	}
	posts, err := data.AllForRequest(c)
	if err != nil {
		return nextgo.Page{}, err
	}
	cards := []ui.Node{}
	for _, p := range posts {
		path, err := routes.PostsBySlug(routes.PostsBySlugParams{Slug: p.Slug})
		if err != nil {
			return nextgo.Page{}, err
		}
		cards = append(cards, ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, path), "class": "post-card"}, ui.El("p", ui.Attrs{"class": "eyebrow"}, ui.Text("ENGINEERING / GO")), ui.El("h2", nil, ui.Text(p.Title+" ↗")), ui.El("p", ui.Attrs{"class": "muted"}, ui.Text(p.Summary))))
	}
	return nextgo.Page{Title: "Field notes — Next.go", Description: "Notes on HTML, Go islands, and file-based routing.", Body: ui.Fragment(ui.El("div", ui.Attrs{"class": "notes-heading"}, ui.El("h1", nil, ui.Text("A few field notes.")), ui.El("p", ui.Attrs{"class": "lead"}, ui.Text("Loaded on the server. Ready when the HTML arrives."))), ui.El("div", ui.Attrs{"class": "post-grid"}, cards...), ui.El("p", ui.Attrs{"class": "muted small"}, ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/posts?delay=750ms")}, ui.Text("Try a slow navigation")), ui.Text(" · "), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/posts?fail=1")}, ui.Text("Try a server error"))))}, nil
}
