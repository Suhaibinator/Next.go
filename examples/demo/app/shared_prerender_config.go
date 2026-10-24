//go:build !js

package app

import (
	"os"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/actions"
	"github.com/Suhaibinator/Next.go/cache/shared"
)

func configureSharedPrerenderActions(router *actions.Router) {
	for _, entry := range []struct{ name, kind string }{{"shared-prerender-tag-expire", "tag"}, {"shared-prerender-path-expire", "prefix"}} {
		router.Handle(entry.name, func(c *actions.Context) (actions.Result, error) {
			if os.Getenv("NEXTGO_DEMO_SHARED_PRERENDER") != "true" {
				return actions.Result{}, &actions.ValidationError{Message: "Shared prerendering is not configured."}
			}
			values, err := c.ParseForm()
			if err != nil {
				return actions.Result{}, err
			}
			slug := values.Get("slug")
			valid := len(slug) > 0 && len(slug) <= 64
			for _, r := range slug {
				if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
					valid = false
				}
			}
			if !valid || len(values) != 1 || len(values["slug"]) != 1 || c.Request.MultipartForm != nil && len(c.Request.MultipartForm.File) > 0 {
				return actions.Result{}, &actions.ValidationError{Message: "Choose a valid prerender slug."}
			}
			selector := shared.Selector{Kind: entry.kind, Value: "shared-prerender"}
			if entry.kind == "prefix" {
				selector.Value = "/shared-prerender/" + slug
			}
			if err := nextgo.ExpireCache(c.Context, selector); err != nil {
				return actions.Result{}, err
			}
			return actions.Result{Redirect: "/shared-prerender/" + slug}, nil
		})
	}
}
