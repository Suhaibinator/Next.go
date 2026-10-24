package seo

import "testing"

func TestMetadataURLBaseResolutionAndInputOwnership(t *testing.T) {
	for _, tc := range []struct{ base, value, want string }{{"https://example.test/docs", "article?q=go#section", "https://example.test/docs/article?q=go#section"}, {"https://example.test/docs/", "/root", "https://example.test/root"}, {"https://example.test", "https://other.test/x", "https://other.test/x"}, {"https://example.test/base", "../parent", "https://example.test/parent"}} {
		if got, err := ResolveURL(tc.base, tc.value); err != nil || got != tc.want {
			t.Fatal(tc, got, err)
		}
	}
	for _, value := range []string{"//foreign.test/a", "javascript:alert(1)", "https://user:secret@example.test/a", "/bad\\path"} {
		if _, err := ResolveURL("https://example.test", value); err == nil {
			t.Fatal("unsafe metadata URL accepted", value)
		}
	}
	for _, base := range []string{"relative", "https://example.test/?q=x", "https://example.test/#x", "https://example.test/#", "https://user:secret@example.test"} {
		if _, err := Elements(Metadata{BaseURL: base, Canonical: "/page"}); err == nil {
			t.Fatal("unsafe base accepted", base)
		}
	}
	if _, err := Elements(Metadata{Canonical: "/page"}); err == nil {
		t.Fatal("relative URL accepted without opt-in base")
	}
	m := Metadata{BaseURL: "https://example.test", Canonical: "/page", Alternates: map[string]string{"en": "/en"}, OpenGraph: OpenGraph{Images: []Image{{URL: "/image.png"}}}, Twitter: Twitter{Images: []string{"/twitter.png"}}}
	if _, err := Elements(m); err != nil {
		t.Fatal(err)
	}
	if m.Canonical != "/page" || m.Alternates["en"] != "/en" || m.OpenGraph.Images[0].URL != "/image.png" || m.Twitter.Images[0] != "/twitter.png" {
		t.Fatal("URL resolution mutated caller input", m)
	}
}
