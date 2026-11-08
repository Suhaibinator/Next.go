//go:build !js

package app

//go:generate go run ../tools/art -out .

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"os"
	"strconv"
)

func Configure(c *nextgo.Config) {
	c.PreserveLayouts = true
	c.BasePath = os.Getenv("NEXTGO_METADATA_BASE_PATH")
	c.AssetPrefix = os.Getenv("NEXTGO_METADATA_ASSET_PREFIX")
	if raw := os.Getenv("NEXTGO_METADATA_TRAILING_SLASH"); raw != "" {
		v, err := strconv.ParseBool(raw)
		if err != nil {
			panic("NEXTGO_METADATA_TRAILING_SLASH must be a boolean")
		}
		c.TrailingSlash = v
	}
}
