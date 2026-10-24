//go:build !js

package fieldpreview

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/profileform"
	"github.com/Suhaibinator/Next.go/examples/demo/profiles"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Local Go field preview — Next.go", Body: profileform.View(profileform.Props{Profile: profiles.Profile{ID: "local-preview", Version: "0", Name: "Ada Lovelace", Email: "ada@example.test", Role: "reader"}, Target: nextgo.MustURL(c, "/about"), ValidateBlur: true, Local: true})}, nil
}
