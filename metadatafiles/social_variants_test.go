//go:build !js

package metadatafiles

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
)

func socialRasterFixture(t testing.TB, kind string) []byte {
	t.Helper()
	var out bytes.Buffer
	palette := color.Palette{color.NRGBA{R: 20, G: 90, B: 70, A: 255}, color.NRGBA{R: 240, G: 220, B: 60, A: 255}}
	first := image.NewPaletted(image.Rect(0, 0, 32, 24), palette)
	for y := 0; y < 24; y++ {
		for x := 0; x < 32; x++ {
			first.SetColorIndex(x, y, uint8((x+y)%2))
		}
	}
	var err error
	switch kind {
	case "jpeg":
		err = jpeg.Encode(&out, first, &jpeg.Options{Quality: 85})
	case "gif":
		second := image.NewPaletted(image.Rect(8, 8, 24, 16), palette)
		for i := range second.Pix {
			second.Pix[i] = 1
		}
		err = gif.EncodeAll(&out, &gif.GIF{Image: []*image.Paletted{first, second}, Delay: []int{5, 5}, LoopCount: 0, Config: image.Config{ColorModel: palette, Width: 32, Height: 24}})
	case "png":
		err = png.Encode(&out, first)
	}
	if err != nil || out.Len() == 0 {
		t.Fatal("fixture encoding", err)
	}
	return out.Bytes()
}

func TestSocialRasterVariantsAndOwnedHead(t *testing.T) {
	jpegData, gifData := socialRasterFixture(t, "jpeg"), socialRasterFixture(t, "gif")
	for _, name := range []string{"opengraph-image.jpg", "opengraph-image.jpeg", "twitter-image.jpg", "twitter-image.jpeg"} {
		entry, err := Validate(name, jpegData)
		if err != nil || entry.ContentType != "image/jpeg" || entry.Width != 32 || entry.Height != 24 {
			t.Fatal(entry, err)
		}
	}
	for _, name := range []string{"opengraph-image.gif", "twitter-image.gif"} {
		entry, err := Validate(name, gifData)
		if err != nil || entry.ContentType != "image/gif" || entry.Width != 32 || entry.Height != 24 {
			t.Fatal(entry, err)
		}
	}
	source := fstest.MapFS{"opengraph-image.jpeg": {Data: jpegData}, "twitter-image.gif": {Data: gifData}, "opengraph-image.alt.txt": {Data: []byte("Original JPEG")}, "twitter-image.alt.txt": {Data: []byte("Original GIF")}}
	set, err := New(context.Background(), Sources{Static: source}, Options{BasePath: "/site"})
	if err != nil {
		t.Fatal(err)
	}
	head, err := set.SocialHead("https://trusted.example/locale/fr")
	if err != nil {
		t.Fatal(err)
	}
	values := map[string]string{}
	for _, e := range head {
		values[e.Attrs["property"]+e.Attrs["name"]] = e.Attrs["content"]
	}
	if values["og:image"] != "https://trusted.example/site/opengraph-image.jpeg" || values["og:image:type"] != "image/jpeg" || values["twitter:image"] != "https://trusted.example/site/twitter-image.gif" {
		t.Fatal(values)
	}
	jpegData[0] = 0
	gifData[0] = 0
	for _, entry := range set.Entries() {
		data, _ := set.Read(entry.Name)
		if strings.HasSuffix(entry.Name, ".jpeg") && data[0] != 255 || strings.HasSuffix(entry.Name, ".gif") && data[0] != 'G' {
			t.Fatal("retained source alias")
		}
	}
}

func TestSocialVariantConflictsPrecedeProducers(t *testing.T) {
	for _, names := range [][]string{{"opengraph-image.png", "opengraph-image.jpg"}, {"twitter-image.jpg", "twitter-image.jpeg"}, {"twitter-image.gif", "twitter-image.png"}, {"opengraph-image.alt.txt"}} {
		if CheckSocialNames(names) == nil {
			t.Fatal("ambiguous/orphan declaration admitted", names)
		}
	}
	if err := CheckSocialNames([]string{"opengraph-image.jpg", "opengraph-image.alt.txt", "twitter-image.gif"}); err != nil {
		t.Fatal(err)
	}
	for _, source := range []Sources{
		{Static: fstest.MapFS{"opengraph-image.jpg": {Data: []byte("not decoded before conflict")}}, OpenGraphImage: func(context.Context) ([]byte, error) { t.Fatal("conflicting producer executed"); return nil, nil }},
		{Static: fstest.MapFS{"opengraph-image.jpg": {Data: []byte("not decoded")}, "opengraph-image.jpeg": {Data: []byte("not decoded")}}, TwitterImage: func(context.Context) ([]byte, error) {
			t.Fatal("producer executed before inventory validation")
			return nil, nil
		}},
	} {
		if _, err := New(context.Background(), source, Options{}); err == nil || !strings.Contains(err.Error(), "conflicting image variants") {
			t.Fatal(err)
		}
	}
}

func TestSocialRasterVariantsRejectMalformedAndBoundWork(t *testing.T) {
	jpegData, gifData := socialRasterFixture(t, "jpeg"), socialRasterFixture(t, "gif")
	for _, name := range []string{"opengraph-image.jpg", "twitter-image.gif"} {
		valid := jpegData
		if strings.HasSuffix(name, ".gif") {
			valid = gifData
		}
		for _, data := range [][]byte{nil, []byte("<svg/>"), valid[:len(valid)-8], make([]byte, MaxImageBytes+1)} {
			if _, err := Validate(name, data); err == nil {
				t.Fatal("invalid raster admitted", name)
			}
		}
	}
	// A bounded canvas alone is insufficient: reject excessive frames and
	// aggregate frame pixels before DecodeAll can allocate the animation.
	frame := []byte{0x2c, 0, 0, 0, 0, 1, 0, 1, 0, 0, 2, 0}
	header := append([]byte(nil), gifData[:13]...)
	header[10] &= 0x7f
	tooMany := append([]byte(nil), header...)
	for i := 0; i < MaxGIFFrames+1; i++ {
		tooMany = append(tooMany, frame...)
	}
	tooMany = append(tooMany, 0x3b)
	if boundedGIF(tooMany, 32, 24) {
		t.Fatal("excessive frames reached decoder")
	}
	large := append([]byte(nil), header...)
	binary.LittleEndian.PutUint16(large[6:], 2048)
	binary.LittleEndian.PutUint16(large[8:], 2048)
	bigFrame := append([]byte(nil), frame...)
	binary.LittleEndian.PutUint16(bigFrame[5:], 2048)
	binary.LittleEndian.PutUint16(bigFrame[7:], 2048)
	for i := 0; i < 3; i++ {
		large = append(large, bigFrame...)
	}
	large = append(large, 0x3b)
	if boundedGIF(large, 2048, 2048) {
		t.Fatal("aggregate pixel bomb reached decoder")
	}
	blocks := append(append([]byte(nil), header...), 0x21, 0xfe)
	for i := 0; i < MaxGIFBlocks; i++ {
		blocks = append(blocks, 1, 0)
	}
	blocks = append(blocks, 0, 0x3b)
	if boundedGIF(blocks, 32, 24) {
		t.Fatal("extension work bound bypassed")
	}
	// Repeated scan envelopes must fail before full JPEG coefficient work.
	scans := []byte{255, 0xd8}
	for i := 0; i < MaxJPEGScans+1; i++ {
		scans = append(scans, 255, 0xda, 0, 2)
	}
	scans = append(scans, 255, 0xd9)
	if boundedJPEG(scans) {
		t.Fatal("JPEG scan work bound bypassed")
	}
	markers := []byte{255, 0xd8}
	for i := 0; i < MaxJPEGMarkers+1; i++ {
		markers = append(markers, 255, 0xfe, 0, 2)
	}
	markers = append(markers, 255, 0xd9)
	if boundedJPEG(markers) {
		t.Fatal("JPEG marker work bound bypassed")
	}
	gifBomb := append([]byte(nil), gifData...)
	binary.LittleEndian.PutUint16(gifBomb[6:], MaxImageDimension+1)
	if _, err := Validate("opengraph-image.gif", gifBomb); err == nil {
		t.Fatal("GIF canvas bomb admitted")
	}
	// The SDK corpus is independent of this encoder and exercises supported
	// progressive scans. No third-party artwork is copied into this repository.
	corpus := filepath.Join(runtime.GOROOT(), "src/image/testdata/video-001.progressive.jpeg")
	if data, err := os.ReadFile(corpus); err == nil {
		if _, err := Validate("opengraph-image.jpg", data); err != nil {
			t.Fatal("valid SDK progressive JPEG rejected", err)
		}
	} else {
		t.Fatal("required SDK progressive JPEG corpus unavailable", err)
	}
}

func FuzzSocialRasterValidation(f *testing.F) {
	for i, kind := range []string{"jpeg", "gif", "png"} {
		f.Add(byte(i), socialRasterFixture(f, kind))
	}
	f.Fuzz(func(t *testing.T, kind byte, data []byte) {
		names := []string{"opengraph-image.jpg", "twitter-image.gif", "opengraph-image.png"}
		_, _ = Validate(names[int(kind)%len(names)], data)
	})
}
