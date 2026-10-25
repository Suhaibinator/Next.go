//go:build !js

package app

import (
	"context"

	"github.com/Suhaibinator/Next.go/metadatafiles"
)

func Manifest(ctx context.Context) (metadatafiles.Manifest, error) {
	return metadatafiles.Manifest{
		Name: "Next.go metadata field notes", ShortName: "Field notes",
		Description: "Explicit public crawler, icon and manifest resources from Go.",
		Lang:        "en", Dir: "ltr", ID: "./", StartURL: "./", Scope: "./", Display: "browser",
		ThemeColor: "#123b35", BackgroundColor: "#f4faf8",
		Icons: []metadatafiles.ManifestIcon{{Src: "icon.png", Sizes: "32x32", Type: "image/png", Purpose: "any"}},
	}, ctx.Err()
}
