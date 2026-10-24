//go:build !js

package partialslug

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/partialdata"
)

func Prerender() *nextgo.PrerenderPolicy { return partialdata.Prerender() }
