package ui

import (
	"io"
	"strings"
	"sync"
	"testing"
)

func TestConcurrentRendering(t *testing.T) {
	node := El("DIV", Attrs{"class": "shared"}, Text("reusable"))
	var workers sync.WaitGroup
	for i := 0; i < 10; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 20; j++ {
				if err := node.Render(io.Discard); err != nil {
					t.Error(err)
				}
			}
		}()
	}
	workers.Wait()
}

func TestEscapingAndIslands(t *testing.T) {
	text, err := HTML(Island("counter", map[string]string{"value": `</script><img src=x onerror="bad">`}, El("p", Attrs{"title": `"<&`}, Text("<script>&'"))))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(text, "<script>") || strings.Contains(text, "<img") || !strings.Contains(text, "&lt;script&gt;&amp;&#39;") || !strings.Contains(text, "data-nextgo-island=\"counter\"") {
		t.Fatal(text)
	}
	if _, err = HTML(Island("bad", make(chan int), Text("x"))); err == nil {
		t.Fatal("unsupported props should fail")
	}
}
func TestUnsafeHTMLRejected(t *testing.T) {
	for _, node := range []Node{
		El("script", nil, Text("bad")), El("style", nil), El("img", Attrs{"onerror": "bad"}), El("p", Attrs{"style": "bad"}), El("iframe", nil),
		El("a", Attrs{"href": "javascript:alert(1)"}), El("a", Attrs{"href": " JAVa\nscript:alert(1)"}), El("img", Attrs{"src": "data:text/html,bad"}), El("p", Attrs{"x\"": "bad"}),
	} {
		if _, err := HTML(node); err == nil {
			t.Fatal("unsafe HTML accepted")
		}
	}
	for _, href := range []string{"/posts?q=go&x=1", "https://go.dev", "#counter", "mailto:a@example.com"} {
		if _, err := HTML(El("a", Attrs{"href": href}, Text("safe"))); err != nil {
			t.Fatal(err)
		}
	}
}

// SVG animation can change an already validated href after parsing. Reject the
// indirection even when its initial URL looks safe.
func TestSVGAnimationURLIndirectionRejected(t *testing.T) {
	for _, tag := range []string{"animate", "set", "animateMotion", "animateTransform", "discard", "ANIMATE"} {
		for _, attr := range []string{"to", "values"} {
			node := El("svg", nil, El("a", Attrs{"href": "/safe"}, El(tag, Attrs{"attributeName": "href", attr: "javascript:alert(1)"})))
			if _, err := HTML(node); err == nil {
				t.Fatalf("accepted %s with %s", tag, attr)
			}
		}
	}
	if _, err := HTML(El("svg", nil, El("path", Attrs{"d": "M0 0L10 10"}))); err != nil {
		t.Fatal(err)
	}
}

func FuzzEscapedHTMLCannotCreateMarkup(f *testing.F) {
	f.Add(`<img src=x onerror="alert(1)">`, `"><svg onload=alert(1)>`)
	f.Add("ordinary text", "ordinary title")
	f.Add("\x00\xff<&", "\r\n\"'&")
	f.Fuzz(func(t *testing.T, text, title string) {
		markup, err := HTML(El("p", Attrs{"title": title}, Text(text)))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(markup, "<") != 2 || strings.Count(markup, ">") != 2 || strings.Count(markup, `"`) != 2 {
			t.Fatalf("untrusted data changed the HTML structure: %q", markup)
		}
	})
}

func TestResponsiveCDNAssetURLs(t *testing.T) {
	for _, value := range []string{"/assets/a.png 320w, /assets/b.png 640w", "https://cdn.example.com/release/assets/a.png 320w, https://cdn.example.com/release/assets/b.png 640w"} {
		if _, err := HTML(El("img", Attrs{"srcset": value})); err != nil {
			t.Fatalf("valid asset candidates: %v", err)
		}
	}
	for _, value := range []string{"javascript:alert(1) 320w", "data:image/png;base64,AAAA 320w", "//foreign/a.png 320w", "https://user:secret@cdn.example.com/a.png 320w", "https://cdn.example.com/a.png 0w", "https://cdn.example.com/a.png 1x", "https://cdn.example.com/a.png 320w, javascript:alert(1) 640w"} {
		if _, err := HTML(El("img", Attrs{"srcset": value})); err == nil {
			t.Errorf("accepted invalid candidates %q", value)
		}
	}
}
