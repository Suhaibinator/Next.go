//go:build !js

package linkhint

import (
	"github.com/Suhaibinator/Next.go/ui"
	"strings"
	"testing"
)

func TestOrdinaryLinkHintsRenderWithoutInlineBehavior(t *testing.T) {
	html, err := ui.HTML(View(Props{Label: "A & B", Links: []Link{{Label: "First <page>", Href: "/link-status/one?x=1&y=2"}}}))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`href="/link-status/one?x=1&amp;y=2"`, `data-nextgo-prefetch="false"`, `First &lt;page&gt;`, `class="link-hint"`, `aria-hidden="true"`} {
		if !strings.Contains(string(html), want) {
			t.Fatal("missing ordinary escaped markup", want, string(html))
		}
	}
	if strings.Contains(string(html), "<script") || strings.Contains(string(html), "style=") {
		t.Fatal("inline behavior/style appeared", string(html))
	}
}
