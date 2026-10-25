//go:build !js

// Package posts is this module's local, public read-only repository.
package posts

import "context"

type Post struct{ Slug, Title, Body string }

var records = map[string]Post{
	"welcome":   {Slug: "welcome", Title: "Blog field note: welcome", Body: "These original public notes are loaded by the independently built blog module."},
	"ownership": {Slug: "ownership", Title: "Blog field note: ownership", Body: "A missing blog record remains a blog 404. It does not fall through to a peer application's routes."},
}

func Find(ctx context.Context, slug string) (Post, bool, error) {
	if err := ctx.Err(); err != nil {
		return Post{}, false, err
	}
	post, ok := records[slug]
	return post, ok, nil
}
