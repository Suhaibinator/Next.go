package client

import (
	"strings"
	"testing"
)

func TestLocationRouteParamsOwnsDecodedActualPathValues(t *testing.T) {
	l := Location{Pathname: "/docs/workspace/project/Go%20WASM/%E6%9D%B1%E4%BA%AC/", Search: "?tag=one&tag=two", Hash: "#section"}
	first, ok := l.RouteParams("/workspace/*rest?", "/docs")
	if !ok || first["rest"] != "project/Go WASM/東京" {
		t.Fatal(first, ok)
	}
	first["rest"] = "mutated"
	next, ok := l.RouteParams("/workspace/*rest?", "/docs")
	if !ok || next["rest"] != "project/Go WASM/東京" || l.Query().Get("tag") != "one" || l.Hash != "#section" {
		t.Fatal("route values shared ownership or changed query/hash", next)
	}
	for _, pathname := range []string{"/docs/workspace", "/docs/workspace/"} {
		if params, ok := (Location{Pathname: pathname}).RouteParams("/workspace/*rest?", "/docs"); !ok || params["rest"] != "" {
			t.Fatal("optional zero-tail lost", pathname, params)
		}
	}
	if params, ok := (Location{Pathname: "/docs/item/a%252fb"}).RouteParams("/item/:id", "/docs"); !ok || params["id"] != "a%2fb" {
		t.Fatal("decoded more than once", params)
	}
}

func TestLocationRouteParamsRejectsMalformedAndOutOfMount(t *testing.T) {
	for _, pathname := range []string{"", "/workspace/a", "/docsevil/workspace/a", "/%64ocs/workspace/a", "/docs/workspace//", "/docs/workspace//a", "/docs/workspace/%2f", "/docs/workspace/%5C", "/docs/workspace/%2e", "/docs/workspace/%2e%2e", "/docs/workspace/%00", "/docs/workspace/%7f", "/docs/workspace/%ff", "/docs/workspace/%", "/docs/workspace/a?x=1", "/docs/workspace/a#x", "/docs/workspace/" + strings.Repeat("x", 8192)} {
		if params, ok := (Location{Pathname: pathname}).RouteParams("/workspace/*rest?", "/docs"); ok || params != nil {
			t.Fatal("invalid path admitted", pathname, params)
		}
	}
	for _, base := range []string{"docs", "/docs/", "/%64ocs", "/docs//nested", "/docs/..", strings.Repeat("x", 8193)} {
		if _, ok := (Location{Pathname: "/docs/workspace/a"}).RouteParams("/workspace/*rest?", base); ok {
			t.Fatal("invalid base admitted", base)
		}
	}
	for _, pattern := range []string{"workspace", "/workspace/*rest?/child", "/workspace/:9bad", "/workspace/..", "/" + strings.Repeat("x", 8193)} {
		if _, ok := (Location{Pathname: "/docs/workspace/a"}).RouteParams(pattern, "/docs"); ok {
			t.Fatal("invalid pattern admitted", pattern)
		}
	}
}

func TestLocationRouteParamsRemainsGenericForResourceSuffixes(t *testing.T) {
	for pathname, want := range map[string]string{
		"/docs/workspace/report.json":   "report.json",
		"/docs/workspace/report%2EJSON": "report.JSON",
		"/docs/workspace/report.json/":  "report.json",
		"/docs/workspace/module.WASM":   "module.WASM",
	} {
		params, ok := (Location{Pathname: pathname}).RouteParams("/workspace/*rest?", "/docs")
		if !ok || params["rest"] != want {
			t.Fatal("static-shell resource restriction leaked into generic params", pathname, params, ok)
		}
	}
}
