//go:build !js

package analytics

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/seo"
)

// This header is a deterministic test probe. The /restricted example contains
// the application's authorization example; slot visibility is not authorization.
func Metadata(c *nextgo.Context, _ seo.Metadata) (seo.Metadata, error) {
	if c.Request.Header.Get("X-Demo-Analytics-Metadata-Deny") == "yes" {
		return seo.Metadata{}, nextgo.ErrForbidden
	}
	return seo.Metadata{}, nil
}
