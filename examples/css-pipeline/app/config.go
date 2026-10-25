//go:build !js

package app

import nextgo "github.com/Suhaibinator/Next.go"

func Configure(config *nextgo.Config) {
	// Static hosts publish their own response headers. The exporter selects
	// this native startup phase before calling Configure.
	if config.Phase == nextgo.PhaseStaticExport {
		return
	}
	// This example keeps a stable filename, so every browser reuse must
	// revalidate the stylesheet after a palette rebuild or deployment.
	config.HeaderRules = append(config.HeaderRules, nextgo.HeaderRule{
		Source:  "/assets/style.css",
		Headers: []nextgo.HeaderValue{{Name: "Cache-Control", Value: "no-cache, must-revalidate"}},
	})
}
