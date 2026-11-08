//go:build !js

package app

//go:generate go run ../tools/icons -out .

import (
	"os"
	"strconv"

	nextgo "github.com/Suhaibinator/Next.go"
)

// PublicOrigin is illustrative public metadata, never an incoming Host value.
const PublicOrigin = "https://example.test"

func Configure(c *nextgo.Config) {
	c.PreserveLayouts = true
	c.BasePath = os.Getenv("NEXTGO_METADATA_BASE_PATH")
	c.AssetPrefix = os.Getenv("NEXTGO_METADATA_ASSET_PREFIX")
	if value := os.Getenv("NEXTGO_METADATA_TRAILING_SLASH"); value != "" {
		var err error
		c.TrailingSlash, err = strconv.ParseBool(value)
		if err != nil {
			panic("NEXTGO_METADATA_TRAILING_SLASH must be a boolean")
		}
	}
}
