//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/media"
	"github.com/Suhaibinator/Next.go/seo"
)

// Metadata establishes a trusted public origin. It does not derive URLs from
// the incoming Host header; replace the illustrative SiteURL for deployment.
func Metadata(_ *nextgo.Context, _ seo.Metadata) (seo.Metadata, error) {
	return seo.Metadata{BaseURL: media.SiteURL, Title: "Next.go", TitleTemplate: "%s — Next.go"}, nil
}
