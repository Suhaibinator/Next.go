//go:build !js

package formspage

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/profileform"
	"github.com/Suhaibinator/Next.go/examples/demo/mutations"
	"github.com/Suhaibinator/Next.go/examples/demo/profiles"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	id := c.Query.Get("id")
	if id == "" {
		id = "demo"
	}
	if !profiles.ValidID(id) {
		return nextgo.Page{}, nextgo.ErrNotFound
	}
	p, err := profiles.Get(c.Context, id)
	if err != nil {
		return nextgo.Page{}, err
	}
	if err := c.SetHeader("Cache-Control", "private, no-store"); err != nil {
		return nextgo.Page{}, err
	}
	return nextgo.Page{Title: "Controlled Go forms — Next.go", Body: profileform.View(profileform.Props{Profile: p, Action: nextgo.MustURL(c, mutations.SaveProfile.Path()), Target: nextgo.MustURL(c, "/about"), ValidateBlur: c.Query.Get("validate") == "blur", PanicHandler: c.Query.Get("mode") == "handler-panic", Mismatch: c.Query.Get("mode") == "mismatch"})}, nil
}
