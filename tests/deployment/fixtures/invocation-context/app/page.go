//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

// A generated-server readiness route, without any identity capture or callback.
func Page(*nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Invocation identity fixture", Body: ui.Text("identity executable ready")}, nil
}
