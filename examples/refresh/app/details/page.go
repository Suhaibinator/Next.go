//go:build !js

package details

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Refresh boundaries", Body: ui.El("article", ui.Attrs{"data-refresh-details": ""}, ui.El("h1", nil, ui.Text("Explicit boundaries make refresh reviewable.")), ui.El("p", nil, ui.Text("This page has no lazy counter. Native rendering, shared code, props, schema, eager code and environment changes keep the normal full rebuild/reload path. Production and static builds keep ordinary lazy component behavior without development refresh.")), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/")}, ui.Text("Return to stateful counters")))}, nil
}
