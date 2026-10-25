//go:build !js

package metadatafiles

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image/png"
	"sort"
	"strconv"
	"strings"
)

func validatePNG(data []byte) (int, int, error) {
	if len(data) == 0 || len(data) > MaxImageBytes {
		return 0, 0, fmt.Errorf("PNG exceeds byte limit")
	}
	config, err := png.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return 0, 0, fmt.Errorf("invalid PNG header")
	}
	if config.Width < 1 || config.Height < 1 || config.Width > MaxImageDimension || config.Height > MaxImageDimension || int64(config.Width)*int64(config.Height) > MaxImagePixels {
		return 0, 0, fmt.Errorf("PNG exceeds dimension/pixel limits")
	}
	// Full decoding checks the bounded compressed data, chunk CRCs and actual
	// raster. A valid IHDR alone must not admit truncated/incorrect PNG data.
	if _, err := png.Decode(bytes.NewReader(data)); err != nil {
		return 0, 0, fmt.Errorf("invalid PNG raster")
	}
	return config.Width, config.Height, nil
}

// ICO is a bounded container of PNG or uncompressed 1/4/8/24/32-bit DIB images.
// Unsupported compression/header variants fail rather than being sniffed as SVG
// or sent with a mismatching content type. ICO payloads are never executed.
func validateICO(data []byte) (int, int, string, error) {
	bad := func() (int, int, string, error) { return 0, 0, "", fmt.Errorf("invalid or unsupported ICO raster") }
	if len(data) < 6 || len(data) > MaxImageBytes || binary.LittleEndian.Uint16(data) != 0 || binary.LittleEndian.Uint16(data[2:]) != 1 {
		return bad()
	}
	count := int(binary.LittleEndian.Uint16(data[4:]))
	if count < 1 || count > 32 || 6+16*count > len(data) {
		return bad()
	}
	type span struct{ start, end uint64 }
	var ranges []span
	maxW, maxH := 0, 0
	var sizes []string
	seen := map[string]bool{}
	for i := 0; i < count; i++ {
		entry := data[6+16*i : 6+16*(i+1)]
		width, height := int(entry[0]), int(entry[1])
		if width == 0 {
			width = 256
		}
		if height == 0 {
			height = 256
		}
		if entry[3] != 0 {
			return bad()
		}
		length, offset := uint64(binary.LittleEndian.Uint32(entry[8:])), uint64(binary.LittleEndian.Uint32(entry[12:]))
		if length == 0 || offset < uint64(6+16*count) || offset > uint64(len(data)) || length > uint64(len(data))-offset {
			return bad()
		}
		for _, old := range ranges {
			if offset < old.end && offset+length > old.start {
				return bad()
			}
		}
		ranges = append(ranges, span{offset, offset + length})
		payload := data[int(offset):int(offset+length)]
		if bytes.HasPrefix(payload, []byte{137, 'P', 'N', 'G', 13, 10, 26, 10}) {
			w, h, err := validatePNG(payload)
			if err != nil || w != width || h != height {
				return bad()
			}
		} else if !validDIB(payload, width, height) {
			return bad()
		}
		if width > maxW {
			maxW = width
		}
		if height > maxH {
			maxH = height
		}
		size := strconv.Itoa(width) + "x" + strconv.Itoa(height)
		if !seen[size] {
			seen[size] = true
			sizes = append(sizes, size)
		}
	}
	sort.Strings(sizes)
	return maxW, maxH, strings.Join(sizes, " "), nil
}

func validDIB(data []byte, width, height int) bool {
	if len(data) < 40 {
		return false
	}
	header := uint64(binary.LittleEndian.Uint32(data))
	if header != 40 && header != 108 && header != 124 || header > uint64(len(data)) {
		return false
	}
	w, h := int32(binary.LittleEndian.Uint32(data[4:])), int32(binary.LittleEndian.Uint32(data[8:]))
	planes, bits := binary.LittleEndian.Uint16(data[12:]), binary.LittleEndian.Uint16(data[14:])
	if w != int32(width) || h != int32(2*height) || planes != 1 || binary.LittleEndian.Uint32(data[16:]) != 0 {
		return false
	}
	if bits != 1 && bits != 4 && bits != 8 && bits != 24 && bits != 32 {
		return false
	}
	palette := uint64(binary.LittleEndian.Uint32(data[32:]))
	if bits <= 8 {
		if palette == 0 {
			palette = uint64(1) << bits
		}
		if palette > uint64(1)<<bits {
			return false
		}
	} else if palette != 0 {
		return false
	}
	xorRow := (uint64(width)*uint64(bits) + 31) / 32 * 4
	andRow := (uint64(width) + 31) / 32 * 4
	need := header + palette*4 + (xorRow+andRow)*uint64(height)
	return need <= uint64(len(data))
}
