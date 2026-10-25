//go:build !js

package socialimage

import (
	"context"
	"fmt"
	"image"
)

// ASCIIFont is an original, deliberately small 5x7 bitmap face, distributed
// under this repository's MIT license. It supports printable ASCII and maps
// lowercase letters to uppercase. It is not a general typography engine.
type ASCIIFont struct{}

func (ASCIIFont) Metrics() FontMetrics { return FontMetrics{Width: 5, Height: 7, Advance: 6} }
func (ASCIIFont) Glyph(r rune) (Bitmap, bool) {
	if r >= 'a' && r <= 'z' {
		r -= 'a' - 'A'
	}
	g, ok := asciiGlyphs[r]
	if !ok {
		return Bitmap{}, false
	}
	rows := make([]uint32, len(g))
	for i, bits := range g {
		rows[i] = uint32(bits)
	}
	return Bitmap{Rows: rows}, true
}

// Rows are handwritten five-bit masks, top to bottom. No external font data.
var asciiGlyphs = map[rune][7]uint8{
	' ': {0, 0, 0, 0, 0, 0, 0},
	'A': {14, 17, 17, 31, 17, 17, 17}, 'B': {30, 17, 17, 30, 17, 17, 30},
	'C': {14, 17, 16, 16, 16, 17, 14}, 'D': {30, 17, 17, 17, 17, 17, 30},
	'E': {31, 16, 16, 30, 16, 16, 31}, 'F': {31, 16, 16, 30, 16, 16, 16},
	'G': {14, 17, 16, 23, 17, 17, 15}, 'H': {17, 17, 17, 31, 17, 17, 17},
	'I': {14, 4, 4, 4, 4, 4, 14}, 'J': {7, 2, 2, 2, 18, 18, 12},
	'K': {17, 18, 20, 24, 20, 18, 17}, 'L': {16, 16, 16, 16, 16, 16, 31},
	'M': {17, 27, 21, 21, 17, 17, 17}, 'N': {17, 25, 25, 21, 19, 19, 17},
	'O': {14, 17, 17, 17, 17, 17, 14}, 'P': {30, 17, 17, 30, 16, 16, 16},
	'Q': {14, 17, 17, 17, 21, 18, 13}, 'R': {30, 17, 17, 30, 20, 18, 17},
	'S': {15, 16, 16, 14, 1, 1, 30}, 'T': {31, 4, 4, 4, 4, 4, 4},
	'U': {17, 17, 17, 17, 17, 17, 14}, 'V': {17, 17, 17, 17, 17, 10, 4},
	'W': {17, 17, 17, 21, 21, 27, 17}, 'X': {17, 17, 10, 4, 10, 17, 17},
	'Y': {17, 17, 10, 4, 4, 4, 4}, 'Z': {31, 1, 2, 4, 8, 16, 31},
	'0': {14, 17, 19, 21, 25, 17, 14}, '1': {4, 12, 4, 4, 4, 4, 14},
	'2': {14, 17, 1, 2, 4, 8, 31}, '3': {30, 1, 1, 14, 1, 1, 30},
	'4': {2, 6, 10, 18, 31, 2, 2}, '5': {31, 16, 16, 30, 1, 1, 30},
	'6': {14, 16, 16, 30, 17, 17, 14}, '7': {31, 1, 2, 4, 8, 8, 8},
	'8': {14, 17, 17, 14, 17, 17, 14}, '9': {14, 17, 17, 15, 1, 1, 14},
	'!': {4, 4, 4, 4, 4, 0, 4}, '"': {10, 10, 10, 0, 0, 0, 0},
	'#': {10, 31, 10, 10, 31, 10, 0}, '$': {4, 15, 20, 14, 5, 30, 4},
	'%': {17, 2, 4, 8, 16, 17, 0}, '&': {12, 18, 20, 8, 21, 18, 13},
	'\'': {4, 4, 8, 0, 0, 0, 0}, '(': {2, 4, 8, 8, 8, 4, 2},
	')': {8, 4, 2, 2, 2, 4, 8}, '*': {0, 21, 14, 31, 14, 21, 0},
	'+': {0, 4, 4, 31, 4, 4, 0}, ',': {0, 0, 0, 0, 4, 4, 8},
	'-': {0, 0, 0, 31, 0, 0, 0}, '.': {0, 0, 0, 0, 0, 12, 12},
	'/': {1, 2, 2, 4, 8, 8, 16}, ':': {0, 12, 12, 0, 12, 12, 0},
	';': {0, 12, 12, 0, 4, 4, 8}, '<': {2, 4, 8, 16, 8, 4, 2},
	'=': {0, 0, 31, 0, 31, 0, 0}, '>': {8, 4, 2, 1, 2, 4, 8},
	'?': {14, 17, 1, 2, 4, 0, 4}, '@': {14, 17, 23, 21, 23, 16, 14},
	'[': {14, 8, 8, 8, 8, 8, 14}, '\\': {16, 8, 8, 4, 2, 2, 1},
	']': {14, 2, 2, 2, 2, 2, 14}, '^': {4, 10, 17, 0, 0, 0, 0},
	'_': {0, 0, 0, 0, 0, 0, 31}, '`': {8, 4, 2, 0, 0, 0, 0},
	'{': {3, 4, 4, 8, 4, 4, 3}, '|': {4, 4, 4, 4, 4, 4, 4},
	'}': {24, 4, 4, 2, 4, 4, 24}, '~': {0, 0, 9, 22, 0, 0, 0},
}

func compileText(ctx context.Context, t Text, rect image.Rectangle) (operation, error) {
	f := t.Font
	if f == nil {
		f = ASCIIFont{}
	}
	m := f.Metrics()
	if err := ctx.Err(); err != nil {
		return operation{}, err
	}
	if m.Width < 1 || m.Width > 32 || m.Height < 1 || m.Height > 64 || m.Advance < m.Width || m.Advance > 64 {
		return operation{}, fmt.Errorf("%w: font metrics", ErrInvalid)
	}
	scale := t.Scale
	if scale == 0 {
		scale = 4
	}
	op := operation{Kind: "text", Rect: rect, Color: t.Color, Scale: scale, Metrics: m}
	runes := []rune(t.Value)
	bitmaps := make([][]uint32, len(runes))
	for i, r := range runes {
		if err := ctx.Err(); err != nil {
			return operation{}, err
		}
		if r == '\n' {
			continue
		}
		bitmap, ok := f.Glyph(r)
		if err := ctx.Err(); err != nil {
			return operation{}, err
		}
		if !ok || len(bitmap.Rows) != m.Height {
			return operation{}, fmt.Errorf("%w: unsupported rune or glyph rows", ErrInvalid)
		}
		for _, row := range bitmap.Rows {
			if row>>uint(m.Width) != 0 {
				return operation{}, fmt.Errorf("%w: glyph bits", ErrInvalid)
			}
		}
		bitmaps[i] = append([]uint32(nil), bitmap.Rows...)
	}
	columns := 0
	if rect.Dx() >= m.Width*scale {
		columns = (rect.Dx()-m.Width*scale)/(m.Advance*scale) + 1
	}
	lineHeight := m.Height*scale + t.LineGap
	lines := (rect.Dy() + t.LineGap) / lineHeight
	if t.MaxLines > 0 {
		lines = min(lines, t.MaxLines)
	}
	if columns < 1 || lines < 1 {
		return op, ctx.Err()
	}
	start := 0
	for line := 0; line < lines && start < len(runes); line++ {
		end := start
		for end < len(runes) && end-start < columns && runes[end] != '\n' {
			end++
		}
		next := end
		if end < len(runes) && runes[end] == '\n' {
			next++
		} else if end < len(runes) && runes[end] != ' ' {
			// Prefer a word boundary, but split a word longer than a line.
			for i := end - 1; i > start; i-- {
				if runes[i] == ' ' {
					end = i
					next = i + 1
					break
				}
			}
		}
		for end > start && runes[end-1] == ' ' {
			end--
		}
		width := 0
		if end > start {
			width = ((end-start-1)*m.Advance + m.Width) * scale
		}
		x := rect.Min.X
		if t.Align == Center {
			x += (rect.Dx() - width) / 2
		}
		if t.Align == End {
			x += rect.Dx() - width
		}
		for i := start; i < end; i++ {
			op.Glyphs = append(op.Glyphs, placedGlyph{X: x + (i-start)*m.Advance*scale, Y: rect.Min.Y + line*lineHeight, Rows: bitmaps[i]})
		}
		for next < len(runes) && runes[next] == ' ' {
			next++
		}
		start = next
	}
	return op, ctx.Err()
}
