//go:build !js

package plainprops

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/merge/components/ordinary"
)

// Deliberately identical SSR/DTO across query and Refresh. An ordinary source
// is initial-only, so a fresh document frame must cold Mount this boundary;
// the older whole-route fingerprint must never retain its old source handle.
func Page(*nextgo.Context) (nextgo.Page, error) {
	return nextgo.Page{Title: "Ordinary initial-only props", Retention: &nextgo.RouteRetention{Partition: "public-ordinary-source"}, Body: ordinary.View(ordinary.Props{Caption: "Identical ordinary props and server body", InitialSource: true})}, nil
}
