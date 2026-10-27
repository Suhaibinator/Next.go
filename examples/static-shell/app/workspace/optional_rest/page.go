//go:build !js

package workspacepage

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/static-shell/components/workspace"
	"github.com/Suhaibinator/Next.go/examples/static-shell/components/workspacelazy"
	"github.com/Suhaibinator/Next.go/examples/static-shell/settings"
)

// Page never reads Params, Query, headers, cookies or application data. Its head,
// fallback and props are the same public seed for every request to this route.
// There is intentionally no StaticParams enumerator: export adds the zero-tail
// seed from the explicit declaration, without exporting the linked deep URLs.
func Page(c *nextgo.Context) (nextgo.Page, error) {
	view := workspace.View
	if settings.Component() == "workspacelazy" {
		view = workspacelazy.View
	}
	return nextgo.Page{
		Title:       "Public workspace — Go static shell",
		Description: "A generic public workspace fallback with explicit browser rendering in Go.",
		Body:        view(c),
	}, nil
}
