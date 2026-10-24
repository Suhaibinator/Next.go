//go:build js && wasm

package client

import "testing"

func TestPublicRuntimeLookup(t *testing.T) {
	original := routeManifest.Public
	defer func() { routeManifest.Public = original }()
	routeManifest.Public = map[string]string{"message": "runtime", "empty": ""}
	if value, ok := PublicValue("message"); !ok || value != "runtime" {
		t.Fatal("missing setting")
	}
	if value, ok := PublicValue("empty"); !ok || value != "" {
		t.Fatal("empty setting lost")
	}
	if _, ok := PublicValue("unknown"); ok {
		t.Fatal("unknown setting present")
	}
}
