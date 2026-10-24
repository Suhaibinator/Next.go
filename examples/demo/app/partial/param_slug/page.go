//go:build !js

package partialslug

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/partialdata"
)

func Page(c *nextgo.Context) (nextgo.Page, error) { return partialdata.Page(c, c.Params["slug"]) }
