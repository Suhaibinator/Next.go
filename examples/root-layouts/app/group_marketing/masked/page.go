//go:build !js

package masked

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(*nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Unmasked marketing", Body: ui.Text("Without the query rewrite, this route belongs to marketing.")}, nil
}
