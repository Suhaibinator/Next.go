package dev

import (
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestDevelopmentRetentionBoundsDiskAndServesPreviousAssets(t *testing.T) {
	root := t.TempDir()
	s := &supervisor{authority: "127.0.0.1:3000"}
	var builds []string
	for i := 0; i < retainedBuilds+4; i++ {
		id := fmt.Sprint(i)
		dir := filepath.Join(root, "build-"+id)
		builds = append(builds, dir)
		assets := filepath.Join(dir, "server", "assets", "_nextgo", id)
		if err := os.MkdirAll(assets, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(assets, "app.wasm"), []byte(id), 0644); err != nil {
			t.Fatal(err)
		}
		s.mu.Lock()
		expired := s.retainBuildLocked(id, dir)
		s.mu.Unlock()
		for _, dir := range expired {
			if err := os.RemoveAll(dir); err != nil {
				t.Fatal(err)
			}
		}
		if len(s.assets) > retainedBuilds || len(s.buildDirs) > retainedBuilds || len(s.buildOrder) > retainedBuilds {
			t.Fatal("generation metadata unbounded")
		}
	}
	for i, dir := range builds {
		_, err := os.Stat(dir)
		if i < 4 {
			if !os.IsNotExist(err) {
				t.Fatalf("expired compiler output retained: %s", dir)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		s.ServeHTTP(w, httptest.NewRequest("GET", fmt.Sprintf("http://127.0.0.1:3000/_nextgo/%d/app.wasm", i), nil))
		if w.Code != 200 || w.Body.String() != fmt.Sprint(i) {
			t.Fatalf("retained asset %d: %d %s", i, w.Code, w.Body.String())
		}
	}
}

func TestReplacingSameBuildIDOnlyExpiresItsOldDirectory(t *testing.T) {
	s := &supervisor{}
	if expired := s.retainBuildLocked("same", "/owned/first"); len(expired) != 0 {
		t.Fatal(expired)
	}
	expired := s.retainBuildLocked("same", "/owned/second")
	if len(expired) != 1 || expired[0] != "/owned/first" || len(s.buildOrder) != 1 || s.buildDirs["same"] != "/owned/second" {
		t.Fatalf("duplicate generation corrupted retention: %v %+v", expired, s)
	}
	if expired := s.retainBuildLocked("same", "/owned/second"); len(expired) != 0 {
		t.Fatal("active directory expired")
	}
	// Legacy/unowned asset entries cannot cause deletion of inferred parents.
	s.assets["external"] = "/never/delete/parents"
	s.retainBuildLocked("external", "/owned/new")
	if len(s.buildDirs) != 2 {
		t.Fatal("asset-only entries became owned builds")
	}
}
