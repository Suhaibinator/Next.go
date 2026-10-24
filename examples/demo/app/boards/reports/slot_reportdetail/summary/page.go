//go:build !js

package summary

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/components/counter"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	if c.Request.Header.Get("X-Demo-Report-Summary-Deny") == "yes" {
		return nextgo.Page{}, nextgo.ErrForbidden
	}
	return nextgo.Page{Body: counter.View(counter.Props{Initial: 41, Label: "Report summary"})}, nil
}
