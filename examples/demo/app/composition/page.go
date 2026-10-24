//go:build !js

package compositionpage

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/composition"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Go component composition — Next.go", Body: composition.View(composition.Props{Initial: 2, Target: nextgo.MustURL(c, "/composition/details"), Fail: c.Query.Get("fail") == "1"})}, nil
}
