//go:build !js

package partial

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/partialdata"
)

func Prerender() *nextgo.PrerenderPolicy { return partialdata.Prerender() }
