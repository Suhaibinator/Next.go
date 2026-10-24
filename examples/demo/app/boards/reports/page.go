//go:build !js

package reports

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/counter"
)

func Page(*nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Reports board — Next.go", Body: counter.View(counter.Props{Initial: 21, Label: "Report page"})}, nil
}
