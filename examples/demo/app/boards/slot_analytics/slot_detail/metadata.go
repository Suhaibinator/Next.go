//go:build !js

package detail

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/seo"
)

func Metadata(c *nextgo.Context, _ seo.Metadata) (seo.Metadata, error) {
	if c.Request.Header.Get("X-Demo-Detail-Deny") == "yes" {
		return seo.Metadata{}, nextgo.ErrForbidden
	}
	return seo.Metadata{}, nil
}
