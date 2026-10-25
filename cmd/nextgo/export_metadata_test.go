package main

import (
	"context"
	"encoding/json"
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
	"github.com/Suhaibinator/Next.go/ui"
)

func TestManagedMetadataStaticExportExactBytesAndRollback(t *testing.T) {
	const robots = "User-agent: *\nDisallow: /private\n"
	app, err := nextgo.New(nextgo.Config{Phase: nextgo.PhaseStaticExport, BuildID: "metadata-build", BasePath: "/site", TrailingSlash: true,
		MetadataFiles: metadatafiles.Sources{Static: fstest.MapFS{"robots.txt": {Data: []byte(robots)}}},
		Routes: []nextgo.Route{{Pattern: "/", Page: func(*nextgo.Context) (nextgo.Page, error) {
			return nextgo.Page{Title: "Metadata export", Body: ui.Text("Public metadata and useful initial HTML")}, nil
		}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close(context.Background())
	config, err := app.StaticExportConfig()
	if err != nil || config.Version != 2 || len(config.Metadata) != 1 || config.Metadata[0].Path != "/site/robots.txt" {
		t.Fatal(config, err)
	}
	var broken atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if broken.Load() && r.URL.Path == "/site/robots.txt" {
			entry := config.Metadata[0]
			w.Header().Set("X-Nextgo-Build", config.Build)
			w.Header().Set("Content-Type", entry.ContentType)
			w.Header().Set("ETag", entry.ETag)
			_, _ = w.Write([]byte(strings.ReplaceAll(robots, "private", "changed")))
			return
		}
		app.ServeHTTP(w, r)
	}))
	defer server.Close()
	assets := t.TempDir()
	for _, name := range []string{"_nextgo", "public"} {
		if err := os.Mkdir(filepath.Join(assets, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	output := filepath.Join(t.TempDir(), "published")
	publish := func() error {
		return exportWithMetadata(context.Background(), server.URL, assets, output, config.Build, []string{"/"}, config.Paths, config.Scopes, config.Metadata)
	}
	if err := publish(); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(output, "site", "robots.txt")); err != nil || string(data) != robots {
		t.Fatal(string(data), err)
	}
	if _, err := os.Stat(filepath.Join(output, "site", "robots.txt", "index.html")); err == nil {
		t.Fatal("metadata must be an exact file independent of trailing slash policy")
	}
	data, err := os.ReadFile(filepath.Join(output, ".nextgo-export.json"))
	var manifest exportManifest
	if err != nil || json.Unmarshal(data, &manifest) != nil || !reflect.DeepEqual(manifest.Metadata, config.Metadata) {
		t.Fatal("publication did not retain the declared public inventory", string(data), err)
	}
	previous := exportDependencySnapshot(t, output)
	broken.Store(true)
	if err := publish(); err == nil || !strings.Contains(err.Error(), "changed content") {
		t.Fatal("same-length changed metadata with a spoofed ETag was accepted", err)
	}
	if !reflect.DeepEqual(previous, exportDependencySnapshot(t, output)) {
		t.Fatal("rejected metadata changed the previously published generation")
	}
}

func TestManagedMetadataInventoryRejectsUnownedPathsAndDuplicates(t *testing.T) {
	deployment := nextgo.PathConfig{BasePath: "/site", AssetPrefix: "https://cdn.example.test/static", TrailingSlash: true}
	entry, err := metadatafiles.Validate("robots.txt", []byte("User-agent: *\n"), metadatafiles.Options{BasePath: deployment.BasePath})
	if err != nil {
		t.Fatal(err)
	}
	if err := validateMetadataInventory(deployment, []metadatafiles.Entry{entry}); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*metadatafiles.Entry){
		func(e *metadatafiles.Entry) { e.Name = "../secret" },
		func(e *metadatafiles.Entry) { e.Path = "/site/../robots.txt" },
		func(e *metadatafiles.Entry) { e.Path = "/site/robots.txt/" },
		func(e *metadatafiles.Entry) { e.Path = "/static/robots.txt" },
		func(e *metadatafiles.Entry) { e.Digest = strings.Repeat("F", 64) },
		func(e *metadatafiles.Entry) { e.ETag = `W/` + e.ETag },
		func(e *metadatafiles.Entry) { e.Size = metadatafiles.MaxTotalBytes + 1 },
		func(e *metadatafiles.Entry) { e.ContentType = "text/plain\r\nX-Injected: true" },
	} {
		bad := entry
		mutate(&bad)
		if err := validateMetadataInventory(deployment, []metadatafiles.Entry{bad}); err == nil {
			t.Fatal("accepted invalid inventory", bad)
		}
	}
	if err := validateMetadataInventory(deployment, []metadatafiles.Entry{entry, entry}); err == nil {
		t.Fatal("accepted duplicate metadata resource")
	}
}
