package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/metadatafiles"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/socialimage"
	"github.com/Suhaibinator/Next.go/ui"
)

func TestSocialMetadataStaticPublicationGlobalFilesAndRollback(t *testing.T) {
	producer := func(ctx context.Context) ([]byte, error) {
		return socialimage.Render(ctx, socialimage.Document{Width: 1200, Height: 630, Root: socialimage.Panel{Color: color.NRGBA{R: 20, G: 90, B: 70, A: 255}}}, socialimage.Limits{MaxOutputBytes: metadatafiles.MaxImageBytes})
	}
	app, err := nextgo.New(nextgo.Config{Phase: nextgo.PhaseStaticExport, BuildID: "social-export", BasePath: "/site", AssetPrefix: "/cdn", TrailingSlash: true, Locales: &nextgo.LocaleConfig{Codes: []string{"en", "fr"}, Default: "en"},
		MetadataFiles: metadatafiles.Sources{OpenGraphImage: producer, TwitterImage: producer, Static: fstest.MapFS{"opengraph-image.alt.txt": {Data: []byte("Public native card\n")}}},
		Routes: []nextgo.Route{{Pattern: "/", Page: func(*nextgo.Context) (nextgo.Page, error) {
			return nextgo.Page{Metadata: seo.Metadata{BaseURL: "https://trusted.example/base/fr"}, Body: ui.El("img", ui.Attrs{"src": "/site/opengraph-image.png", "alt": "Public card"})}, nil
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close(context.Background())
	config, err := app.StaticExportConfig()
	if err != nil || config.Version != 2 || len(config.Metadata) != 3 {
		t.Fatal(config, err)
	}
	var broken atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/opengraph-image.png") && broken.Load() > 0 {
			response := httptest.NewRecorder()
			app.ServeHTTP(response, r)
			for key, values := range response.Header() {
				w.Header()[key] = values
			}
			body := append([]byte(nil), response.Body.Bytes()...)
			switch broken.Load() {
			case 1:
				body[len(body)-1] ^= 1
			case 2:
				w.Header().Set("Content-Type", "image/jpeg")
			case 3:
				w.Header().Set("Set-Cookie", "private=1")
			}
			w.WriteHeader(response.Code)
			_, _ = w.Write(body)
			return
		}
		app.ServeHTTP(w, r)
	}))
	defer server.Close()
	assets := t.TempDir()
	for _, name := range []string{"_nextgo", "public"} {
		if err := os.Mkdir(filepath.Join(assets, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(t.TempDir(), "published")
	publish := func(entries []metadatafiles.Entry) error {
		return exportWithMetadata(context.Background(), server.URL, assets, out, config.Build, []string{"/", "/fr"}, config.Paths, config.Scopes, entries)
	}
	if err := publish(config.Metadata); err != nil {
		t.Fatal(err)
	}
	for _, entry := range config.Metadata {
		data, err := os.ReadFile(filepath.Join(out, strings.TrimPrefix(entry.Path, "/")))
		sum := sha256.Sum256(data)
		if err != nil || int64(len(data)) != entry.Size || hex.EncodeToString(sum[:]) != entry.Digest {
			t.Fatal(entry, err)
		}
		if strings.HasSuffix(entry.Name, ".png") {
			image, err := png.DecodeConfig(bytes.NewReader(data))
			if err != nil || image.Width != 1200 || image.Height != 630 {
				t.Fatal(image, err)
			}
		}
		if _, err := os.Stat(filepath.Join(out, "site/fr", entry.Name)); !os.IsNotExist(err) {
			t.Fatal("global metadata copied to a locale alias", entry, err)
		}
	}
	for _, path := range []string{"site/index.html", "site/fr/index.html"} {
		data, err := os.ReadFile(filepath.Join(out, path))
		if err != nil || !bytes.Contains(data, []byte(`content="https://trusted.example/site/opengraph-image.png"`)) || bytes.Contains(data, []byte("/site/fr/opengraph-image.png")) {
			t.Fatal(path, err, string(data))
		}
	}
	previous := exportDependencySnapshot(t, out)
	for mode := int32(1); mode <= 3; mode++ {
		broken.Store(mode)
		if err := publish(config.Metadata); err == nil {
			t.Fatal("bad social resource published", mode)
		}
		if !reflect.DeepEqual(previous, exportDependencySnapshot(t, out)) {
			t.Fatal("failure replaced previous publication", mode)
		}
	}
	broken.Store(0)
	entries := append([]metadatafiles.Entry(nil), config.Metadata...)
	for i := range entries {
		if entries[i].Name == "opengraph-image.png" {
			entries[i].Width++
		}
	}
	if err := publish(entries); err == nil {
		t.Fatal("mismatching declared image dimensions published")
	}
	if !reflect.DeepEqual(previous, exportDependencySnapshot(t, out)) {
		t.Fatal("bad declaration replaced previous publication")
	}
}
