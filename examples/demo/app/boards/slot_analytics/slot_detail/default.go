//go:build !js

package detail

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/counter"
)

func Default(*nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Body: counter.View(counter.Props{Initial: 11, Label: "Detail default"})}, nil
}
