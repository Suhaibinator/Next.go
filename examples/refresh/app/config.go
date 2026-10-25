//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"os"
	"strconv"
)

func Configure(c *nextgo.Config) {
	c.PreserveLayouts = true
	c.BasePath = os.Getenv("NEXTGO_REFRESH_BASE_PATH")
	c.AssetPrefix = os.Getenv("NEXTGO_REFRESH_ASSET_PREFIX")
	note := os.Getenv("NEXTGO_REFRESH_PUBLIC_NOTE")
	if note == "" {
		note = "Configured public development note"
	}
	c.Public = map[string]string{"refresh.note": note, "refresh.server": strconv.FormatBool(c.Phase != nextgo.PhaseStaticExport)}
}
