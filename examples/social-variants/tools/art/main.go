// Command art writes original procedural social-image fixtures. No third-party
// artwork, font, network service or JavaScript tooling is required.
package main

import (
	"flag"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"os"
	"path/filepath"
)

func main() {
	out := flag.String("out", ".", "fixture destination")
	flag.Parse()
	palette := color.Palette{color.NRGBA{R: 9, G: 32, B: 28, A: 255}, color.NRGBA{R: 121, G: 234, B: 185, A: 255}, color.NRGBA{R: 244, G: 250, B: 248, A: 255}}
	frame := func(invert bool) *image.Paletted {
		img := image.NewPaletted(image.Rect(0, 0, 320, 180), palette)
		for y := 0; y < 180; y++ {
			for x := 0; x < 320; x++ {
				index := uint8(0)
				if x >= 28 && x < 292 && y >= 28 && y < 152 {
					index = 1
					if ((x-28)/22+(y-28)/22)%2 == 0 {
						index = 2
					}
				}
				if invert && index > 0 {
					index = 3 - index
				}
				img.SetColorIndex(x, y, index)
			}
		}
		return img
	}
	write := func(name string, encode func(*os.File) error) {
		file, err := os.Create(filepath.Join(*out, name))
		if err != nil {
			panic(err)
		}
		if err := encode(file); err != nil {
			file.Close()
			panic(err)
		}
		if err := file.Close(); err != nil {
			panic(err)
		}
	}
	write("opengraph-image.jpg", func(f *os.File) error { return jpeg.Encode(f, frame(false), &jpeg.Options{Quality: 85}) })
	write("twitter-image.gif", func(f *os.File) error {
		return gif.EncodeAll(f, &gif.GIF{Image: []*image.Paletted{frame(false), frame(true)}, Delay: []int{25, 25}, LoopCount: 0, Config: image.Config{ColorModel: palette, Width: 320, Height: 180}})
	})
}
