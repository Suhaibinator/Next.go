//go:build !js

package lazyprops

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/merge/components/lazyordinary"
)

func Page(c *nextgo.Context) (nextgo.Page, error) {
	query := c.Query.Get("q")
	return nextgo.Page{Title: "Lazy ordinary props · " + query, Retention: &nextgo.RouteRetention{Partition: "public-lazy-source"}, Body: lazyordinary.View(lazyordinary.Props{Query: query, Caption: "Fresh lazy ordinary source query " + query, Initial: 2})}, nil
}
