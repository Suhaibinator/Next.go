//go:build !js

package partial

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/partialdata"
)

func Regions() map[string]nextgo.RegionFunc { return partialdata.Regions() }
