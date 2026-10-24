//go:build !js

package choicepreview

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/choices"
	"github.com/Suhaibinator/Next.go/examples/demo/components/choiceform"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Local Go choices — Next.go", Body: choiceform.View(choiceform.Props{Values: choices.Default(), Prefix: "local-choices", Local: true, Target: nextgo.MustURL(c, "/about")})}, nil
}
