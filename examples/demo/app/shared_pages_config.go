//go:build !js

package app

import (
	"os"
	"time"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/actions"
	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/cache/shared"
)

func configureSharedPages(config *nextgo.Config) {
	endpoint := os.Getenv("NEXTGO_DEMO_SHARED_ENDPOINT")
	if endpoint == "" {
		return
	}
	backend, err := shared.NewHTTP(endpoint, os.Getenv("NEXTGO_CACHE_TOKEN"), nil)
	if err != nil {
		panic("invalid demo shared cache configuration")
	}
	config.SharedCache, err = shared.New(backend, "demo", "demo-pages-v1", shared.Options{})
	if err != nil {
		panic("invalid demo shared page scope")
	}
	config.CacheProfiles["demo-shared-pages"] = cache.Profile{Stale: 0, Revalidate: 15 * time.Second, Expire: time.Minute}
	for i := range config.Routes {
		if config.Routes[i].Pattern == "/shared-pages/:slug" {
			config.Routes[i].Cache = &nextgo.CachePolicy{Shared: true, Profile: "demo-shared-pages", Tags: []string{"shared-pages"}}
		}
	}
}
func configureSharedPageActions(router *actions.Router) {
	for _, entry := range []struct {
		name, kind string
		stale      bool
	}{{"shared-page-tag-expire", "tag", false}, {"shared-page-path-expire", "prefix", false}, {"shared-page-stale", "tag", true}} {
		router.Handle(entry.name, func(c *actions.Context) (actions.Result, error) {
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
				return actions.Result{}, &actions.ValidationError{Message: "Choose a valid page slug."}
			}
			selector := shared.Selector{Kind: entry.kind, Value: "shared-pages"}
			if entry.kind == "prefix" {
				selector.Value = "/shared-pages/" + slug
			}
			if entry.stale {
				profile, lookupErr := nextgo.LookupCacheProfile(c.Context, "demo-shared-pages")
				if lookupErr != nil {
					return actions.Result{}, &actions.ValidationError{Message: "Shared page caching is not configured."}
				}
				err = nextgo.MarkCacheStale(c.Context, selector, profile.Expire)
			} else {
				err = nextgo.ExpireCache(c.Context, selector)
			}
			if err != nil {
				return actions.Result{}, err
			}
			return actions.Result{Redirect: "/shared-pages/" + slug}, nil
		})
	}
}
