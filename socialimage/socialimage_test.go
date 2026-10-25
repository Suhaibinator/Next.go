//go:build !js

package socialimage

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

var red = color.NRGBA{R: 255, A: 255}
var blue = color.NRGBA{B: 255, A: 255}

func renderImage(t *testing.T, d Document) (image.Image, []byte) {
	t.Helper()
	b, err := Render(context.Background(), d, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	im, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	return im, b
}

func TestLayoutDeterminismAndAllocation(t *testing.T) {
	d := Document{Width: 12, Height: 10, Root: Layout{Direction: Row, Padding: 1, Gap: 1, Children: []Item{
		{Size: 3, Node: Panel{Color: red}},
		{Node: Panel{Color: blue}},
		{Node: Panel{Color: red}},
	}}}
	im, first := renderImage(t, d)
	_, second := renderImage(t, d)
	if !bytes.Equal(first, second) {
		t.Fatal("PNG encoding is nondeterministic")
	}
	for _, point := range []struct {
		x, y int
		want color.NRGBA
	}{{0, 0, color.NRGBA{}}, {1, 1, red}, {3, 8, red}, {4, 1, color.NRGBA{}}, {5, 1, blue}, {7, 8, blue}, {8, 1, color.NRGBA{}}, {9, 1, red}, {10, 8, red}, {11, 9, color.NRGBA{}}} {
		if got := color.NRGBAModel.Convert(im.At(point.x, point.y)); got != point.want {
			t.Fatalf("pixel %d,%d = %v, want %v", point.x, point.y, got, point.want)
		}
	}
	for _, root := range []Node{Layout{Padding: 6}, Layout{Direction: Row, Children: []Item{{Size: 13, Node: Panel{}}}}, Layout{Direction: Column, Gap: 10, Children: []Item{{Node: Panel{}}, {Node: Panel{}}}}, nil, &Panel{}} {
		d.Root = root
		if _, err := Render(context.Background(), d, Limits{}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid layout %T: %v", root, err)
		}
	}
}

func TestBitmapTextWrapAlignAndInjection(t *testing.T) {
	d := Document{Width: 5, Height: 7, Root: Text{Value: "A", Scale: 1, Color: red}}
	im, upper := renderImage(t, d)
	if im.At(0, 0) == im.At(1, 0) {
		t.Fatal("expected visible glyph mask")
	}
	d.Root = Text{Value: "a", Scale: 1, Color: red}
	_, lower := renderImage(t, d)
	if !bytes.Equal(upper, lower) {
		t.Fatal("default lowercase normalization")
	}
	d.Width, d.Height = 17, 16
	d.Root = Text{Value: "AA A", Scale: 1, LineGap: 2, Color: red, Align: End}
	im, _ = renderImage(t, d)
	if color.NRGBAModel.Convert(im.At(13, 9)) != red {
		t.Fatal("wrapped second line was not right-aligned")
	}
	for r := rune(32); r <= 126; r++ {
		if _, ok := (ASCIIFont{}).Glyph(r); !ok {
			t.Fatalf("missing printable ASCII %q", r)
		}
	}
	d.Width, d.Height = 300, 100
	d.Root = Text{Value: `<script>alert("hello")</script>`, Color: red}
	_, data := renderImage(t, d)
	if !bytes.HasPrefix(data, []byte("\x89PNG\r\n\x1a\n")) || bytes.Contains(data, []byte("<script>")) {
		t.Fatal("text was not rasterized as PNG")
	}
	for _, value := range []string{"مرحبا", "é", "bad\x00text", "bad\rtext", string([]byte{0xff})} {
		d.Root = Text{Value: value}
		if _, err := Render(context.Background(), d, Limits{}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("unsupported/control %q: %v", value, err)
		}
	}
}

type testFont struct {
	metrics FontMetrics
	rows    []uint32
	calls   int
	cancel  context.CancelFunc
}

func (f *testFont) Metrics() FontMetrics { f.calls++; return f.metrics }
func (f *testFont) Glyph(rune) (Bitmap, bool) {
	f.calls++
	if f.cancel != nil {
		f.cancel()
	}
	return Bitmap{Rows: f.rows}, true
}

func TestLimitsBeforeFontAndOwnedGlyphs(t *testing.T) {
	f := &testFont{metrics: FontMetrics{Width: 1, Height: 1, Advance: 1}, rows: []uint32{1}}
	d := Document{Width: 10, Height: 10, Root: Overlay{Children: []Node{Text{Value: "A", Font: f}, Text{Value: strings.Repeat("x", 100)}}}}
	if _, err := Render(context.Background(), d, Limits{MaxTextBytes: 10}); !errors.Is(err, ErrLimit) {
		t.Fatal(err)
	}
	if f.calls != 0 {
		t.Fatal("font called before entire input was bounded")
	}
	for _, limits := range []Limits{{MaxDimension: -1}, {MaxPixels: 17_000_000}, {MaxNodes: 1025}, {MaxDepth: 33}, {MaxTextBytes: 33 << 10}, {MaxInputBytes: 33 << 20}, {MaxInputPixels: 33_000_000}, {MaxOutputBytes: 65 << 20}} {
		if _, err := Render(context.Background(), d, limits); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid limits accepted", limits, err)
		}
	}
	d.Root = Text{Value: "A", Font: f, Color: red, Scale: 1}
	l, _ := (Limits{}).normalized()
	p, err := prepare(context.Background(), d, l)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := p.key(context.Background())
	f.rows[0] = 0
	keyAgain, _ := p.key(context.Background())
	if key != keyAgain {
		t.Fatal("plan retained mutable font rows")
	}
	data, err := p.render(context.Background(), l)
	if err != nil {
		t.Fatal(err)
	}
	im, _ := png.Decode(bytes.NewReader(data))
	if color.NRGBAModel.Convert(im.At(0, 0)) != red {
		t.Fatal("font rows were not owned")
	}
	p2, err := prepare(context.Background(), d, l)
	if err != nil {
		t.Fatal(err)
	}
	key2, _ := p2.key(context.Background())
	if key == key2 {
		t.Fatal("different bitmap font output collided")
	}
	f.rows[0] = 2
	if _, err := Render(context.Background(), d, Limits{}); !errors.Is(err, ErrInvalid) {
		t.Fatal("out of range font bit accepted", err)
	}
	f.metrics.Width = 33
	if _, err := Render(context.Background(), d, Limits{}); !errors.Is(err, ErrInvalid) {
		t.Fatal("font metrics accepted", err)
	}
	// A cyclic slice-backed layout is rejected by depth rather than recursing forever.
	items := make([]Item, 1)
	cycle := Layout{Children: items}
	items[0].Node = cycle
	d.Root = cycle
	if _, err := Render(context.Background(), d, Limits{}); !errors.Is(err, ErrLimit) {
		t.Fatal("cyclic node graph", err)
	}
	d.Root = Panel{}
	if _, err := Render(context.Background(), d, Limits{MaxOutputBytes: 8}); !errors.Is(err, ErrLimit) {
		t.Fatal("encoded output bound", err)
	}
	d.Width = 2049
	if _, err := Render(context.Background(), d, Limits{}); !errors.Is(err, ErrLimit) {
		t.Fatal("dimension bound", err)
	}
}

func pictureBytes(t *testing.T) []byte {
	t.Helper()
	im := image.NewNRGBA(image.Rect(0, 0, 4, 2))
	for y := 0; y < 2; y++ {
		for x := 0; x < 4; x++ {
			if x < 2 {
				im.SetNRGBA(x, y, red)
			} else {
				im.SetNRGBA(x, y, blue)
			}
		}
	}
	var b bytes.Buffer
	if err := png.Encode(&b, im); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestPictureBoundsFitsAndOwnership(t *testing.T) {
	data := pictureBytes(t)
	d := Document{Width: 4, Height: 4, Root: Picture{Data: data, Fit: Contain}}
	im, _ := renderImage(t, d)
	if color.NRGBAModel.Convert(im.At(0, 0)) != (color.NRGBA{}) || color.NRGBAModel.Convert(im.At(0, 1)) != red {
		t.Fatal("contain")
	}
	d.Root = Picture{Data: data, Fit: Cover}
	im, _ = renderImage(t, d)
	if color.NRGBAModel.Convert(im.At(0, 0)) != red || color.NRGBAModel.Convert(im.At(3, 3)) != blue {
		t.Fatal("cover")
	}
	d.Root = Picture{Data: data, Fit: Stretch}
	if _, err := Render(context.Background(), d, Limits{MaxInputBytes: 1}); !errors.Is(err, ErrLimit) {
		t.Fatal("input bytes", err)
	}
	if _, err := Render(context.Background(), d, Limits{MaxInputPixels: 7}); !errors.Is(err, ErrLimit) {
		t.Fatal("input pixels", err)
	}
	bomb := bytes.Clone(data)
	binary.BigEndian.PutUint32(bomb[16:20], 50000)
	binary.BigEndian.PutUint32(bomb[20:24], 50000)
	binary.BigEndian.PutUint32(bomb[29:33], crc32.ChecksumIEEE(bomb[12:29]))
	d.Root = Picture{Data: bomb}
	if _, err := Render(context.Background(), d, Limits{}); !errors.Is(err, ErrLimit) {
		t.Fatal("PNG pixel bomb not rejected before decoding", err)
	}
	for _, bad := range [][]byte{[]byte(`<svg onload="bad()"/>`), []byte("https://example.test/private"), data[:33]} {
		d.Root = Picture{Data: bad}
		if _, err := Render(context.Background(), d, Limits{}); !errors.Is(err, ErrInvalid) {
			t.Fatal("invalid picture", err)
		}
	}
	d.Root = Picture{Data: data}
	l, _ := (Limits{}).normalized()
	p, err := prepare(context.Background(), d, l)
	if err != nil {
		t.Fatal(err)
	}
	key, _ := p.key(context.Background())
	data[0] = 0
	keyAgain, _ := p.key(context.Background())
	if key != keyAgain {
		t.Fatal("plan retained source bytes")
	}
	if _, err := p.render(context.Background(), l); err != nil {
		t.Fatal("mutating source corrupted prepared plan", err)
	}
}

func TestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	f := &testFont{metrics: FontMetrics{Width: 1, Height: 1, Advance: 1}, rows: []uint32{1}}
	d := Document{Width: 10, Height: 10, Root: Text{Value: "A", Font: f}}
	if _, err := Render(ctx, d, Limits{}); !errors.Is(err, context.Canceled) || f.calls != 0 {
		t.Fatal("pre-cancellation", err, f.calls)
	}
	ctx, cancel = context.WithCancel(context.Background())
	f.cancel = cancel
	if _, err := Render(ctx, d, Limits{}); !errors.Is(err, context.Canceled) {
		t.Fatal("font cancellation", err)
	}
	w := &outputWriter{ctx: ctx, limit: 100}
	if _, err := w.Write([]byte("PNG")); !errors.Is(err, context.Canceled) || w.Len() != 0 {
		t.Fatal("encoding cancellation", err)
	}
}
