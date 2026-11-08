//go:build !js

package settings

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(*nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Workspace settings", Body: ui.Text("Settings share the workspace document root.")}, nil
}
