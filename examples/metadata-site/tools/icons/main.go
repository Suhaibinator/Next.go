//go:build !js

// This tool draws the example's original mint N raster tiles. No external art,
// fonts or network resources are used. Run from app with go generate.
package main

import (
	"bytes"
	"encoding/binary"
	"flag"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
)

func tile(size int) []byte {
	im := image.NewNRGBA(image.Rect(0, 0, size, size))
	background := color.NRGBA{R: 18, G: 59, B: 53, A: 255}
	mint := color.NRGBA{R: 121, G: 234, B: 185, A: 255}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			u, v := x*32/size, y*32/size
			ink := u >= 7 && u <= 10 && v >= 7 && v <= 24 || u >= 21 && u <= 24 && v >= 7 && v <= 24 || u >= 9 && u <= 22 && v >= u-2 && v <= u+2
			if ink {
				im.SetNRGBA(x, y, mint)
			} else {
				im.SetNRGBA(x, y, background)
			}
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		panic(err)
	}
	return b.Bytes()
}

func main() {
	out := flag.String("out", "app", "output directory")
	flag.Parse()
	icon := tile(32)
	var ico bytes.Buffer
	for _, v := range []uint16{0, 1, 1} {
		if err := binary.Write(&ico, binary.LittleEndian, v); err != nil {
			panic(err)
		}
	}
	ico.Write([]byte{32, 32, 0, 0})
	for _, v := range []uint16{1, 32} {
		if err := binary.Write(&ico, binary.LittleEndian, v); err != nil {
			panic(err)
		}
	}
	for _, v := range []uint32{uint32(len(icon)), 22} {
		if err := binary.Write(&ico, binary.LittleEndian, v); err != nil {
			panic(err)
		}
	}
	ico.Write(icon)
	for name, data := range map[string][]byte{"icon.png": icon, "apple-icon.png": tile(180), "favicon.ico": ico.Bytes()} {
		if err := os.WriteFile(filepath.Join(*out, name), data, 0644); err != nil {
			panic(err)
		}
	}
}
