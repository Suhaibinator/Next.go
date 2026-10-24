//go:build !js

package scripts

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/ui"
	"html"
	"io"
	"strings"
)

type declaration struct{ script Script }

// Node declares a route/layout dependency. It is inert SSR markup. Early
// declarations belong in Config.EarlyScripts, not a page or deferred boundary.
func Node(script Script) ui.Node { return declaration{script} }
func (n declaration) Render(w io.Writer) error {
	s, err := n.script.Check()
	if err != nil {
		return err
	}
	if s.Strategy == BeforeInteractive {
		return fmt.Errorf("scripts: early declarations belong in Config.EarlyScripts")
	}
	raw, _ := json.Marshal(s)
	return ui.El("nextgo-script", ui.Attrs{"hidden": "", "data-nextgo-script": string(raw)}).Render(w)
}

type nonceKey struct{}

// WithNonce binds an application-generated request nonce. Middleware must also
// send the matching CSP header. Nonce responses bypass framework public caches.
func WithNonce(ctx context.Context, nonce string) (context.Context, error) {
	if ctx == nil || len(nonce) < 22 || len(nonce) > 128 {
		return nil, fmt.Errorf("scripts: nonce requires 22..128 base64 characters")
	}
	base := strings.TrimRight(nonce, "=")
	if len(nonce)-len(base) > 2 || strings.Contains(base, "=") {
		return nil, fmt.Errorf("scripts: invalid nonce padding")
	}
	for _, r := range nonce {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '+' || r == '/' || r == '-' || r == '_' || r == '=') {
			return nil, fmt.Errorf("scripts: invalid nonce")
		}
	}
	return context.WithValue(cache.WithBypass(ctx), nonceKey{}, nonce), nil
}
func Nonce(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	nonce, _ := ctx.Value(nonceKey{}).(string)
	return nonce
}
func NewNonce() (string, error) {
	var data [24]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", err
	}
	return base64.RawStdEncoding.EncodeToString(data[:]), nil
}

// EarlyHTML is the validated renderer used by the document shell. These
// classic scripts execute in parser order before the Go bootstrap; they can
// delay parsing. It never renders an inline body or an inline event handler.
func EarlyHTML(declarations []Script, resolve func(string) (string, error), nonce string) (string, error) {
	var markup string
	for _, script := range declarations {
		s, err := script.Check()
		if err != nil {
			return "", err
		}
		if s.Strategy != BeforeInteractive {
			return "", fmt.Errorf("scripts: early declarations require beforeInteractive")
		}
		if s.Asset {
			if resolve == nil {
				return "", fmt.Errorf("scripts: asset resolver is required")
			}
			src, err := resolve(s.Src)
			if err != nil {
				return "", err
			}
			s.Src = src
			s.Asset = false
		}
		s, err = s.Check()
		if err != nil {
			return "", err
		}
		src := s.Src
		raw, _ := json.Marshal(s)
		markup += `<script data-nextgo-early="` + html.EscapeString(string(raw)) + `" src="` + html.EscapeString(src) + `"`
		for _, attr := range [][2]string{{"nonce", nonce}, {"integrity", s.Integrity}, {"crossorigin", s.CrossOrigin}, {"referrerpolicy", s.ReferrerPolicy}} {
			if attr[1] != "" {
				markup += ` ` + attr[0] + `="` + html.EscapeString(attr[1]) + `"`
			}
		}
		markup += `></script>`
	}
	return markup, nil
}
