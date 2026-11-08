package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/metadatafiles"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

func TestSocialVariantStaticExportAndConflictRollback(t *testing.T) {
	app, err := nextgo.New(nextgo.Config{Phase: nextgo.PhaseStaticExport, BuildID: "variants", BasePath: "/site", AssetPrefix: "/cdn", TrailingSlash: true,
		MetadataFiles: metadatafiles.Sources{Static: os.DirFS("../../examples/social-variants/app")}, Routes: []nextgo.Route{{Pattern: "/", Page: func(*nextgo.Context) (nextgo.Page, error) {
			return nextgo.Page{Metadata: seo.Metadata{BaseURL: "https://trusted.example"}, Body: ui.Text("JPEG and animated GIF")}, nil
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close(context.Background())
	config, err := app.StaticExportConfig()
	if err != nil || len(config.Metadata) != 4 {
		t.Fatal(config, err)
	}
	server := httptest.NewServer(app)
	defer server.Close()
	assets := t.TempDir()
	for _, name := range []string{"_nextgo", "public"} {
		if err := os.Mkdir(filepath.Join(assets, name), 0755); err != nil {
			t.Fatal(err)
		}
	}
	out := filepath.Join(t.TempDir(), "published")
	if err := exportWithMetadata(context.Background(), server.URL, assets, out, config.Build, []string{"/"}, config.Paths, config.Scopes, config.Metadata); err != nil {
		t.Fatal(err)
	}
	for _, entry := range config.Metadata {
		data, err := os.ReadFile(filepath.Join(out, strings.TrimPrefix(entry.Path, "/")))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(data)
		if hex.EncodeToString(digest[:]) != entry.Digest {
			t.Fatal("variant bytes changed")
		}
	}
	previous := exportDependencySnapshot(t, out)
	duplicate := config.Metadata[0]
	duplicate.Name = "opengraph-image.png"
	duplicate.Path = "/site/" + duplicate.Name
	duplicate.ContentType = "image/png"
	conflict := append(append([]metadatafiles.Entry(nil), config.Metadata...), duplicate)
	if err := exportWithMetadata(context.Background(), server.URL, assets, out, config.Build, []string{"/"}, config.Paths, config.Scopes, conflict); err == nil || !strings.Contains(err.Error(), "conflicting image variants") {
		t.Fatal("export admitted ambiguous role", err)
	}
	if !reflect.DeepEqual(previous, exportDependencySnapshot(t, out)) {
		t.Fatal("invalid variant inventory replaced prior publication")
	}
}
