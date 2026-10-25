//go:build !js

package app

import (
	"context"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/seo"
)

func Sitemap(ctx context.Context) ([]seo.SitemapEntry, error) {
	var entries []seo.SitemapEntry
	for _, path := range []string{"/", "/about", "/override", "/disabled"} {
		mounted, err := nextgo.URL(ctx, path)
		if err != nil {
			return nil, err
		}
		entries = append(entries, seo.SitemapEntry{URL: PublicOrigin + mounted, ChangeFrequency: "weekly"})
	}
	return entries, ctx.Err()
}
