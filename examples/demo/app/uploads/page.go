//go:build !js

package uploadspage

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/fileform"
	"github.com/Suhaibinator/Next.go/examples/demo/mutations"
	"github.com/Suhaibinator/Next.go/examples/demo/uploads"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	receipt := uploads.Receipt{}
	title := "Public demo files"
	if id := c.Query.Get("receipt"); id != "" {
		var ok bool
		var err error
		receipt, ok, err = uploads.Get(c.Context, id)
		if err != nil {
			return nextgo.Page{}, err
		}
		if !ok {
			return nextgo.Page{}, nextgo.ErrNotFound
		}
		title = receipt.Title
	}
	if err := c.SetHeader("Cache-Control", "private, no-store"); err != nil {
		return nextgo.Page{}, err
	}
	return nextgo.Page{Title: "Go file uploads — Next.go", Body: fileform.View(fileform.Props{Title: title, Receipt: receipt, Prefix: "uploads", JSON: c.Query.Get("transport") == "json", Action: nextgo.MustURL(c, mutations.UploadFiles.Path()), Target: nextgo.MustURL(c, "/about")})}, nil
}
