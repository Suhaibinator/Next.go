// Package assets creates typed links and self-hosted font stylesheets. It does
// not download fonts, subset font files, or execute third-party JavaScript.
package assets

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode"

	"github.com/Suhaibinator/Next.go/seo"
)

// Resource returns validated head elements. Consumers must escape attributes
// when rendering and should validate every resource before writing a response.
type Resource interface{ Elements() ([]seo.Element, error) }

type stylesheet struct {
	href      string
	integrity []string
}

// Stylesheet creates a local stylesheet link. The optional integrity value is
// a space-separated set of sha256, sha384, or sha512 SRI digests.
func Stylesheet(href string, integrity ...string) Resource {
	return stylesheet{href: href, integrity: append([]string(nil), integrity...)}
}

func (s stylesheet) Elements() ([]seo.Element, error) {
	if err := localURL(s.href); err != nil {
		return nil, err
	}
	if len(s.integrity) > 1 {
		return nil, fmt.Errorf("assets: at most one integrity value is accepted")
	}
	attrs := map[string]string{"rel": "stylesheet", "href": s.href}
	if len(s.integrity) == 1 && s.integrity[0] != "" {
		if err := validIntegrity(s.integrity[0]); err != nil {
			return nil, err
		}
		attrs["integrity"] = s.integrity[0]
		attrs["crossorigin"] = "anonymous"
	}
	return []seo.Element{{Tag: "link", Attrs: attrs}}, nil
}

// Font describes a single local @font-face. Source must point to an application
// asset containing licensed WOFF/WOFF2 bytes. Weight accepts 1..1000 or a variable
// range such as "100 900"; it defaults to 400. Style defaults to normal and
// Display defaults to swap. Metric overrides are percentages, not raw CSS.
type Font struct {
	Family, Source, Format, Weight, Style, Display               string
	SizeAdjust, AscentOverride, DescentOverride, LineGapOverride *float64
}

type fontPreload struct{ font Font }

// Preload requests the local font early with the correct MIME and CORS mode.
// Preloading every face can waste bandwidth; select only a critical face.
func (f Font) Preload() Resource { return fontPreload{font: f} }

func (p fontPreload) Elements() ([]seo.Element, error) {
	f, err := normalize(p.font)
	if err != nil {
		return nil, err
	}
	return []seo.Element{{Tag: "link", Attrs: map[string]string{"rel": "preload", "href": f.Source, "as": "font", "type": "font/" + f.Format, "crossorigin": "anonymous"}}}, nil
}

// NewFontStyles validates and snapshots faces into an immutable CSS handler.
// The returned URL is content-addressed; mount the handler at this exact path.
// Changing font bytes requires changing the Source URL to preserve immutable
// caching semantics (typically an application-generated content hash).
func NewFontStyles(fonts []Font) (http.Handler, string, error) {
	if len(fonts) == 0 || len(fonts) > 128 {
		return nil, "", fmt.Errorf("assets: require between 1 and 128 font faces")
	}
	var css strings.Builder
	seen := map[string]bool{}
	for _, raw := range fonts {
		f, err := normalize(raw)
		if err != nil {
			return nil, "", err
		}
		key := strings.ToLower(f.Family) + "\x00" + f.Weight + "\x00" + f.Style
		if seen[key] {
			return nil, "", fmt.Errorf("assets: duplicate font face %q", f.Family)
		}
		seen[key] = true
		css.WriteString("@font-face{font-family:")
		css.WriteString(cssString(f.Family))
		css.WriteString(";src:url(")
		css.WriteString(cssString(f.Source))
		css.WriteString(") format(")
		css.WriteString(cssString(f.Format))
		css.WriteString(");font-weight:")
		css.WriteString(f.Weight)
		css.WriteString(";font-style:")
		css.WriteString(f.Style)
		css.WriteString(";font-display:")
		css.WriteString(f.Display)
		for _, m := range []struct {
			name  string
			value *float64
		}{{"size-adjust", f.SizeAdjust}, {"ascent-override", f.AscentOverride}, {"descent-override", f.DescentOverride}, {"line-gap-override", f.LineGapOverride}} {
			if m.value != nil {
				css.WriteByte(';')
				css.WriteString(m.name)
				css.WriteByte(':')
				css.WriteString(strconv.FormatFloat(*m.value, 'f', -1, 64))
				css.WriteByte('%')
			}
		}
		css.WriteString("}\n")
	}
	data := []byte(css.String())
	digest := sha256.Sum256(data)
	hash := hex.EncodeToString(digest[:])
	urlPath := "/_nextgo/fonts/" + hash + ".css"
	etag := `"` + hash + `"`
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != urlPath {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/css; charset=utf-8")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		w.Header().Set("ETag", etag)
		for _, tag := range strings.Split(r.Header.Get("If-None-Match"), ",") {
			tag = strings.TrimSpace(tag)
			if tag == "*" || strings.TrimPrefix(tag, "W/") == etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		if r.Method != http.MethodHead {
			_, _ = w.Write(data)
		}
	})
	return handler, urlPath, nil
}

func normalize(f Font) (Font, error) {
	f.Family = strings.TrimSpace(f.Family)
	if f.Family == "" || len(f.Family) > 256 || strings.IndexFunc(f.Family, unicode.IsControl) >= 0 {
		return Font{}, fmt.Errorf("assets: invalid font family")
	}
	if err := localURL(f.Source); err != nil {
		return Font{}, err
	}
	if f.Format != "woff" && f.Format != "woff2" {
		return Font{}, fmt.Errorf("assets: font format must be woff or woff2")
	}
	if f.Weight == "" || f.Weight == "normal" {
		f.Weight = "400"
	}
	if f.Weight == "bold" {
		f.Weight = "700"
	}
	weights := strings.Fields(f.Weight)
	if len(weights) < 1 || len(weights) > 2 {
		return Font{}, fmt.Errorf("assets: invalid font weight")
	}
	previous := 0
	for _, s := range weights {
		weight, err := strconv.Atoi(s)
		if err != nil || weight < 1 || weight > 1000 || weight < previous || strconv.Itoa(weight) != s {
			return Font{}, fmt.Errorf("assets: invalid font weight")
		}
		previous = weight
	}
	f.Weight = strings.Join(weights, " ")
	if f.Style == "" {
		f.Style = "normal"
	}
	switch f.Style {
	case "normal", "italic", "oblique":
	default:
		return Font{}, fmt.Errorf("assets: invalid font style")
	}
	if f.Display == "" {
		f.Display = "swap"
	}
	switch f.Display {
	case "auto", "block", "swap", "fallback", "optional":
	default:
		return Font{}, fmt.Errorf("assets: invalid font display")
	}
	for _, m := range []struct {
		name     string
		value    *float64
		positive bool
	}{{"size-adjust", f.SizeAdjust, true}, {"ascent-override", f.AscentOverride, false}, {"descent-override", f.DescentOverride, false}, {"line-gap-override", f.LineGapOverride, false}} {
		if m.value != nil {
			v := *m.value
			if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 || v > 1000 || (m.positive && v == 0) {
				return Font{}, fmt.Errorf("assets: invalid %s percentage", m.name)
			}
		}
	}
	return f, nil
}

func localURL(value string) error {
	if value == "" || len(value) > 4096 || !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || strings.IndexFunc(value, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) >= 0 || strings.Contains(value, "\\") {
		return fmt.Errorf("assets: expected local root-relative URL")
	}
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "" || u.Host != "" || u.User != nil || u.Fragment != "" || u.Path == "/" || path.Clean(u.Path) != u.Path || strings.HasPrefix(u.Path, "//") || strings.Contains(u.Path, "\\") || strings.IndexFunc(u.Path, unicode.IsControl) >= 0 {
		return fmt.Errorf("assets: invalid local URL %q", value)
	}
	return nil
}

func cssString(value string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range value {
		if r == '"' || r == '\\' || r == '<' || r == '>' {
			fmt.Fprintf(&b, "\\%x ", r)
		} else {
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

func validIntegrity(value string) error {
	if strings.IndexFunc(value, unicode.IsControl) >= 0 {
		return fmt.Errorf("assets: invalid integrity digest")
	}
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return fmt.Errorf("assets: invalid integrity digest")
	}
	for _, field := range fields {
		algorithm, encoded, ok := strings.Cut(field, "-")
		size := map[string]int{"sha256": 32, "sha384": 48, "sha512": 64}[algorithm]
		decoded, err := base64.StdEncoding.Strict().DecodeString(encoded)
		if !ok || size == 0 || err != nil || len(decoded) != size {
			return fmt.Errorf("assets: invalid integrity digest")
		}
	}
	return nil
}
