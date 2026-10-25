//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/zones/tenantdata"
)

func Configure(c *nextgo.Config) { tenantdata.Configure(c) }
