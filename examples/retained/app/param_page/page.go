//go:build !js

package param_page

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/retained/app"
)

func Page(c *nextgo.Context) (nextgo.Page, error) { return app.Render(c, c.Params["page"]) }
