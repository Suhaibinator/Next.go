//go:build !js

package partial

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/partialdata"
)

func Page(c *nextgo.Context) (nextgo.Page, error) { return partialdata.Page(c, "root") }
