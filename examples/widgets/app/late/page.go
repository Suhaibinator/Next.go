//go:build !js

package late

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/widgets/widgetpage"
)

func Page(c *nextgo.Context) (nextgo.Page, error) { return widgetpage.View(c, false, "late") }
