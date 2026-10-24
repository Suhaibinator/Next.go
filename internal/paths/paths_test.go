package paths

import (
	"strings"
	"testing"
)

func TestMountAndAssetURLRules(t *testing.T) {
	for _, test := range []struct {
		config               Config
		page, asset, runtime string
	}{
		{Config{}, "/posts?q=go#intro", "/assets/icon.svg?v=1", "/_nextgo/build/"},
		{Config{BasePath: "/docs"}, "/docs/posts?q=go#intro", "/docs/assets/icon.svg?v=1", "/docs/_nextgo/build/"},
		{Config{BasePath: "/docs", AssetPrefix: "/cdn"}, "/docs/posts?q=go#intro", "/cdn/assets/icon.svg?v=1", "/cdn/_nextgo/build/"},
		{Config{BasePath: "/docs", AssetPrefix: "/"}, "/docs/posts?q=go#intro", "/assets/icon.svg?v=1", "/_nextgo/build/"},
		{Config{BasePath: "/docs", AssetPrefix: "https://cdn.example.com/v1"}, "/docs/posts?q=go#intro", "https://cdn.example.com/v1/assets/icon.svg?v=1", "https://cdn.example.com/v1/_nextgo/build/"},
	} {
		if err := test.config.Validate(); err != nil {
			t.Fatal(err)
		}
		if got, err := test.config.URL("/posts?q=go#intro"); err != nil || got != test.page {
			t.Fatalf("page URL: %s %v", got, err)
		}
		if got, err := test.config.AssetURL("/icon.svg?v=1"); err != nil || got != test.asset {
			t.Fatalf("asset URL: %s %v", got, err)
		}
		if got := test.config.RuntimeURL("build"); got != test.runtime {
			t.Fatalf("runtime URL: %s", got)
		}
		if !test.config.IsAssetURL(test.asset) {
			t.Fatalf("rejected configured asset: %s", test.asset)
		}
	}
	c := Config{BasePath: "/docs"}
	if got, err := c.URL("/"); err != nil || got != "/docs" {
		t.Fatalf("mounted root: %q %v", got, err)
	}
	for _, path := range []string{"/docs", "/docs/", "/docs/posts"} {
		if _, ok := c.Strip(path); !ok {
			t.Fatal(path)
		}
	}
	for _, path := range []string{"/docstring", "/other/docs", "/%64ocs/posts", "/docs%2Fposts"} {
		if _, ok := c.Strip(path); ok {
			t.Fatalf("accepted mount alias: %s", path)
		}
	}
}
func TestRejectUnsafeConfigurationsAndURLInputs(t *testing.T) {
	if err := (Config{BasePath: "/" + strings.Repeat("a", 8192)}).Validate(); err == nil {
		t.Fatal("accepted oversized deployment prefix")
	}
	for _, base := range []string{"/", "relative", "//foreign", "/a/", "/a//b", "/a/../b", "/a%2Fb", "/café", "/a?x", "/a#x", "/a\\b"} {
		if err := (Config{BasePath: base}).Validate(); err == nil {
			t.Errorf("accepted base %q", base)
		}
	}
	for _, prefix := range []string{"javascript:alert(1)", "//foreign", "/a/", "https://user:secret@cdn.test", "https://cdn.test/x?query", "https://cdn.test/x#", "https://cdn.test/%2e%2e", "https://cdn.test:99999", "https://cdn.test/../x", "https://cdn.test/"} {
		if err := (Config{AssetPrefix: prefix}).Validate(); err == nil {
			t.Errorf("accepted prefix %q", prefix)
		}
	}
	c := Config{BasePath: "/app", AssetPrefix: "https://cdn.test/v1"}
	for _, value := range []string{"relative", "//foreign/a", "https://foreign/a", "javascript:alert(1)", "/a//b", "/a/../b", "/a%2Fb", "/a%5Cb", "/a%00b"} {
		if _, err := c.URL(value); err == nil {
			t.Errorf("accepted URL %q", value)
		}
	}
	for _, value := range []string{"https://foreign.test/v1/assets/a.css", "https://user@cdn.test/v1/assets/a.css", "https://cdn.test/v1/assets/../a.css", "https://cdn.test/v1/assets/%2e%2e/a.css", "https://cdn.test/v10/assets/a.css", "https://cdn.test/v1/_nextgo/a.css", "/v1/assets/a.css"} {
		if c.IsAssetURL(value) {
			t.Errorf("trusted foreign/escaping asset %q", value)
		}
	}
}
