//go:build !js

package formpair

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/profileform"
	"github.com/Suhaibinator/Next.go/examples/demo/profiles"
	"github.com/Suhaibinator/Next.go/ui"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	first := profiles.Profile{ID: "first-preview", Version: "0", Name: "First model", Email: "first@example.test", Role: "reader"}
	second := first
	second.ID = "second-preview"
	second.Name = "Second model"
	target := nextgo.MustURL(c, "/about")
	return nextgo.Page{Title: "Independent Go forms — Next.go", Body: ui.Fragment(profileform.View(profileform.Props{Profile: first, Target: target, Local: true}), profileform.View(profileform.Props{Profile: second, Target: target, Local: true}))}, nil
}
