//go:build !js

package choicepair

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/choices"
	"github.com/Suhaibinator/Next.go/examples/demo/components/choiceform"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	target := nextgo.MustURL(c, "/about")
	return nextgo.Page{Title: "Independent radio groups — Next.go", Body: ui.Fragment(choiceform.View(choiceform.Props{Values: choices.Default(), Prefix: "choice-first", Local: true, Target: target}), choiceform.View(choiceform.Props{Values: choices.Default(), Prefix: "choice-second", Local: true, Target: target}))}, nil
}
