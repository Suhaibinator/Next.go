//go:build !js

package metadatafiles

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image/gif"
	"image/jpeg"
)

const MaxGIFFrames = 32
const MaxGIFDecodedPixels = 8 << 20
const MaxGIFBlocks = 16384
const MaxJPEGScans = 128
const MaxJPEGMarkers = 4096

func socialRasterBounds(width, height int) bool {
	return width > 0 && height > 0 && width <= MaxImageDimension && height <= MaxImageDimension && int64(width)*int64(height) <= MaxImagePixels
}

func validateJPEG(data []byte) (int, int, error) {
	bad := func() (int, int, error) { return 0, 0, fmt.Errorf("invalid JPEG or raster/work limits exceeded") }
	if len(data) == 0 || len(data) > MaxImageBytes {
		return bad()
	}
	config, err := jpeg.DecodeConfig(bytes.NewReader(data))
	if err != nil || !socialRasterBounds(config.Width, config.Height) || !boundedJPEG(data) {
		return bad()
	}
	if _, err := jpeg.Decode(bytes.NewReader(data)); err != nil {
		return bad()
	}
	return config.Width, config.Height, nil
}

// Count marker/scan work before full coefficient/raster allocation. Segment
// payloads are skipped by their declared lengths; entropy bytes honor stuffing
// and restart markers. The standard decoder validates actual JPEG semantics.
func boundedJPEG(data []byte) bool {
	if len(data) < 4 || data[0] != 255 || data[1] != 0xd8 {
		return false
	}
	position, scans, markers := 2, 0, 0
	for position < len(data) {
		if data[position] != 255 {
			return false
		}
		for position < len(data) && data[position] == 255 {
			position++
		}
		if position == len(data) {
			return false
		}
		marker := data[position]
		position++
		markers++
		if markers > MaxJPEGMarkers {
			return false
		}
		if marker == 0xd9 {
			return scans > 0
		}
		if marker == 0 || marker == 0xd8 || marker >= 0xd0 && marker <= 0xd7 || marker == 1 {
			return false
		}
		if len(data)-position < 2 {
			return false
		}
		length := int(binary.BigEndian.Uint16(data[position:]))
		if length < 2 || length > len(data)-position {
			return false
		}
		position += length
		if marker != 0xda {
			continue
		}
		scans++
		if scans > MaxJPEGScans {
			return false
		}
		for position < len(data) {
			if data[position] != 255 {
				position++
				continue
			}
			start := position
			for position < len(data) && data[position] == 255 {
				position++
			}
			if position == len(data) {
				return false
			}
			code := data[position]
			if code == 0 || code >= 0xd0 && code <= 0xd7 {
				position++
				continue
			}
			position = start
			break
		}
	}
	return false
}

func validateGIF(data []byte) (int, int, error) {
	bad := func() (int, int, error) { return 0, 0, fmt.Errorf("invalid GIF or raster/frame/work limits exceeded") }
	if len(data) < 13 || len(data) > MaxImageBytes || string(data[:6]) != "GIF87a" && string(data[:6]) != "GIF89a" {
		return bad()
	}
	width, height := int(binary.LittleEndian.Uint16(data[6:])), int(binary.LittleEndian.Uint16(data[8:]))
	if !socialRasterBounds(width, height) || !boundedGIF(data, width, height) {
		return bad()
	}
	decoded, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil || len(decoded.Image) == 0 || len(decoded.Image) > MaxGIFFrames {
		return bad()
	}
	return width, height, nil
}

// Walk the complete bounded block envelope before DecodeAll can allocate any
// animation frames. Sum frame rectangles, not just the logical canvas. Full
// raster/LZW/extension semantics remain the standard decoder's responsibility.
func boundedGIF(data []byte, width, height int) bool {
	position, frames, pixels, blocks := 13, 0, int64(0), 0
	palette := func(packed byte) bool {
		if packed&0x80 == 0 {
			return true
		}
		size := 3 * (2 << (packed & 7))
		if size > len(data)-position {
			return false
		}
		position += size
		return true
	}
	if !palette(data[10]) {
		return false
	}
	subblocks := func() bool {
		for position < len(data) {
			blocks++
			if blocks > MaxGIFBlocks {
				return false
			}
			size := int(data[position])
			position++
			if size == 0 {
				return true
			}
			if size > len(data)-position {
				return false
			}
			position += size
		}
		return false
	}
	for position < len(data) {
		blocks++
		if blocks > MaxGIFBlocks {
			return false
		}
		kind := data[position]
		position++
		switch kind {
		case 0x3b:
			return frames > 0
		case 0x21:
			if position == len(data) {
				return false
			}
			position++ // Extension label.
			if !subblocks() {
				return false
			}
		case 0x2c:
			if len(data)-position < 9 {
				return false
			}
			x, y := int(binary.LittleEndian.Uint16(data[position:])), int(binary.LittleEndian.Uint16(data[position+2:]))
			w, h := int(binary.LittleEndian.Uint16(data[position+4:])), int(binary.LittleEndian.Uint16(data[position+6:]))
			packed := data[position+8]
			position += 9
			frames++
			if frames > MaxGIFFrames || w < 1 || h < 1 || x > width-w || y > height-h {
				return false
			}
			pixels += int64(w) * int64(h)
			if pixels > MaxGIFDecodedPixels {
				return false
			}
			if !palette(packed) || position == len(data) {
				return false
			}
			position++ // Minimum LZW code size is validated during full decoding.
			if !subblocks() {
				return false
			}
		default:
			return false
		}
	}
	return false
}
