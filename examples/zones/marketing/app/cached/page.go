//go:build !js

package cached

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/zones/site"
)

func Page(c *nextgo.Context) (nextgo.Page, error) { return site.Cached(c, "marketing") }
