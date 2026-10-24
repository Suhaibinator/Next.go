package assets

import (
	"crypto/sha256"
	"encoding/base64"
	"math"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
)

func pointer(v float64) *float64 { return &v }
func face() Font                 { return Font{Family: "Demo Sans", Source: "/assets/demo-v1.woff2", Format: "woff2"} }

func TestFontCSSAndHTTP(t *testing.T) {
	f := face()
	f.Weight = "100 900"
	f.Style = "italic"
	f.SizeAdjust = pointer(102.5)
	f.AscentOverride = pointer(90)
	f.DescentOverride = pointer(20)
	f.LineGapOverride = pointer(0)
	h, url, err := NewFontStyles([]Font{f})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("GET", url, nil))
	if r.Code != 200 || r.Header().Get("Content-Type") != "text/css; charset=utf-8" || r.Header().Get("X-Content-Type-Options") != "nosniff" || !strings.Contains(r.Header().Get("Cache-Control"), "immutable") {
		t.Fatal(r.Code, r.Header())
	}
	for _, want := range []string{`font-family:"Demo Sans"`, `src:url("/assets/demo-v1.woff2") format("woff2")`, `font-weight:100 900`, `font-style:italic`, `font-display:swap`, `size-adjust:102.5%`, `ascent-override:90%`, `descent-override:20%`, `line-gap-override:0%`} {
		if !strings.Contains(r.Body.String(), want) {
			t.Errorf("missing %s in %s", want, r.Body.String())
		}
	}
	_, sameURL, err := NewFontStyles([]Font{f})
	if err != nil || sameURL != url {
		t.Fatal("nondeterministic URL")
	}
	css := r.Body.String()
	etag := r.Header().Get("ETag")
	*f.SizeAdjust = 125
	r = httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("GET", url, nil))
	if r.Body.String() != css {
		t.Fatal("handler did not snapshot input")
	}
	_, changedURL, err := NewFontStyles([]Font{f})
	if err != nil || changedURL == url {
		t.Fatal("content did not affect URL")
	}
	for _, method := range []string{"HEAD", "POST"} {
		r := httptest.NewRecorder()
		h.ServeHTTP(r, httptest.NewRequest(method, url, nil))
		if method == "HEAD" {
			if r.Code != 200 || r.Body.Len() != 0 || r.Header().Get("Content-Length") != strconv.Itoa(len(css)) {
				t.Fatal(r.Code, r.Header())
			}
		} else if r.Code != 405 || r.Header().Get("Allow") != "GET, HEAD" {
			t.Fatal(r.Code, r.Header())
		}
	}
	for _, condition := range []string{etag, "W/" + etag, "*", `"unrelated", ` + etag} {
		r := httptest.NewRecorder()
		req := httptest.NewRequest("GET", url, nil)
		req.Header.Set("If-None-Match", condition)
		h.ServeHTTP(r, req)
		if r.Code != 304 || r.Body.Len() != 0 || r.Header().Get("ETag") != etag {
			t.Fatal(r.Code, r.Header(), r.Body.String())
		}
	}
	r = httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("GET", "/wrong.css", nil))
	if r.Code != 404 {
		t.Fatal("URL mismatch accepted")
	}
}

func TestFontCSSStringsCannotEscapeDeclaration(t *testing.T) {
	f := face()
	f.Family = `Demo ";}body{background:red}/* </style><script>bad</script>`
	f.Source = `/assets/face"quoted.woff2?v=one&two=three`
	h, url, err := NewFontStyles([]Font{f})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRecorder()
	h.ServeHTTP(r, httptest.NewRequest("GET", url, nil))
	css := r.Body.String()
	if strings.Contains(css, "</style>") || strings.Contains(css, "<script>") || strings.Contains(css, `Demo ";}`) || !strings.Contains(css, `\22 `) || !strings.Contains(css, `\3c `) {
		t.Fatal(css)
	}
	// The sole unescaped double quotes delimit family, URL and format strings.
	if strings.Count(css, `"`) != 6 {
		t.Fatal("unescaped CSS quote", css)
	}
}

func TestFontValidation(t *testing.T) {
	if _, _, err := NewFontStyles(nil); err == nil {
		t.Fatal("no faces accepted")
	}
	if _, _, err := NewFontStyles([]Font{face(), face()}); err == nil {
		t.Fatal("duplicate face accepted")
	}
	tests := map[string]func(*Font){
		"familyempty": func(f *Font) { f.Family = " " }, "familycontrol": func(f *Font) { f.Family = "a\nfont-weight:bold" }, "familylong": func(f *Font) { f.Family = strings.Repeat("x", 257) },
		"source-remote": func(f *Font) { f.Source = "https://example.com/a.woff2" }, "source-protocolrelative": func(f *Font) { f.Source = "//example.com/a.woff2" }, "source-js": func(f *Font) { f.Source = "javascript:alert(1)" }, "source-dot": func(f *Font) { f.Source = "/fonts/../a.woff2" }, "source-encoded-dot": func(f *Font) { f.Source = "/fonts/%2e%2e/a.woff2" }, "source-backslash": func(f *Font) { f.Source = "/\\evil/a.woff2" }, "source-encodedbackslash": func(f *Font) { f.Source = "/%5cevil/a.woff2" }, "source-control": func(f *Font) { f.Source = "/a\n.woff2" }, "source-fragment": func(f *Font) { f.Source = "/a.woff2#x" }, "source-encodedcontrol": func(f *Font) { f.Source = "/a%0a.woff2" }, "source-space": func(f *Font) { f.Source = "/a b.woff2" },
		"format": func(f *Font) { f.Format = `woff2");color:red` }, "weight-low": func(f *Font) { f.Weight = "0" }, "weight-high": func(f *Font) { f.Weight = "1001" }, "weight-range": func(f *Font) { f.Weight = "900 100" }, "weight-count": func(f *Font) { f.Weight = "100 200 300" }, "weight-inject": func(f *Font) { f.Weight = "400;color:red" }, "weight-exponent": func(f *Font) { f.Weight = "1e2" }, "weight-signed": func(f *Font) { f.Weight = "+400" }, "style": func(f *Font) { f.Style = "italic;color:red" }, "display": func(f *Font) { f.Display = "swap;color:red" },
		"size-zero": func(f *Font) { f.SizeAdjust = pointer(0) }, "size-negative": func(f *Font) { f.SizeAdjust = pointer(-1) }, "size-high": func(f *Font) { f.SizeAdjust = pointer(1001) }, "size-nan": func(f *Font) { f.SizeAdjust = pointer(math.NaN()) }, "ascent-inf": func(f *Font) { f.AscentOverride = pointer(math.Inf(1)) }, "descent-negative": func(f *Font) { f.DescentOverride = pointer(-1) }, "gap-high": func(f *Font) { f.LineGapOverride = pointer(1001) },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			f := face()
			change(&f)
			if _, _, err := NewFontStyles([]Font{f}); err == nil {
				t.Fatal("invalid font accepted")
			}
			if _, err := f.Preload().Elements(); err == nil {
				t.Fatal("invalid font preload accepted")
			}
		})
	}
}

func TestLinks(t *testing.T) {
	font := face()
	items, err := font.Preload().Elements()
	if err != nil {
		t.Fatal(err)
	}
	attrs := items[0].Attrs
	if len(items) != 1 || items[0].Tag != "link" || attrs["href"] != font.Source || attrs["as"] != "font" || attrs["type"] != "font/woff2" || attrs["crossorigin"] != "anonymous" {
		t.Fatal(items)
	}
	attrs["href"] = "mutated"
	again, _ := font.Preload().Elements()
	if again[0].Attrs["href"] == "mutated" {
		t.Fatal("aliased head attributes")
	}
	digest := sha256.Sum256([]byte("body{}"))
	integrity := "sha256-" + base64.StdEncoding.EncodeToString(digest[:])
	items, err = Stylesheet("/assets/app.css?v=1", integrity).Elements()
	if err != nil {
		t.Fatal(err)
	}
	if items[0].Attrs["rel"] != "stylesheet" || items[0].Attrs["integrity"] != integrity || items[0].Attrs["crossorigin"] != "anonymous" {
		t.Fatal(items)
	}
	for _, resource := range []Resource{Stylesheet("https://example.com/a.css"), Stylesheet("//evil/a.css"), Stylesheet("/a.css", "sha256-bad"), Stylesheet("/a.css", "md5-aaaaaaaa"), Stylesheet("/a.css", "", "")} {
		if _, err := resource.Elements(); err == nil {
			t.Fatal("invalid resource accepted")
		}
	}
	items, err = Stylesheet("/app.css").Elements()
	if err != nil || items[0].Attrs["crossorigin"] != "" {
		t.Fatal(items, err)
	}
}
