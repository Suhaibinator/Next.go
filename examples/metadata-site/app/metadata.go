//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/seo"
)

func Metadata(_ *nextgo.Context, _ seo.Metadata) (seo.Metadata, error) {
	return seo.Metadata{BaseURL: PublicOrigin, Title: "Metadata field notes", TitleTemplate: "%s — Metadata field notes", Description: "Public metadata is explicit, frozen at startup, and shared across pages."}, nil
}
