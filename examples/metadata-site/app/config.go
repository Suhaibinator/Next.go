//go:build !js

package app

//go:generate go run ../tools/icons -out .

import (
	"os"

	nextgo "github.com/Suhaibinator/Next.go"
)

// PublicOrigin is illustrative public metadata, never an incoming Host value.
const PublicOrigin = "https://example.test"

func Configure(c *nextgo.Config) {
	c.PreserveLayouts = true
	c.BasePath = os.Getenv("NEXTGO_METADATA_BASE_PATH")
	c.AssetPrefix = os.Getenv("NEXTGO_METADATA_ASSET_PREFIX")
}
