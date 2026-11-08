//go:build !js

// Package localfonts embeds original, minimal GO-only demonstration fonts.
// Generate their outlines with tests/fonts/generate.py; no third-party font
// data is included and runtime builds need neither Python nor a font encoder.
package localfonts

import (
	"context"
	"embed"

	"github.com/Suhaibinator/Next.go/assets"
)

//go:embed fixture.woff fixture.woff2
var files embed.FS

var set = func() *assets.LocalFonts {
	value, err := assets.NewLocalFonts(context.Background(), assets.LocalFontConfig{
		FS: files,
		Fonts: []assets.LocalFont{
			{File: "fixture.woff2", Face: assets.Font{Family: "Nextgo Fixture", Display: "swap"}},
			{File: "fixture.woff", Face: assets.Font{Family: "Nextgo Fixture WOFF", Display: "swap"}},
		},
	})
	if err != nil {
		panic(err) // Literal declarations and embedded public fixture bytes.
	}
	return value
}()

func Set() *assets.LocalFonts { return set }
