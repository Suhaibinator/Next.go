package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLazyComponentConfigurationAndDiscovery(t *testing.T) {
	for _, mode := range []string{"", `{"loading":"eager"}`, `{"loading":"lazy"}`} {
		files := map[string]string{"app/page.go": "package app", "components/counter/client_js.go": "package counter"}
		if mode != "" {
			files["components/counter/component.json"] = mode
		}
		p, err := Discover(fixture(t, files))
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Components) != 1 || p.Components[0].Lazy != (mode == `{"loading":"lazy"}`) {
			t.Fatalf("unexpected component: %+v", p.Components)
		}
	}
	for _, invalid := range []string{`{}`, `null`, `{"loading":"other"}`, `{"loading":"lazy","extra":true}`, `{"loading":"lazy","loading":"eager"}`, `{"loading":1}`, strings.Repeat(" ", 1025)} {
		root := fixture(t, map[string]string{"app/page.go": "package app", "components/counter/client_js.go": "package counter", "components/counter/component.json": invalid})
		if _, err := Discover(root); err == nil {
			t.Fatalf("accepted %q", invalid)
		}
	}
	files := map[string]string{"app/page.go": "package app"}
	for i := 0; i < 9; i++ {
		files[fmt.Sprintf("components/c%d/client_js.go", i)] = "package counter"
		files[fmt.Sprintf("components/c%d/component.json", i)] = `{"loading":"lazy"}`
	}
	if _, err := Discover(fixture(t, files)); err == nil {
		t.Fatal("accepted more runtimes than the documented bound")
	}
}

func TestLazyClientEntrypointSeparatesApplicationImports(t *testing.T) {
	p := &Project{Components: []Component{{Name: "eager", Import: "example.com/eager"}, {Name: "lazy", Import: "example.com/lazy", Lazy: true}}}
	modules := []lazyOutput{{Name: "lazy", File: "module-abc.wasm", Digest: "abc", Epoch: "epoch", Size: 123}}
	source := clientSource(p, modules)
	for _, want := range []string{`example.com/eager`, `client.Register("eager",c0.Mount)`, `client.RegisterLazy("lazy"`, `module-abc.wasm`} {
		if !strings.Contains(source, want) {
			t.Fatalf("missing %s: %s", want, source)
		}
	}
	if strings.Contains(source, "example.com/lazy") || strings.Contains(source, `client.Register("lazy"`) {
		t.Fatal("lazy application implementation entered the core entrypoint", source)
	}
}

func TestLazyMetadataChangesBuildFingerprint(t *testing.T) {
	root := fixture(t, map[string]string{"app/page.go": "package app", "components/counter/client_js.go": "package counter"})
	before, err := Fingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(root, "components/counter/component.json")
	if err := os.WriteFile(file, []byte(`{"loading":"lazy"}`), 0600); err != nil {
		t.Fatal(err)
	}
	after, err := Fingerprint(root)
	if err != nil {
		t.Fatal(err)
	}
	if before == after {
		t.Fatal("lazy opt-in did not invalidate the application build")
	}
	a := lazyIdentity([]lazyOutput{{Name: "one", Digest: "abc", Epoch: "epoch", Size: 3}})
	b := lazyIdentity([]lazyOutput{{Name: "one", Digest: "def", Epoch: "epoch", Size: 3}})
	if a == b || a == lazyIdentity(nil) {
		t.Fatal("module bytes are absent from build identity")
	}
}
