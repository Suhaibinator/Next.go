package ui

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestLinkModesAndNativeAttributes(t *testing.T) {
	for _, test := range []struct {
		mode PrefetchMode
		want string
	}{
		{"", "viewport"}, {PrefetchAuto, "viewport"},
		{PrefetchIntent, "intent"}, {PrefetchNone, "false"},
	} {
		html, err := HTML(Link("/posts?q=go&name=<name>", LinkOptions{
			Prefetch: test.mode, Replace: true,
			Attrs: Attrs{"target": "_blank", "download": "post.html", "aria-label": `Read "Go"`},
		}, Text("Read <post>")))
		if err != nil {
			t.Fatal(err)
		}
		for _, want := range []string{
			`data-nextgo-prefetch="` + test.want + `"`,
			`href="/posts?q=go&amp;name=&lt;name&gt;"`,
			`data-nextgo-replace="true"`, `download="post.html"`,
			`target="_blank"`, `aria-label="Read &#34;Go&#34;"`,
			`>Read &lt;post&gt;</a>`,
		} {
			if !strings.Contains(html, want) {
				t.Errorf("mode %q: missing %q in %s", test.mode, want, html)
			}
		}
	}
	plain, err := HTML(El("a", Attrs{"href": "/posts"}, Text("Posts")))
	if err != nil || strings.Contains(plain, "prefetch") {
		t.Fatalf("plain anchor changed: %s, %v", plain, err)
	}
}

func TestLinkRejectsAmbiguousControlsAndUnsafeMarkup(t *testing.T) {
	for _, key := range []string{"href", "HREF", "data-nextgo-prefetch", "DATA-NEXTGO-PREFETCH", "data-nextgo-replace", "Data-Nextgo-Replace"} {
		if _, err := HTML(Link("/posts", LinkOptions{Attrs: Attrs{key: "other"}})); err == nil {
			t.Errorf("accepted ambiguous control %s", key)
		}
	}
	for _, node := range []Node{
		Link("javascript:alert(1)", LinkOptions{}),
		Link("/posts", LinkOptions{Prefetch: "unknown"}),
		Link("/posts", LinkOptions{Attrs: Attrs{"onclick": "bad()"}}),
		Link("/posts", LinkOptions{Attrs: Attrs{"style": "display:none"}}),
	} {
		if _, err := HTML(node); err == nil {
			t.Errorf("accepted invalid link")
		}
	}
	for _, target := range []string{"https://go.dev", "mailto:a@example.com", "#section"} {
		if _, err := HTML(Link(target, LinkOptions{})); err != nil {
			t.Errorf("ordinary native destination %q: %v", target, err)
		}
	}
}

func TestLinkOwnsCallerAttributesAndChildren(t *testing.T) {
	attrs := Attrs{"id": "original"}
	children := []Node{Text("original")}
	node := Link("/posts", LinkOptions{Attrs: attrs}, children...)
	attrs["id"] = "changed"
	attrs["onclick"] = "bad()"
	children[0] = Text("changed")
	html, err := HTML(node)
	if err != nil || strings.Contains(html, "changed") || !strings.Contains(html, `id="original"`) || !strings.Contains(html, ">original</a>") {
		t.Fatalf("link retained mutable caller inputs: %s, %v", html, err)
	}
	if _, changed := attrs["href"]; changed {
		t.Fatal("Link mutated caller attributes")
	}
}

func TestLinkPropagatesWriterFailure(t *testing.T) {
	want := errors.New("write failed")
	if err := Link("/posts", LinkOptions{}).Render(linkFailedWriter{want}); !errors.Is(err, want) {
		t.Fatalf("writer failure lost: %v", err)
	}
}

type linkFailedWriter struct{ err error }

func (w linkFailedWriter) Write([]byte) (int, error) { return 0, w.err }

var _ io.Writer = linkFailedWriter{}
