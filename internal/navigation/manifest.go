// Package navigation contains the public route-kind manifest shared by the
// server and browser. It contains patterns only, never loader data or imports.
package navigation

import (
	"github.com/Suhaibinator/Next.go/internal/paths"
	"github.com/Suhaibinator/Next.go/internal/routing"
	"strings"
)

type Route struct {
	Pattern  string `json:"pattern"`
	Endpoint bool   `json:"endpoint,omitempty"`
}

type Manifest struct {
	Prerender string `json:"prerender,omitempty"`
	// Scripts binds global early dependency configuration across navigation.
	Scripts string            `json:"scripts,omitempty"`
	Public  map[string]string `json:"public,omitempty"`
	// Draft marks managed preview policy, never a session token or signing key.
	Draft bool `json:"draft,omitempty"`
	// Routes must follow the server's specificity order.
	Routes   []Route  `json:"routes"`
	Handlers []string `json:"handlers,omitempty"`
	// Cache identifies server cache ownership/freshness rules without exposing
	// their configuration or invalidation labels to the browser.
	Cache string `json:"cache,omitempty"`
	// Locale identifies the locale/domain ownership rules of this document.
	Locale string `json:"locale,omitempty"`
	// Rules identifies server routing policy without exposing conditions/origins.
	Rules    string `json:"rules,omitempty"`
	Parallel string `json:"parallel,omitempty"`
	// Documents conservatively bypass SPA interception for external rule paths.
	Documents []string `json:"documents,omitempty"`
}

// IsEndpoint mirrors configured handler prefixes and the first matching file
// route, so a static page can outrank a dynamic endpoint without being skipped.
func (m Manifest) IsEndpoint(escapedPath, decodedPath string) bool {
	for _, path := range m.Handlers {
		if path == paths.Logical(decodedPath) || path == decodedPath || strings.HasSuffix(path, "/") && strings.HasPrefix(decodedPath, path) {
			return true
		}
	}
	for _, pattern := range m.Documents {
		if _, ok := routing.Match(pattern, paths.Logical(escapedPath)); ok {
			return true
		}
	}
	for _, route := range m.Routes {
		if _, ok := routing.Match(route.Pattern, paths.Logical(escapedPath)); ok {
			return route.Endpoint
		}
	}
	return false
}

// ProtocolVersion versions navigation JSON independently from asset Build IDs.
const ProtocolVersion = 3
const ProtocolHeader = "X-Nextgo-Protocol"

// DraftHeader binds navigation responses to the document's preview session state.
const DraftHeader = "X-Nextgo-Draft"

// BuildHeader binds empty lifecycle probes to the document's runtime generation.
const BuildHeader = "X-Nextgo-Build"
