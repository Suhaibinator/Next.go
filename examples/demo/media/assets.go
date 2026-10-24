//go:build !js

// Package media embeds the example's original artwork for standalone builds.
package media

import (
	"embed"
	nextgo "github.com/Suhaibinator/Next.go"
	"net/http"
	"strings"

	"github.com/Suhaibinator/Next.go/images"
	"github.com/Suhaibinator/Next.go/seo"
)

// SiteURL is an illustrative origin. Replace it with your deployment's public
// HTTPS origin before publishing crawler resources or canonical metadata.
const SiteURL = "https://example.com"

//go:embed demo.png
var assets embed.FS

var optimizer = func() *images.Handler {
	h, err := images.New(images.Config{FS: assets, Widths: []int{320, 640, 960}})
	if err != nil {
		panic(err) // All options above are compile-time example configuration.
	}
	return h
}()

// Handler is shared between rendering and HTTP serving so generated image URLs
// use the same endpoint and supported sizes as the optimizer.
func Handler() *images.Handler { return optimizer }

// Crawlers demonstrates the framework's sitemap and robots HTTP handlers.
func Crawlers() map[string]http.Handler { return CrawlersAt("") }
func CrawlersAt(base string) map[string]http.Handler {
	return CrawlersWithPaths(nextgo.PathConfig{BasePath: base})
}
func CrawlersWithPaths(deployment nextgo.PathConfig) map[string]http.Handler {
	publicURL := func(path string) string {
		value, err := deployment.URL(path)
		if err != nil {
			panic(err)
		}
		return value
	}
	paths := []string{"/", "/about", "/features", "/media", "/posts", "/posts/html-first", "/posts/go-islands", "/posts/routes-as-packages"}
	entries := make([]seo.SitemapEntry, 0, len(paths))
	for _, path := range paths {
		entries = append(entries, seo.SitemapEntry{URL: SiteURL + publicURL(path), ChangeFrequency: "weekly"})
	}
	sitemap, err := seo.Sitemap(entries)
	if err != nil {
		panic(err)
	}
	robots, err := seo.Robots(seo.RobotsOptions{Rules: []seo.RobotsRule{{Allow: []string{"/"}, Disallow: []string{strings.TrimRight(publicURL("/api"), "/") + "/", strings.TrimRight(publicURL("/_nextgo"), "/") + "/"}}}, Sitemaps: []string{SiteURL + publicURL("/sitemap.xml")}})
	if err != nil {
		panic(err)
	}
	return map[string]http.Handler{"/sitemap.xml": sitemap, "/robots.txt": robots}
}
