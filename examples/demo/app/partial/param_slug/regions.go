//go:build !js

package partialslug

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/partialdata"
)

func Regions() map[string]nextgo.RegionFunc { return partialdata.Regions() }
