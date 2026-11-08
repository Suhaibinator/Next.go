//go:build !js

package dashboard

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(*nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Workspace dashboard", Body: ui.El("h1", nil, ui.Text("Independent workspace root"))}, nil
}
