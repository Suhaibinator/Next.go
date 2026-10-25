//go:build !js

package app

import (
	"os"

	nextgo "github.com/Suhaibinator/Next.go"
)

func Configure(config *nextgo.Config) {
	// Static hosts publish their own response headers. This explicit example
	// deployment mode lets export proceed without a native-only header rule.
	if os.Getenv("NEXTGO_CSS_STATIC") == "true" {
		return
	}
	// This example keeps a stable filename, so every browser reuse must
	// revalidate the stylesheet after a palette rebuild or deployment.
	config.HeaderRules = append(config.HeaderRules, nextgo.HeaderRule{
		Source:  "/assets/style.css",
		Headers: []nextgo.HeaderValue{{Name: "Cache-Control", Value: "no-cache, must-revalidate"}},
	})
}
