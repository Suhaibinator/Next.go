//go:build !js

package analytics

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func NotFound(*nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Body: ui.El("p", nil, ui.Text("Analytics is unavailable for this selection."))}, nil
}
