package content

import (
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/Suhaibinator/Next.go/ui"
)

func render(t *testing.T, source string, limits Limits) string {
	t.Helper()
	node, err := Parse(source, limits)
	if err != nil {
		t.Fatal(err)
	}
	html, err := ui.HTML(node)
	if err != nil {
		t.Fatal(err)
	}
	return html
}

func TestSyntaxSubset(t *testing.T) {
	for _, test := range []struct{ name, source, want string }{
		{"paragraph", "First line\nsecond line\n\nNext.", "<p>First line second line</p><p>Next.</p>"},
		{"heading", "# One\n###### Six\n####### literal", "<h1>One</h1><h6>Six</h6><p>####### literal</p>"},
		{"unordered", "- first\n+ second\n* third", "<ul><li>first</li><li>second</li><li>third</li></ul>"},
		{"ordered", "3. three\n9. next\n\n0. zero", `<ol start="3"><li>three</li><li>next</li></ol><ol start="0"><li>zero</li></ol>`},
		{"quote", "> Outer\n>> Inner\n>\n> End", "<blockquote><p>Outer</p><blockquote><p>Inner</p></blockquote><p>End</p></blockquote>"},
		{"fence", "```go\nif a < b {\n  print(\"x\")\n}\n```", "<pre><code class=\"language-go\">if a &lt; b {\n  print(&#34;x&#34;)\n}</code></pre>"},
		{"inline", "**strong *soft* text** and `*code*`", "<p><strong>strong <em>soft</em> text</strong> and <code>*code*</code></p>"},
		{"link", "[**Read**](/docs?q=a&b=2#part)", `<p><a href="/docs?q=a&amp;b=2#part"><strong>Read</strong></a></p>`},
		{"empty", "\n \n", ""},
		{"crlf", "# A\r\n\r\nB", "<h1>A</h1><p>B</p>"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := render(t, test.source, Limits{}); got != test.want {
				t.Fatalf("got %q; want %q", got, test.want)
			}
		})
	}
}

func TestUntrustedHTMLAndUnsupportedSyntaxStayText(t *testing.T) {
	for _, source := range []string{
		`<script>alert("x")</script>`, `<img src=x onerror=alert(1)>`,
		`<a href="javascript:alert(1)">bad</a>`, `&lt;script&gt;`,
		`![image](javascript:bad)`, `{{component}}`, `import secrets from "native"`,
		"[broken", "*unclosed [link](/ignored)", "`unclosed", "[label](missing",
		"    indented code", "---", "a | b",
	} {
		got := render(t, source, Limits{})
		escaped, err := ui.HTML(ui.El("p", nil, ui.Text(source)))
		if err != nil || got != escaped {
			t.Fatalf("unsupported syntax became active: source=%q got=%q expected=%q error=%v", source, got, escaped, err)
		}
	}
	if got := render(t, "`<script>&amp;`", Limits{}); got != "<p><code>&lt;script&gt;&amp;amp;</code></p>" {
		t.Fatal(got)
	}
}

func TestRecognizedLinksRejectUnsafeOrAmbiguousDestinationsBeforeRendering(t *testing.T) {
	for _, target := range []string{
		"javascript:alert", "JaVaScRiPt:alert", "data:text/html,script", "vbscript:run", "file:///etc/passwd",
		"//outside.test/a", "https://user:password@example.test/", "https:opaque", "mailto:", "tel:",
		"/\\outside.test", "https://example.test/a b", "java\tscript:bad", "https://example.test/\u00a0",
		"", "%ZZ", "https://", strings.Repeat("x", 2049),
	} {
		node, err := Parse("[public]("+target+")", Limits{})
		if !errors.Is(err, ErrInvalid) || node != nil {
			t.Errorf("target %q accepted: node=%v error=%v", target, node, err)
		}
	}
	for _, target := range []string{"/docs", "../notes", "notes?q=a&b=2", "#part", "https://example.test/a", "http://example.test/", "mailto:hello@example.test", "tel:+12345"} {
		got := render(t, "[public]("+target+")", Limits{})
		if !strings.HasPrefix(got, "<p><a href=") {
			t.Errorf("safe link %q not rendered: %s", target, got)
		}
	}
}

func TestInputLimitsAndFailureClasses(t *testing.T) {
	for _, test := range []struct {
		name, source string
		limits       Limits
		want         error
	}{
		{"bytes", "abcd", Limits{MaxBytes: 3}, ErrLimit},
		{"line-bytes", "abcd", Limits{MaxLineBytes: 3}, ErrLimit},
		{"lines", "a\nb", Limits{MaxLines: 1}, ErrLimit},
		{"trailing-line", "a\n", Limits{MaxLines: 1}, ErrLimit},
		{"nodes", "a", Limits{MaxNodes: 2}, ErrLimit},
		{"quote-depth", ">> deep", Limits{MaxDepth: 1}, ErrLimit},
		{"inline-depth", "**a *b* c**", Limits{MaxDepth: 1}, ErrLimit},
		{"UTF8", string([]byte{0xff}), Limits{}, ErrInvalid},
		{"NUL", "a\x00b", Limits{}, ErrInvalid},
		{"bare-CR", "a\rb", Limits{}, ErrInvalid},
		{"escape-control", "a\x1bb", Limits{}, ErrInvalid},
		{"unclosed-fence", "```go\nx", Limits{}, ErrInvalid},
		{"fence-info", "```go onclick=x\nx\n```", Limits{}, ErrInvalid},
		{"negative-config", "a", Limits{MaxNodes: -1}, ErrInvalid},
		{"hard-bytes", "a", Limits{MaxBytes: (1 << 20) + 1}, ErrInvalid},
		{"hard-lines", "a", Limits{MaxLines: 16385}, ErrInvalid},
		{"hard-line-bytes", "a", Limits{MaxLineBytes: (64 << 10) + 1}, ErrInvalid},
		{"hard-nodes", "a", Limits{MaxNodes: 65537}, ErrInvalid},
		{"hard-depth", "a", Limits{MaxDepth: 33}, ErrInvalid},
	} {
		t.Run(test.name, func(t *testing.T) {
			node, err := Parse(test.source, test.limits)
			if node != nil || !errors.Is(err, test.want) {
				t.Fatalf("partial output or wrong failure: %v %v", node, err)
			}
		})
	}
	if got := render(t, "abc", Limits{MaxBytes: 3, MaxLineBytes: 3, MaxLines: 1, MaxNodes: 3, MaxDepth: 1}); got != "<p>abc</p>" {
		t.Fatal(got)
	}
}

func TestParsedResultIsDeterministicAndSafeForConcurrentRendering(t *testing.T) {
	node, err := Parse("public text", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for range 8 {
				got, err := ui.HTML(node)
				if err != nil || got != "<p>public text</p>" {
					t.Errorf("result retained mutable render state: %q %v", got, err)
				}
			}
		}()
	}
	workers.Wait()
}

func FuzzParseBoundedEscapedDeterministic(f *testing.F) {
	for _, seed := range []string{"# title\n\n**hello** [link](/docs)", "<script>bad</script>", "[x](javascript:bad)", ">> quote", "```go\n<raw>\n```", strings.Repeat("[", 1000)} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, source string) {
		node, err := Parse(source, Limits{MaxBytes: 4096, MaxLines: 128, MaxLineBytes: 1024, MaxNodes: 512, MaxDepth: 8})
		if err != nil {
			if node != nil || !errors.Is(err, ErrInvalid) && !errors.Is(err, ErrLimit) {
				t.Fatalf("unclassified error or partial node: %v %v", node, err)
			}
			return
		}
		first, err := ui.HTML(node)
		if err != nil {
			t.Fatal("parser accepted serializer-invalid output", err)
		}
		second, err := ui.HTML(node)
		if err != nil || first != second || strings.Contains(first, "<script") || strings.Contains(first, "<iframe") || strings.Contains(first, "<style") {
			t.Fatalf("active raw markup or unstable output: %q %q %v", first, second, err)
		}
	})
}
