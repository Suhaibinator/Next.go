//go:build !js

package details

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/zones/site"
)

func Page(c *nextgo.Context) (nextgo.Page, error) { return site.Details(c, "blog") }
