//go:build !js

package compositiondetails

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/composition"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Composition details — Next.go", Body: composition.View(composition.Props{Initial: 7, Target: nextgo.MustURL(c, "/composition")})}, nil
}
