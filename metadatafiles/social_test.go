//go:build !js

package metadatafiles

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/fstest"
)

func TestSocialImageProducersFrozenHeadAndOwnership(t *testing.T) {
	image := pngData(t, 1200, 630)
	var calls int
	sources := Sources{Static: fstest.MapFS{"opengraph-image.alt.txt": {Data: []byte("A public <Go> card\n")}, "twitter-image.png": {Data: image}, "twitter-image.alt.txt": {Data: []byte("Public Twitter card")}},
		OpenGraphImage: func(context.Context) ([]byte, error) { calls++; return image, nil },
	}
	set, err := New(context.Background(), sources, Options{BasePath: "/site"})
	if err != nil || calls != 1 || len(set.Entries()) != 4 {
		t.Fatal(set, calls, err)
	}
	image[0] = 0
	head, err := set.SocialHead("https://trusted.example/base/fr")
	if err != nil || len(head) != 8 {
		t.Fatal(head, err)
	}
	values := map[string]string{}
	for _, e := range head {
		values[e.Attrs["property"]+e.Attrs["name"]] = e.Attrs["content"]
	}
	for key, expected := range map[string]string{"og:image": "https://trusted.example/site/opengraph-image.png", "og:image:alt": "A public <Go> card", "og:image:type": "image/png", "og:image:width": "1200", "og:image:height": "630", "twitter:image": "https://trusted.example/site/twitter-image.png", "twitter:image:alt": "Public Twitter card", "twitter:card": "summary_large_image"} {
		if values[key] != expected {
			t.Fatal(key, values[key], expected)
		}
	}
	head[0].Attrs["content"] = "mutated"
	again, _ := set.SocialHead("https://trusted.example")
	if again[0].Attrs["content"] != values["og:image"] {
		t.Fatal("borrowed head map")
	}
	if empty, err := set.SocialHead(""); err != nil || len(empty) != 0 {
		t.Fatal("missing base inferred a request origin", empty, err)
	}
	if _, err := set.SocialHead("https://bad.example/?query=1"); err == nil {
		t.Fatal("accepted invalid metadata base")
	}
	for _, name := range []string{"opengraph-image.png", "twitter-image.png"} {
		data, _ := set.Read(name)
		if data[0] != 137 {
			t.Fatal("producer or source retained caller bytes")
		}
	}
}

func TestSocialImageSourceAdmissionBeforeProducers(t *testing.T) {
	image := pngData(t, 32, 32)
	for _, test := range []struct {
		name    string
		sources Sources
	}{
		{"static-conflict", Sources{Static: fstest.MapFS{"opengraph-image.png": {Data: image}}, OpenGraphImage: func(context.Context) ([]byte, error) { t.Fatal("conflicting producer executed"); return nil, nil }}},
		{"orphan-alt", Sources{Static: fstest.MapFS{"opengraph-image.alt.txt": {Data: []byte("alt")}}, TwitterImage: func(context.Context) ([]byte, error) {
			t.Fatal("producer executed before complete inventory validation")
			return nil, nil
		}}},
		{"wrong-format", Sources{OpenGraphImage: func(context.Context) ([]byte, error) { return []byte("<svg/>"), nil }}},
		{"oversize", Sources{TwitterImage: func(context.Context) ([]byte, error) { return make([]byte, MaxImageBytes+1), nil }}},
		{"panic", Sources{TwitterImage: func(context.Context) ([]byte, error) { panic("private producer detail") }}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := New(context.Background(), test.sources, Options{})
			if err == nil || strings.Contains(err.Error(), "private producer detail") {
				t.Fatal("bad producer admitted or diagnostic leaked", err)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	_, err := New(ctx, Sources{OpenGraphImage: func(context.Context) ([]byte, error) { cancel(); return image, nil }}, Options{})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled producer published", err)
	}
}

func TestSocialAltValidationAndPNGAdmission(t *testing.T) {
	for _, name := range []string{"opengraph-image.alt.txt", "twitter-image.alt.txt"} {
		if _, err := Validate(name, []byte("A & B <public>\n")); err != nil {
			t.Fatal(err)
		}
		for _, data := range [][]byte{nil, []byte(" \n"), []byte("first\nsecond"), {255}, []byte(strings.Repeat("x", MaxAltBytes+1))} {
			if _, err := Validate(name, data); err == nil {
				t.Fatal("bad alt admitted", name)
			}
		}
	}
	for _, name := range []string{"opengraph-image.png", "twitter-image.png"} {
		data := pngData(t, 1200, 630)
		entry, err := Validate(name, data)
		if err != nil || entry.Width != 1200 || entry.Height != 630 || entry.ContentType != "image/png" {
			t.Fatal(entry, err)
		}
		if _, err := Validate(name, data[:len(data)-10]); err == nil {
			t.Fatal("truncated raster admitted")
		}
	}
}
