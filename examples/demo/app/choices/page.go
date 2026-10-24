//go:build !js

package choicespage

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/choices"
	"github.com/Suhaibinator/Next.go/examples/demo/components/choiceform"
	"github.com/Suhaibinator/Next.go/examples/demo/mutations"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	values := choices.Default()
	if c.Query.Has("applied") {
		values = choices.Preferences{Channel: c.Query.Get("channel"), Topics: c.Query["topics"]}
		if len(c.Query["channel"]) != 1 || len(choices.Validate(values)) > 0 {
			return nextgo.Page{}, nextgo.ErrNotFound
		}
	}
	if err := c.SetHeader("Cache-Control", "private, no-store"); err != nil {
		return nextgo.Page{}, err
	}
	return nextgo.Page{Title: "Go choice controls — Next.go", Body: choiceform.View(choiceform.Props{Values: values, Prefix: "choices", Action: nextgo.MustURL(c, mutations.PreviewChoices.Path()), Target: nextgo.MustURL(c, "/about")})}, nil
}
