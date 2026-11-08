//go:build !js

package app

import (
	"context"
	"image/color"

	"github.com/Suhaibinator/Next.go/metadatafiles"
	"github.com/Suhaibinator/Next.go/socialimage"
)

// OpenGraphImage is a public startup producer. It receives no incoming request.
func OpenGraphImage(ctx context.Context) ([]byte, error) {
	return publicCard(ctx, "OPEN GRAPH / GO METADATA")
}

func publicCard(ctx context.Context, label string) ([]byte, error) {
	return socialimage.Render(ctx, socialimage.Document{Width: 1200, Height: 630,
		Background: color.NRGBA{R: 9, G: 32, B: 28, A: 255},
		Root: socialimage.Layout{Padding: 56, Gap: 32, Children: []socialimage.Item{
			{Size: 64, Node: socialimage.Text{Value: label, Scale: 5, Color: color.NRGBA{R: 121, G: 234, B: 185, A: 255}}},
			{Node: socialimage.Text{Value: "Small files. Clear ownership.", Scale: 10, Color: color.NRGBA{R: 244, G: 250, B: 248, A: 255}}},
			{Size: 64, Node: socialimage.Text{Value: "PUBLIC PIXELS / NATIVE GO / STATIC OR SERVER", Scale: 4, Color: color.NRGBA{R: 183, G: 207, B: 199, A: 255}}},
		}},
	}, socialimage.Limits{MaxOutputBytes: metadatafiles.MaxImageBytes})
}
