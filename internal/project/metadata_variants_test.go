package project

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSocialVariantsDiscoveryCopyAndProducerConflict(t *testing.T) {
	jpeg, err := os.ReadFile("../../examples/social-variants/app/opengraph-image.jpg")
	if err != nil {
		t.Fatal(err)
	}
	gif, err := os.ReadFile("../../examples/social-variants/app/twitter-image.gif")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{"app/page.go": "package app", "app/opengraph-image.jpg": string(jpeg), "app/twitter-image.gif": string(gif), "app/twitter-image.alt.txt": "Original animated card"}
	root := fixture(t, files)
	p, err := Discover(root)
	if err != nil || len(p.MetadataFiles) != 3 {
		t.Fatal(p, err)
	}
	copy := filepath.Join(t.TempDir(), "metadata")
	if err := CopyMetadataFiles(context.Background(), p, copy); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(filepath.Join(copy, "opengraph-image.jpg")); err != nil || string(data) != string(jpeg) {
		t.Fatal("JPEG copy changed", err)
	}
	if err := os.WriteFile(filepath.Join(root, "app/opengraph-image.go"), []byte("//go:build !js\n\npackage app\nfunc OpenGraphImage(){}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := Discover(root); err == nil || !strings.Contains(err.Error(), "conflicting image variants") {
		t.Fatal("static JPEG and PNG producer silently overlapped", err)
	}
	delete(files, "app/opengraph-image.jpg")
	files["app/opengraph-image.jpeg"] = string(jpeg)
	if _, err := Discover(fixture(t, files)); err != nil {
		t.Fatal("jpeg extension discovery", err)
	}
}
