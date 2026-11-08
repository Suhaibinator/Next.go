//go:build !js

package escaped

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(*nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Escaped literal route", Body: ui.El("p", ui.Attrs{"data-private-literal-route": ""}, ui.Text("A literal_ directory exposes this intentional underscore URL."))}, nil
}
