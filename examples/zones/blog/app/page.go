//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/zones/site"
)

func Page(c *nextgo.Context) (nextgo.Page, error) { return site.Page(c, "blog") }
