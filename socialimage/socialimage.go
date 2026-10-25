//go:build !js

// Package socialimage composes bounded PNG cards on the server using Go values.
// It does not interpret HTML/CSS, execute scripts, or fetch images or fonts.
package socialimage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"unicode"
	"unicode/utf8"
)

var (
	ErrInvalid = errors.New("socialimage: invalid document")
	ErrLimit   = errors.New("socialimage: resource limit exceeded")
)

// Limits apply to a whole document, including repeated image/text inputs. Zero
// fields select defaults. Limits are validated before a user Font is called.
type Limits struct {
	MaxDimension, MaxPixels, MaxNodes, MaxDepth int
	MaxTextBytes, MaxInputBytes, MaxInputPixels int
	MaxOutputBytes                              int
}

func (l Limits) normalized() (Limits, error) {
	fields := []*int{&l.MaxDimension, &l.MaxPixels, &l.MaxNodes, &l.MaxDepth, &l.MaxTextBytes, &l.MaxInputBytes, &l.MaxInputPixels, &l.MaxOutputBytes}
	defaults := []int{2048, 4_000_000, 256, 16, 4096, 8 << 20, 16_000_000, 20 << 20}
	ceilings := []int{8192, 16_000_000, 1024, 32, 32 << 10, 32 << 20, 32_000_000, 64 << 20}
	for i, p := range fields {
		if *p == 0 {
			*p = defaults[i]
		}
		if *p < 1 || *p > ceilings[i] {
			return l, fmt.Errorf("%w: invalid limits", ErrInvalid)
		}
	}
	return l, nil
}

// Document is a public image description. Do not put secrets in its text or
// pictures: the output can be copied and shared independently of the page.
// Callers must not mutate the document, its slices, or Font during rendering.
type Document struct {
	Width, Height int
	Background    color.NRGBA
	Root          Node
}

// Node is implemented only by this package's value types. Use value nodes;
// pointers and nil nodes are rejected. Layout graphs may not exceed MaxDepth.
type Node interface{ socialImageNode() }

// Panel fills its entire allocated rectangle, including alpha compositing.
type Panel struct{ Color color.NRGBA }

func (Panel) socialImageNode() {}

type Direction uint8

const (
	Column Direction = iota
	Row
)

// Item allocates Size pixels along the parent layout's direction. Zero shares
// remaining space equally with other zero-sized items. The cross axis fills.
type Item struct {
	Size int
	Node Node
}

// Layout is a padded row or column. Fixed sizes and gaps must fit; remaining
// pixels go to flexible items in declaration order. Children never overflow.
type Layout struct {
	Direction    Direction
	Padding, Gap int
	Children     []Item
}

func (Layout) socialImageNode() {}

// Overlay paints children back-to-front in the same rectangle.
type Overlay struct{ Children []Node }

func (Overlay) socialImageNode() {}

type Align uint8

const (
	Start Align = iota
	Center
	End
)

// Text uses fixed-metric bitmap glyphs, greedy word wrapping, and explicit
// newlines. Scale defaults to 4. Overflow is clipped to complete lines; MaxLines
// can further reduce them. There is no automatic font shrinking or ellipsis.
type Text struct {
	Value          string
	Color          color.NRGBA
	Scale, LineGap int
	MaxLines       int
	Align          Align
	Font           Font
}

func (Text) socialImageNode() {}

// Font supplies trusted, deterministic, fixed-metric bitmap glyphs. Nil Text.Font
// selects the original 5x7 ASCII face. It renders a-z as A-Z. Unsupported runes
// fail rendering. Font code must cooperate with request cancellation itself.
// Font methods may be called concurrently by independent renders.
type Font interface {
	Metrics() FontMetrics
	Glyph(rune) (Bitmap, bool)
}

// FontMetrics bounds each glyph. Width <=32, Height <=64, and Advance is between
// Width and 64. A bitmap has exactly Height rows; bit (Width-1-x) lights pixel x.
type FontMetrics struct{ Width, Height, Advance int }
type Bitmap struct{ Rows []uint32 }

type Fit uint8

const (
	Contain Fit = iota
	Cover
	Stretch
)

// Picture consumes encoded PNG/JPEG/GIF bytes (GIF's first frame), never URLs.
// Configured byte and pixel bounds are checked before full decoding. Scaling is
// deterministic nearest-neighbor sampling. Contain centers on transparent space.
type Picture struct {
	Data []byte
	Fit  Fit
}

func (Picture) socialImageNode() {}

type placedGlyph struct {
	X, Y int
	Rows []uint32
}

type operation struct {
	Kind    string
	Rect    image.Rectangle
	Color   color.NRGBA
	Scale   int
	Metrics FontMetrics
	Glyphs  []placedGlyph
	Data    []byte
	Fit     Fit
}

type plan struct {
	Width, Height int
	Background    color.NRGBA
	Operations    []operation
}

// Render validates, composes, and PNG-encodes a document. Returned bytes are
// owned by the caller. Errors preserve context cancellation via errors.Is.
func Render(ctx context.Context, d Document, limits Limits) ([]byte, error) {
	l, err := limits.normalized()
	if err != nil {
		return nil, err
	}
	p, err := prepare(ctx, d, l)
	if err != nil {
		return nil, err
	}
	return p.render(ctx, l)
}

// Preflight visits the entire graph before calling a font or image decoder.
func prepare(ctx context.Context, d Document, l Limits) (*plan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if d.Width < 1 || d.Height < 1 || d.Width > l.MaxDimension || d.Height > l.MaxDimension || d.Width > l.MaxPixels/d.Height {
		return nil, fmt.Errorf("%w: output dimensions", ErrLimit)
	}
	count, textBytes, imageBytes := 0, 0, 0
	var check func(Node, int) error
	check = func(n Node, depth int) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		count++
		if depth > l.MaxDepth || count > l.MaxNodes {
			return fmt.Errorf("%w: node count or depth", ErrLimit)
		}
		switch v := n.(type) {
		case Panel:
		case Layout:
			if v.Direction > Row || v.Padding < 0 || v.Padding > l.MaxDimension || v.Gap < 0 || v.Gap > l.MaxDimension {
				return fmt.Errorf("%w: layout", ErrInvalid)
			}
			if len(v.Children) > l.MaxNodes {
				return fmt.Errorf("%w: children", ErrLimit)
			}
			for _, child := range v.Children {
				if child.Size < 0 || child.Size > l.MaxDimension {
					return fmt.Errorf("%w: item size", ErrInvalid)
				}
				if err := check(child.Node, depth+1); err != nil {
					return err
				}
			}
		case Overlay:
			if len(v.Children) > l.MaxNodes {
				return fmt.Errorf("%w: children", ErrLimit)
			}
			for _, child := range v.Children {
				if err := check(child, depth+1); err != nil {
					return err
				}
			}
		case Text:
			if len(v.Value) > l.MaxTextBytes-textBytes {
				return fmt.Errorf("%w: text bytes", ErrLimit)
			}
			textBytes += len(v.Value)
			if !utf8.ValidString(v.Value) || v.Scale < 0 || v.Scale > 32 || v.LineGap < 0 || v.LineGap > l.MaxDimension || v.MaxLines < 0 || v.MaxLines > l.MaxDimension || v.Align > End {
				return fmt.Errorf("%w: text style or UTF-8", ErrInvalid)
			}
			for _, r := range v.Value {
				if unicode.IsControl(r) && r != '\n' {
					return fmt.Errorf("%w: text control character", ErrInvalid)
				}
			}
		case Picture:
			if len(v.Data) < 1 || v.Fit > Stretch {
				return fmt.Errorf("%w: picture", ErrInvalid)
			}
			if len(v.Data) > l.MaxInputBytes-imageBytes {
				return fmt.Errorf("%w: input bytes", ErrLimit)
			}
			imageBytes += len(v.Data)
		default:
			return fmt.Errorf("%w: nil, pointer or unknown node", ErrInvalid)
		}
		return nil
	}
	if err := check(d.Root, 1); err != nil {
		return nil, err
	}
	p := &plan{Width: d.Width, Height: d.Height, Background: d.Background}
	var compile func(Node, image.Rectangle) error
	compile = func(n Node, rect image.Rectangle) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		switch v := n.(type) {
		case Panel:
			p.Operations = append(p.Operations, operation{Kind: "panel", Rect: rect, Color: v.Color})
		case Text:
			op, err := compileText(ctx, v, rect)
			if err != nil {
				return err
			}
			p.Operations = append(p.Operations, op)
		case Picture:
			data := bytes.Clone(v.Data)
			cfg, format, err := image.DecodeConfig(contextReader{ctx, bytes.NewReader(data)})
			if err != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				return fmt.Errorf("%w: image encoding", ErrInvalid)
			}
			if format != "png" && format != "jpeg" && format != "gif" {
				return fmt.Errorf("%w: image format", ErrInvalid)
			}
			if cfg.Width < 1 || cfg.Height < 1 || cfg.Width > l.MaxInputPixels/cfg.Height {
				return fmt.Errorf("%w: input pixels", ErrLimit)
			}
			p.Operations = append(p.Operations, operation{Kind: "picture", Rect: rect, Data: data, Fit: v.Fit})
		case Overlay:
			for _, child := range v.Children {
				if err := compile(child, rect); err != nil {
					return err
				}
			}
		case Layout:
			if v.Padding*2 > rect.Dx() || v.Padding*2 > rect.Dy() {
				return fmt.Errorf("%w: padding exceeds box", ErrInvalid)
			}
			inner := rect.Inset(v.Padding)
			available := inner.Dy()
			if v.Direction == Row {
				available = inner.Dx()
			}
			available -= max(0, len(v.Children)-1) * v.Gap
			flex := 0
			for _, child := range v.Children {
				available -= child.Size
				if child.Size == 0 {
					flex++
				}
			}
			if available < 0 || (flex > 0 && available < flex) {
				return fmt.Errorf("%w: children exceed box", ErrInvalid)
			}
			offset := 0
			for _, child := range v.Children {
				size := child.Size
				if size == 0 {
					size = available / flex
					if available%flex != 0 {
						size++
					}
					available -= size
					flex--
				}
				box := inner
				if v.Direction == Row {
					box.Min.X += offset
					box.Max.X = box.Min.X + size
				} else {
					box.Min.Y += offset
					box.Max.Y = box.Min.Y + size
				}
				if err := compile(child.Node, box); err != nil {
					return err
				}
				offset += size + v.Gap
			}
		}
		return nil
	}
	if err := compile(d.Root, image.Rect(0, 0, d.Width, d.Height)); err != nil {
		return nil, err
	}
	return p, ctx.Err()
}

func (p *plan) key(ctx context.Context) (string, error) {
	h := sha256.New()
	_, _ = h.Write([]byte("Next.go socialimage plan v1\x00"))
	var buf [8]byte
	integer := func(n int) {
		binary.LittleEndian.PutUint64(buf[:], uint64(n))
		_, _ = h.Write(buf[:])
	}
	ink := func(c color.NRGBA) { _, _ = h.Write([]byte{c.R, c.G, c.B, c.A}) }
	integer(p.Width)
	integer(p.Height)
	ink(p.Background)
	integer(len(p.Operations))
	for _, op := range p.Operations {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		integer(len(op.Kind))
		_, _ = h.Write([]byte(op.Kind))
		integer(op.Rect.Min.X)
		integer(op.Rect.Min.Y)
		integer(op.Rect.Max.X)
		integer(op.Rect.Max.Y)
		ink(op.Color)
		integer(op.Scale)
		integer(op.Metrics.Width)
		integer(op.Metrics.Height)
		integer(op.Metrics.Advance)
		integer(int(op.Fit))
		integer(len(op.Glyphs))
		for _, glyph := range op.Glyphs {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			integer(glyph.X)
			integer(glyph.Y)
			integer(len(glyph.Rows))
			for _, row := range glyph.Rows {
				binary.LittleEndian.PutUint32(buf[:4], row)
				_, _ = h.Write(buf[:4])
			}
		}
		integer(len(op.Data))
		_, _ = h.Write(op.Data)
	}
	return hex.EncodeToString(h.Sum(nil)), ctx.Err()
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

type outputWriter struct {
	ctx context.Context
	bytes.Buffer
	limit int
}

func (w *outputWriter) Write(p []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) > w.limit-w.Len() {
		return 0, fmt.Errorf("%w: encoded bytes", ErrLimit)
	}
	return w.Buffer.Write(p)
}

func (p *plan) render(ctx context.Context, l Limits) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dst := image.NewNRGBA(image.Rect(0, 0, p.Width, p.Height))
	draw.Draw(dst, dst.Bounds(), image.NewUniform(p.Background), image.Point{}, draw.Src)
	for _, op := range p.Operations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch op.Kind {
		case "panel":
			draw.Draw(dst, op.Rect, image.NewUniform(op.Color), image.Point{}, draw.Over)
		case "text":
			ink := image.NewUniform(op.Color)
			for _, g := range op.Glyphs {
				if err := ctx.Err(); err != nil {
					return nil, err
				}
				for y, bits := range g.Rows {
					for x := 0; x < op.Metrics.Width; x++ {
						if bits&(uint32(1)<<uint(op.Metrics.Width-1-x)) != 0 {
							r := image.Rect(g.X+x*op.Scale, g.Y+y*op.Scale, g.X+(x+1)*op.Scale, g.Y+(y+1)*op.Scale).Intersect(op.Rect)
							draw.Draw(dst, r, ink, image.Point{}, draw.Over)
						}
					}
				}
			}
		case "picture":
			src, _, err := image.Decode(contextReader{ctx, bytes.NewReader(op.Data)})
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				return nil, fmt.Errorf("%w: corrupt image", ErrInvalid)
			}
			if err := paintPicture(ctx, dst, src, op.Rect, op.Fit); err != nil {
				return nil, err
			}
		}
	}
	w := &outputWriter{ctx: ctx, limit: l.MaxOutputBytes}
	if err := png.Encode(w, dst); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return w.Bytes(), nil
}

func paintPicture(ctx context.Context, dst *image.NRGBA, src image.Image, box image.Rectangle, fit Fit) error {
	if box.Empty() {
		return nil
	}
	s := src.Bounds()
	target := box
	if fit == Contain {
		w, h := box.Dx(), box.Dy()
		if int64(w)*int64(s.Dy()) > int64(h)*int64(s.Dx()) {
			w = max(1, int(int64(h)*int64(s.Dx())/int64(s.Dy())))
		} else {
			h = max(1, int(int64(w)*int64(s.Dy())/int64(s.Dx())))
		}
		target = image.Rect(box.Min.X+(box.Dx()-w)/2, box.Min.Y+(box.Dy()-h)/2, box.Min.X+(box.Dx()-w)/2+w, box.Min.Y+(box.Dy()-h)/2+h)
	}
	// Cover samples a centered source crop with the destination aspect ratio.
	if fit == Cover {
		w, h := s.Dx(), s.Dy()
		if int64(w)*int64(box.Dy()) > int64(h)*int64(box.Dx()) {
			w = max(1, int(int64(h)*int64(box.Dx())/int64(box.Dy())))
		} else {
			h = max(1, int(int64(w)*int64(box.Dy())/int64(box.Dx())))
		}
		s = image.Rect(s.Min.X+(s.Dx()-w)/2, s.Min.Y+(s.Dy()-h)/2, s.Min.X+(s.Dx()-w)/2+w, s.Min.Y+(s.Dy()-h)/2+h)
	}
	// One reusable scanline keeps temporary memory proportional to output width.
	line := image.NewNRGBA(image.Rect(0, 0, target.Dx(), 1))
	for y := target.Min.Y; y < target.Max.Y; y++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		sy := s.Min.Y + int(int64(y-target.Min.Y)*int64(s.Dy())/int64(target.Dy()))
		for x := 0; x < target.Dx(); x++ {
			line.Set(x, 0, src.At(s.Min.X+int(int64(x)*int64(s.Dx())/int64(target.Dx())), sy))
		}
		draw.Draw(dst, image.Rect(target.Min.X, y, target.Max.X, y+1), line, image.Point{}, draw.Over)
	}
	return nil
}
