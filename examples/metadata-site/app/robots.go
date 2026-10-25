//go:build !js

package app

import (
	"context"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/seo"
)

// Robots runs at startup with the global mount, not a visitor's host or locale.
func Robots(ctx context.Context) (seo.RobotsOptions, error) {
	root, err := nextgo.URL(ctx, "/")
	if err != nil {
		return seo.RobotsOptions{}, err
	}
	sitemap, err := nextgo.URL(ctx, "/sitemap.xml")
	if err != nil {
		return seo.RobotsOptions{}, err
	}
	return seo.RobotsOptions{
		Rules:    []seo.RobotsRule{{UserAgents: []string{"*"}, Allow: []string{root}}},
		Sitemaps: []string{PublicOrigin + sitemap},
	}, ctx.Err()
}
