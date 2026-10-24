//go:build !js

package details

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/app/templates"
)

func Page(c *nextgo.Context) (nextgo.Page, error) { return templates.Document(c), nil }
