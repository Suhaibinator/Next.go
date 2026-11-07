//go:build !js

package product

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/merge/app"
)

func Page(c *nextgo.Context) (nextgo.Page, error) { return app.ProductPage(c, c.Params["id"]) }
