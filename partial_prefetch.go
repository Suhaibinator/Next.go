package nextgo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

type partialResponse struct {
	token      string
	prefetch   bool
	reuseUntil time.Time
}

func shellProtocol(r *http.Request) bool {
	values := r.Header.Values(navigation.ShellProtocolHeader)
	return len(values) == 1 && values[0] == fmt.Sprint(navigation.ShellVersion)
}

// Bind content as well as generation and runtime ownership. A reference is an
// optimization proposal, never permission to omit request authorization.
func (a *App) partialShellID(route Route, logical string, value cachedPage) (string, error) {
	if err := validatePageShell(value, route); err != nil {
		return "", err
	}
	if value.Page.document == nil || value.Page.preparedHead == nil {
		return "", fmt.Errorf("partial shell lacks frozen metadata")
	}
	input := struct {
		Domain, Build, Configuration, Pattern, Path, HTML, Title, Description string
		Meta                                                                  map[string]string
		Document                                                              Document
		Head                                                                  []seo.Element
		Regions                                                               []ui.RegionSpec
		GeneratedAt                                                           time.Time
	}{"nextgo.partial-shell.v1", a.config.BuildID, a.configurationID, route.Pattern, logical, value.HTML, value.Page.Title, value.Page.Description, value.Page.Meta, *value.Page.document, value.Page.preparedHead.elements, value.Regions, value.GeneratedAt.UTC()}
	digest := sha256.New()
	if err := json.NewEncoder(digest).Encode(input); err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func shellReuseFor(info cache.Info, elapsed time.Duration) time.Duration {
	reuse := min(30*time.Second, info.FreshFor)
	if !info.NoExpiry {
		reuse = min(reuse, info.ExpiresIn)
	}
	return max(0, reuse-elapsed)
}

// Fetch deliberately hides Set-Cookie from browser code. Enforce this policy
// before writing the response rather than relying solely on the client parser.
func shellRetentionMillis(header http.Header, until time.Time) int64 {
	if len(header.Values("Set-Cookie")) > 0 {
		return 0
	}
	for _, vary := range header.Values("Vary") {
		for _, field := range strings.Split(vary, ",") {
			if strings.TrimSpace(field) == "*" {
				return 0
			}
		}
	}
	return max(0, min(int64(navigation.MaxShellStaleMillis), time.Until(until).Milliseconds()))
}

func partialEnvelopeFields(r *http.Request, page Page, status int, stream bool) (string, bool) {
	if status != http.StatusOK || page.partial == nil || r.Header.Get("X-Nextgo-Navigate") != "1" || !shellProtocol(r) || !navigation.ValidShellID(page.partial.token) {
		return "", false
	}
	if !stream {
		if !page.partial.prefetch {
			return "", false
		}
		return page.partial.token, false
	}
	if page.partial.prefetch {
		return "", false
	}
	if r.Header.Get(navigation.StreamHeader) != fmt.Sprint(navigation.StreamVersion) {
		return "", false
	}
	values := r.Header.Values(navigation.ShellHeader)
	ref := len(values) == 1 && values[0] == page.partial.token
	return page.partial.token, ref
}
