// Package scripts declares explicitly trusted browser dependencies. It never
// accepts inline JavaScript and does not fetch scripts on the server.
package scripts

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/Suhaibinator/Next.go/internal/wirejson"
	"net/url"
	"strings"
	"unicode"
)

const NonceHeader = "X-Nextgo-Nonce"

type Strategy string

const (
	BeforeInteractive Strategy = "beforeInteractive"
	AfterInteractive  Strategy = "afterInteractive"
	LazyOnload        Strategy = "lazyOnload"
)

type Script struct {
	// Asset resolves Src beneath the framework public-asset mount. Otherwise Src is a browser URL.
	Asset          bool     `json:"asset,omitempty"`
	ID             string   `json:"id"`
	Src            string   `json:"src"`
	Strategy       Strategy `json:"strategy,omitempty"`
	Type           string   `json:"type,omitempty"`
	Integrity      string   `json:"integrity,omitempty"`
	CrossOrigin    string   `json:"crossOrigin,omitempty"`
	ReferrerPolicy string   `json:"referrerPolicy,omitempty"`
}

func (s Script) Check() (Script, error) {
	if s.ID == "" || len(s.ID) > 64 {
		return s, fmt.Errorf("scripts: require a 1..64 byte ID")
	}
	for _, r := range s.ID {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' || r == '.') {
			return s, fmt.Errorf("scripts: invalid ID")
		}
	}
	if s.Strategy == "" {
		s.Strategy = AfterInteractive
	}
	if s.Strategy != BeforeInteractive && s.Strategy != AfterInteractive && s.Strategy != LazyOnload {
		return s, fmt.Errorf("scripts: unsupported loading strategy")
	}
	if len(s.Src) == 0 || len(s.Src) > 4096 || strings.TrimSpace(s.Src) != s.Src || strings.HasPrefix(s.Src, "//") || strings.Contains(s.Src, "\\") {
		return s, fmt.Errorf("scripts: require an absolute HTTP(S) or root-relative source")
	}
	for _, r := range s.Src {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return s, fmt.Errorf("scripts: invalid source whitespace")
		}
	}
	u, err := url.Parse(s.Src)
	if err != nil || u.User != nil || u.Fragment != "" || strings.Contains(s.Src, "#") {
		return s, fmt.Errorf("scripts: invalid source URL")
	}
	if s.Asset && (u.Scheme != "" || !strings.HasPrefix(s.Src, "/")) {
		return s, fmt.Errorf("scripts: asset sources must be root-relative asset paths")
	}
	if u.Scheme != "" {
		if (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" {
			return s, fmt.Errorf("scripts: require HTTP(S)")
		}
	} else if !strings.HasPrefix(s.Src, "/") || u.Host != "" {
		return s, fmt.Errorf("scripts: require a root-relative source")
	}
	if s.Type != "" && s.Type != "module" {
		return s, fmt.Errorf("scripts: type must be empty or module")
	}
	if s.Strategy == BeforeInteractive && s.Type == "module" {
		return s, fmt.Errorf("scripts: early module scripts are not supported")
	}
	if s.CrossOrigin != "" && s.CrossOrigin != "anonymous" && s.CrossOrigin != "use-credentials" {
		return s, fmt.Errorf("scripts: invalid crossorigin policy")
	}
	if s.Integrity != "" {
		if len(s.Integrity) > 512 {
			return s, fmt.Errorf("scripts: integrity exceeds limit")
		}
		parts := strings.Fields(s.Integrity)
		if len(parts) > 4 || len(parts) == 0 {
			return s, fmt.Errorf("scripts: invalid integrity")
		}
		for _, part := range parts {
			algorithm, digest, found := strings.Cut(part, "-")
			size := map[string]int{"sha256": 32, "sha384": 48, "sha512": 64}[algorithm]
			data, err := base64.StdEncoding.DecodeString(digest)
			if !found || size == 0 || err != nil || len(data) != size {
				return s, fmt.Errorf("scripts: invalid integrity digest")
			}
		}
		s.Integrity = strings.Join(parts, " ")
		if s.CrossOrigin == "" {
			s.CrossOrigin = "anonymous"
		}
	}
	switch s.ReferrerPolicy {
	case "", "no-referrer", "no-referrer-when-downgrade", "origin", "origin-when-cross-origin", "same-origin", "strict-origin", "strict-origin-when-cross-origin", "unsafe-url":
	default:
		return s, fmt.Errorf("scripts: invalid referrer policy")
	}
	return s, nil
}

func Parse(raw string) (Script, error) {
	var s Script
	if len(raw) > 8192 {
		return s, fmt.Errorf("scripts: declaration exceeds 8 KiB")
	}
	if err := wirejson.Unique([]byte(raw)); err != nil {
		return s, fmt.Errorf("scripts: invalid declaration: %w", err)
	}
	if err := wirejson.Strict([]byte(raw), &s); err != nil {
		return s, fmt.Errorf("scripts: invalid declaration: %w", err)
	}
	return s.Check()
}

// Key excludes identity aliases and timing. One document loads a dependency
// once; callers with another ID or a more eager strategy share that result.
func (s Script) Key() string {
	s.ID = ""
	s.Strategy = ""
	data, _ := json.Marshal(s)
	return string(data)
}
