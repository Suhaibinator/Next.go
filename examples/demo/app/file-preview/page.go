//go:build !js

package filepreview

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/fileform"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Local Go file reader — Next.go", Body: fileform.View(fileform.Props{Title: "Local files", Prefix: "file-preview", Local: true, Target: nextgo.MustURL(c, "/about")})}, nil
}
