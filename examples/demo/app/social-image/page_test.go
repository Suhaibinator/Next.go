//go:build !js

package socialimagepage

import (
	"bytes"
	"context"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/internal/paths"
	"github.com/Suhaibinator/Next.go/seo"
)

func TestSocialPageUsesEscapedPublicContentAndMetadata(t *testing.T) {
	p, err := Page(&nextgo.Context{Context: context.Background(), Query: url.Values{"title": {`<script>"Go"</script>`}, "description": {"A & B"}}})
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	if err := p.Body.Render(&body); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body.String(), "<script>") || !strings.Contains(body.String(), "&lt;script&gt;") {
		t.Fatal("unescaped form/image text", body.String())
	}
	if _, err := seo.Elements(p.Metadata); err != nil {
		t.Fatal(err)
	}
	if len(p.Metadata.OpenGraph.Images) != 1 || !strings.HasPrefix(p.Metadata.OpenGraph.Images[0].URL, "https://example.com/api/social?") {
		t.Fatal(p.Metadata)
	}
}

func TestSocialPageResolvesMountedAssetsAndImageURLs(t *testing.T) {
	c := &nextgo.Context{Context: paths.WithContext(context.Background(), paths.Config{BasePath: "/site", AssetPrefix: "https://cdn.example.test/static"})}
	p, err := Page(c)
	if err != nil {
		t.Fatal(err)
	}
	var body bytes.Buffer
	if err := p.Body.Render(&body); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body.String(), `src="/site/api/social?`) || !strings.Contains(body.String(), `action="/site/social-image"`) {
		t.Fatal("mounted route paths", body.String())
	}
	if !strings.HasPrefix(p.Metadata.OpenGraph.Images[0].URL, "https://example.com/site/api/social?") {
		t.Fatal("mounted crawler URL", p.Metadata)
	}
}

func TestSocialPageNativeHeadUsesDeploymentAndDeclaresServer(t *testing.T) {
	a, err := nextgo.New(nextgo.Config{BuildID: "social-test", BasePath: "/site", AssetPrefix: "https://cdn.example.test/static", Routes: []nextgo.Route{{Pattern: "/social-image", Page: Page}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"GET", "HEAD"} {
		w := httptest.NewRecorder()
		a.ServeHTTP(w, httptest.NewRequest(method, "http://example.test/site/social-image", nil))
		if w.Code != 200 || w.Header().Get(nextgo.RequiresServerHeader) != "1" {
			t.Fatal(method, w.Code, w.Header(), w.Body.String())
		}
		if method == "HEAD" {
			if w.Body.Len() != 0 {
				t.Fatal("HEAD body", w.Body.String())
			}
			continue
		}
		for _, want := range []string{`href="https://cdn.example.test/static/assets/social-image.css"`, `src="/site/api/social?`, `content="https://example.com/site/api/social?`} {
			if !strings.Contains(w.Body.String(), want) {
				t.Fatalf("missing %s in %s", want, w.Body.String())
			}
		}
		if strings.Contains(w.Body.String(), "/site/site/") {
			t.Fatal("deployment applied twice")
		}
	}
}
