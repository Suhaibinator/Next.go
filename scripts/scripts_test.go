//go:build !js

package scripts

import (
	"context"
	"encoding/base64"
	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/ui"
	"strings"
	"testing"
)

func TestExternalDescriptorsRejectExecutableAndAmbiguousInput(t *testing.T) {
	good := Script{ID: "library", Src: "https://cdn.example/library.js", Integrity: "sha256-" + base64.StdEncoding.EncodeToString(make([]byte, 32))}
	checked, err := good.Check()
	if err != nil || checked.Strategy != AfterInteractive || checked.CrossOrigin != "anonymous" {
		t.Fatal(checked, err)
	}
	for _, src := range []string{"javascript:alert(1)", "data:text/javascript,alert(1)", "blob:https://example/123", "//cdn.example/x.js", "relative.js", "https://user:secret@cdn.example/x.js", "/x.js#fragment", "/x\\y.js", "/x\ny.js", " https://cdn.example/x.js"} {
		bad := good
		bad.Src = src
		if _, err := bad.Check(); err == nil {
			t.Fatal("accepted unsafe source", src)
		}
	}
	for _, change := range []func(*Script){func(s *Script) { s.ID = "bad\"id" }, func(s *Script) { s.Strategy = "worker" }, func(s *Script) { s.Type = "importmap" }, func(s *Script) { s.Integrity = "sha256-invalid" }, func(s *Script) { s.ReferrerPolicy = "unknown" }, func(s *Script) { s.CrossOrigin = "anything" }, func(s *Script) { s.Asset = true }} {
		bad := good
		change(&bad)
		if _, err := bad.Check(); err == nil {
			t.Fatal("accepted invalid script", bad)
		}
	}
	for _, raw := range []string{`null`, `{"id":"x","id":"y","src":"/x.js"}`, `{"id":"x","src":"/x.js","onload":"alert(1)"}`, strings.Repeat(" ", 8193)} {
		if _, err := Parse(raw); err == nil {
			t.Fatal("accepted malformed declaration", raw)
		}
	}
	node, err := ui.HTML(Node(Script{ID: "asset", Src: "/library.js?x=\"value\"", Asset: true}))
	if err != nil || strings.Contains(node, "<script") || strings.Contains(node, `x="value"`) {
		t.Fatal("declaration escaped its inert marker", node, err)
	}
	if _, err := ui.HTML(Node(Script{ID: "early", Src: "/x.js", Strategy: BeforeInteractive})); err == nil {
		t.Fatal("early script allowed in page content")
	}
}
func TestEarlyRendererAndNonceContextAreExplicit(t *testing.T) {
	nonce, err := NewNonce()
	if err != nil {
		t.Fatal(err)
	}
	ctx, err := WithNonce(context.Background(), nonce)
	if err != nil || Nonce(ctx) != nonce || !cache.Bypassed(ctx) || Nonce(context.Background()) != "" {
		t.Fatal("nonce context", err)
	}
	for _, bad := range []string{"", `bad"nonce-value-1234`, strings.Repeat("x", 129)} {
		if _, err := WithNonce(context.Background(), bad); err == nil {
			t.Fatal("accepted nonce", bad)
		}
	}
	declarations := []Script{{ID: "first", Src: "/first.js", Asset: true, Strategy: BeforeInteractive}, {ID: "second", Src: "https://cdn.example/second.js", Strategy: BeforeInteractive}}
	markup, err := EarlyHTML(declarations, func(src string) (string, error) { return "/cdn/assets" + src, nil }, nonce)
	if err != nil || strings.Index(markup, "first.js") > strings.Index(markup, "second.js") || strings.Count(markup, `nonce="`+nonce+`"`) != 2 || !strings.Contains(markup, "/cdn/assets/first.js") {
		t.Fatal(markup, err)
	}
	if _, err := EarlyHTML(declarations, func(string) (string, error) { return "javascript:alert(1)", nil }, nonce); err == nil {
		t.Fatal("unsafe resolved URL accepted")
	}
}
