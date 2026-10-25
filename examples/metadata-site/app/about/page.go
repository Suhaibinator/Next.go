//go:build !js

package about

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(*nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Inherited defaults", Body: ui.El("article", nil, ui.El("h1", nil, ui.Text("One application, shared defaults.")), ui.El("p", nil, ui.Text("This page declares no icon or manifest override. The same application-root resources remain in the document head during Go navigation. The layout counter remains mounted.")))}, nil
}
