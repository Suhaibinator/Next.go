//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"os"
)

func Configure(c *nextgo.Config) {
	c.BasePath = os.Getenv("NEXTGO_FORM_VALIDATION_BASE_PATH")
	c.AssetPrefix = os.Getenv("NEXTGO_FORM_VALIDATION_ASSET_PREFIX")
}
