package navigation

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/paths"
)

func TestCompiledManifestPreservesWireConfigurationAndSelection(t *testing.T) {
	for _, root := range []string{"", strings.Repeat("a", 64)} {
		manifest := Manifest{Routes: []Route{{Pattern: "/reports/new", Root: root}, {Pattern: "/reports/:id", Endpoint: true}, {Pattern: "/files/*tail?", Root: root}}, Handlers: []string{"/health"}, Documents: []string{"/native/*tail?"}}
		before, _ := json.Marshal(manifest)
		identity := ConfigurationID("build", paths.Config{}, manifest, true, false)
		compiled := manifest
		if err := compiled.CompileRoutes(); err != nil {
			t.Fatal(err)
		}
		after, _ := json.Marshal(compiled)
		if string(before) != string(after) || ConfigurationID("build", paths.Config{}, compiled, true, false) != identity {
			t.Fatal("compiled lookup changed public identity")
		}
		for _, path := range []string{"/reports/new", "/reports/new/", "/reports/a%20b", "/files", "/files/a/b", "/files/%2F", "/health", "/native/page", "/missing"} {
			if manifest.IsEndpoint(path, path) != compiled.IsEndpoint(path, path) || manifest.RootForPath(path) != compiled.RootForPath(path) || manifest.SameRoot(path, root) != compiled.SameRoot(path, root) {
				t.Fatal(path, "selection changed")
			}
		}
		manifest.Routes[0].Endpoint = true
		manifest.Routes[0].Root = strings.Repeat("b", 64)
		if compiled.IsEndpoint("/reports/new", "/reports/new") || compiled.RootForPath("/reports/new") != root {
			t.Fatal("compiled routes alias public input")
		}
	}
}

func TestCompiledManifestRejectsAmbiguousRoutesAndMalformedRoots(t *testing.T) {
	for _, routes := range [][]Route{{{Pattern: "/:id"}, {Pattern: "/:other"}}, {{Pattern: "/", Root: "bad"}}, {{Pattern: "/bad//path"}}} {
		manifest := Manifest{Routes: routes}
		if manifest.CompileRoutes() == nil {
			t.Fatal("invalid manifest compiled", routes)
		}
	}
}
