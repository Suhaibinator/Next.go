//go:build !js

package post

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
	"independent.example/blog/internal/posts"
	"independent.example/blog/internal/state"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	post, ok, err := posts.Find(c, c.Params["slug"])
	if err != nil {
		return nextgo.Page{}, err
	}
	if !ok {
		return nextgo.Page{}, nextgo.ErrNotFound
	}
	return nextgo.Page{Title: "Blog: " + post.Slug, Body: ui.El("article", ui.Attrs{"data-independent-post": post.Slug}, ui.El("h1", nil, ui.Text(post.Title)), ui.El("p", nil, ui.Text(post.Body)), ui.El("p", nil, ui.Text("Navigation source: "), ui.El("output", ui.Attrs{"data-independent-query": ""}, ui.Text(c.Query.Get("source")))), ui.El("p", nil, ui.Text("Native implementation: "+state.NativeVersion)), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/")}, ui.Text("Return to blog")))}, nil
}
