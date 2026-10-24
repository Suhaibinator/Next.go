//go:build !js

package posts

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/seo"
)

func Metadata(c *nextgo.Context, _ seo.Metadata) (seo.Metadata, error) {
	canonical, err := nextgo.URL(c, "/posts")
	if err != nil {
		return seo.Metadata{}, err
	}
	return seo.Metadata{Description: "Server-rendered field notes with inherited metadata.", Canonical: canonical, OpenGraph: seo.OpenGraph{Type: "website", SiteName: "Next.go field notes"}}, nil
}
