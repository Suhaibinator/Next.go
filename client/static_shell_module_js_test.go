//go:build js && wasm && nextgo_module

package client

import (
	"encoding/json"
	"strings"
	"syscall/js"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/paths"
)

func TestModuleRouteParamsUsesExistingLocationAndPathsSnapshot(t *testing.T) {
	oldDoc, oldPaths, oldLocation, oldURL := doc, deployment, committedLocation, currentURL
	oldPublic, oldSelections, oldFrozen := routeManifest.Public, selections, draftFrozen
	oldObservers, oldSelectionObservers := locationObservers, selectionObservers
	t.Cleanup(func() {
		doc, deployment, committedLocation, currentURL = oldDoc, oldPaths, oldLocation, oldURL
		routeManifest.Public, selections, draftFrozen = oldPublic, oldSelections, oldFrozen
		locationObservers, selectionObservers = oldObservers, oldSelectionObservers
	})
	configuration := strings.Repeat("b", 64)
	getAttribute := js.FuncOf(func(js.Value, []js.Value) any { return configuration })
	defer getAttribute.Release()
	doc = js.ValueOf(map[string]any{"body": map[string]any{"getAttribute": getAttribute}})
	locationObservers = map[uint64]*locationObserver{}
	selectionObservers = map[uint64]selectionObserver{}
	state := moduleState{ResourceEpoch: resourceEpoch, Paths: paths.Config{BasePath: "/app"}, Location: Location{URL: "https://site.test/app/workspace/module%20path?q=module", Pathname: "/app/workspace/module%20path", Search: "?q=module"}, Ready: true, Configuration: configuration}
	raw, err := json.Marshal(state)
	if err != nil {
		t.Fatal(err)
	}
	if err := syncModule(string(raw)); err != nil {
		t.Fatal(err)
	}
	params, ok := CurrentRouteParams("/workspace/*rest?")
	if !ok || params["rest"] != "module path" {
		t.Fatal("module did not consume the core's committed URL", params, ok)
	}
	if location, ready := CurrentLocation(); !ready || location.Query().Get("q") != "module" {
		t.Fatal(location, ready)
	}
	if registeredStaticShellInitializer != nil || registeredStaticShellFetch != nil {
		t.Fatal("module target linked document-owned static shell hooks")
	}
}
