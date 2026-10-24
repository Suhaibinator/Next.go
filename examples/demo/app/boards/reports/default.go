//go:build !js

package reports

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/counter"
)

func Default(*nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Default report — Next.go", Body: counter.View(counter.Props{Initial: 25, Label: "Report default"})}, nil
}
