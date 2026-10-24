//go:build !js

package analytics

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Error(*nextgo.Context, error) (nextgo.Page, error) {
	return nextgo.Page{Body: ui.El("p", ui.Attrs{"role": "alert"}, ui.Text("Analytics could not be loaded. Navigate again to retry."))}, nil
}
