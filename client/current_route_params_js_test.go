//go:build js && wasm

package client

import (
	"testing"

	"github.com/Suhaibinator/Next.go/internal/paths"
)

// This runs in both eager/core and lazy-module WASM targets. No browser router
// or host method participates in this value helper.
func TestCurrentRouteParamsUsesOnlyCommittedLocationAndDeployment(t *testing.T) {
	locationEnvironment(t)
	oldDeployment := deployment
	t.Cleanup(func() { deployment = oldDeployment })
	deployment = paths.Config{BasePath: "/app"}
	if _, ok := CurrentRouteParams("/workspace/*rest?"); ok {
		t.Fatal("route params became available before Run")
	}
	setLocation("https://site.test/app/workspace/a%20b/%E2%98%83?tag=one&tag=two#part")
	params, ok := CurrentRouteParams("/workspace/*rest?")
	if !ok || params["rest"] != "a b/☃" {
		t.Fatal(params, ok)
	}
	params["rest"] = "caller-owned"
	again, ok := CurrentRouteParams("/workspace/*rest?")
	if !ok || again["rest"] != "a b/☃" {
		t.Fatal("caller mutation escaped its map", again)
	}
	if location, ready := CurrentLocation(); !ready || location.Query().Get("tag") != "one" || location.Hash != "#part" {
		t.Fatal(location, ready)
	}
	// The live location from locationEnvironment is still a pending URL.
	// Updating the committed snapshot is the only way to publish new params.
	setLocation("https://site.test/app/workspace")
	if params, ok := CurrentRouteParams("/workspace/*rest?"); !ok || params["rest"] != "" {
		t.Fatal("optional empty tail failed", params, ok)
	}
	for _, pathname := range []string{"/app/workspace/a%2fb", "/app/workspace/%2e", "/%61pp/workspace/a", "/application/workspace/a"} {
		committedLocation = Location{URL: "https://site.test" + pathname, Pathname: pathname}
		if params, ok := CurrentRouteParams("/workspace/*rest?"); ok {
			t.Fatal("malformed or outside committed path admitted", pathname, params)
		}
	}
}
