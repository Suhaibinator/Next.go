//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"os"
)

func Configure(c *nextgo.Config) {
	c.PreserveLayouts = true
	c.BasePath = os.Getenv("NEXTGO_METADATA_BASE_PATH")
	c.AssetPrefix = os.Getenv("NEXTGO_METADATA_ASSET_PREFIX")
	// Plain static hosting has no query-based request routing. Its document
	// root boundaries are the same, with this server-only demonstration absent.
	if c.Phase != nextgo.PhaseStaticExport {
		c.Public = map[string]string{"rootRewrite": "enabled"}
		c.Rewrites = []nextgo.RewriteRule{{Source: "/masked", Destination: "/dashboard", Has: []nextgo.RuleCondition{{Type: nextgo.QueryCondition, Key: "other", Value: "1"}}}}
	}
}
