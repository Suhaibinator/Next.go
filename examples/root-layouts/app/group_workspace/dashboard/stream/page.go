//go:build !js

package stream

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(*nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Streamed workspace", Stream: true, Body: ui.Text("Streamed body in the workspace root.")}, nil
}
