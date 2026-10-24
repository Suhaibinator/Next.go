//go:build !js

package settings

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(*nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Settings board — Next.go", Body: ui.El("p", nil, ui.Text("Primary settings page"))}, nil
}
