//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/zones/site"
)

func Configure(c *nextgo.Config) { site.Configure(c, "marketing") }
