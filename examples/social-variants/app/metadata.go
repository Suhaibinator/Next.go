//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/seo"
)

func Metadata(*nextgo.Context, seo.Metadata) (seo.Metadata, error) {
	return seo.Metadata{BaseURL: "https://example.test", TitleTemplate: "%s — Original formats"}, nil
}
