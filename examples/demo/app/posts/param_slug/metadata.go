//go:build !js

package slug

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/data"
	"github.com/Suhaibinator/Next.go/seo"
	"net/url"
)

func Metadata(c *nextgo.Context, _ seo.Metadata) (seo.Metadata, error) {
	post, err := data.FindForRequest(c.Context, c.Params["slug"])
	if err != nil {
		return seo.Metadata{}, err
	}
	canonical, err := nextgo.URL(c, "/posts/"+url.PathEscape(post.Slug))
	if err != nil {
		return seo.Metadata{}, err
	}
	return seo.Metadata{Title: post.Title, Description: post.Summary, Canonical: canonical, OpenGraph: seo.OpenGraph{Title: post.Title, Description: post.Summary, URL: canonical, Type: "article"}}, nil
}
