//go:build !js

package marketing

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(*nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Marketing home", Body: ui.El("h1", nil, ui.Text("Independent marketing root"))}, nil
}
