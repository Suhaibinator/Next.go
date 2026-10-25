//go:build !js

// Package socialcard provides public demonstration content, never private props.
package socialcard

import (
	"fmt"
	"image/color"
	"net/url"

	"github.com/Suhaibinator/Next.go/socialimage"
)

const (
	Title       = "Go, all the way through."
	Description = "Real HTML. Go on the server. WebAssembly in your browser."
)

// Query deliberately limits this public example to printable ASCII supported
// by the built-in bitmap font. The package also supports app-owned bitmap fonts.
func Query(q url.Values) (title, description string, err error) {
	title, description = Title, Description
	for key, values := range q {
		if (key != "title" && key != "description") || len(values) != 1 {
			return "", "", fmt.Errorf("%w: unknown or repeated field", socialimage.ErrBadRequest)
		}
		limit := 180
		if key == "title" {
			limit = 96
		}
		value := values[0]
		if len(value) == 0 || len(value) > limit {
			return "", "", fmt.Errorf("%w: field length", socialimage.ErrBadRequest)
		}
		for _, r := range value {
			if r < ' ' || r > '~' {
				return "", "", fmt.Errorf("%w: printable ASCII required", socialimage.ErrBadRequest)
			}
		}
		if key == "title" {
			title = value
		} else {
			description = value
		}
	}
	return title, description, nil
}

func Document(title, description string) socialimage.Document {
	white := color.NRGBA{R: 244, G: 250, B: 248, A: 255}
	mint := color.NRGBA{R: 121, G: 234, B: 185, A: 255}
	muted := color.NRGBA{R: 183, G: 207, B: 199, A: 255}
	return socialimage.Document{
		Width: 1200, Height: 630,
		Background: color.NRGBA{R: 9, G: 32, B: 28, A: 255},
		Root: socialimage.Layout{Direction: socialimage.Row, Children: []socialimage.Item{
			{Size: 18, Node: socialimage.Panel{Color: mint}},
			{Node: socialimage.Layout{Padding: 52, Gap: 22, Children: []socialimage.Item{
				{Size: 42, Node: socialimage.Text{Value: "NEXT.GO / PUBLIC FIELD NOTES", Color: mint, Scale: 4}},
				{Size: 220, Node: socialimage.Text{Value: title, Color: white, Scale: 9, LineGap: 16, MaxLines: 3}},
				{Node: socialimage.Text{Value: description, Color: muted, Scale: 4, LineGap: 12, MaxLines: 3}},
				{Size: 36, Node: socialimage.Text{Value: "ONE LANGUAGE. FROM REQUEST TO NEXT CLICK.", Color: mint, Scale: 3}},
			}}},
		}},
	}
}
