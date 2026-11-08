// Package navigation contains the public route-kind manifest shared by the
// server and browser. It contains patterns only, never loader data or imports.
package navigation

import (
	"fmt"
	"github.com/Suhaibinator/Next.go/internal/paths"
	"github.com/Suhaibinator/Next.go/internal/routing"
	"strings"
)

type Route struct {
	Pattern  string `json:"pattern"`
	Endpoint bool   `json:"endpoint,omitempty"`
	// Root is an opaque document-layout identity, emitted for multi-root apps.
	Root string `json:"root,omitempty"`
}

// SameRoot keeps ordinary single-root manifests backward compatible. Once a
// root is declared, unknown paths and missing document identities require a
// native document request. Specificity follows the same order as IsEndpoint.
func (m Manifest) SameRoot(escapedPath, current string) bool {
	usesRoots := false
	if m.routeIndex != nil {
		usesRoots = m.hasRoots
	} else {
		for _, route := range m.Routes {
			if route.Root != "" {
				usesRoots = true
				break
			}
		}
	}
	if !usesRoots {
		return current == ""
	}
	if !ValidConfigurationID(current) {
		return false
	}
	return m.RootForPath(escapedPath) == current
}

// RootForPath returns only known page roots, respecting route specificity.
func (m Manifest) RootForPath(escapedPath string) string {
	if route, ok := m.routeForPath(escapedPath); ok && !route.Endpoint {
		return route.Root
	}
	return ""
}

type Manifest struct {
	routeIndex     *routing.Index
	compiledRoutes []Route
	hasRoots       bool
	// MergeVersion negotiates the independent buffered client-island extension.
	MergeVersion int `json:"mergeVersion,omitempty"`
	// RetainRoutes opts into bounded document-local UI ownership, not HTTP reuse.
	RetainRoutes bool `json:"retainRoutes,omitempty"`
	// Development suppresses automatic viewport speculation, not navigation.
	Development bool   `json:"development,omitempty"`
	Prerender   string `json:"prerender,omitempty"`
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
	// Documents conservatively bypass SPA interception for external rule paths
	// and explicitly declared full-document subtrees. They use route grammar.
	Documents []string `json:"documents,omitempty"`
}

// CompileRoutes freezes a private lookup snapshot without changing the public
// wire/configuration identity. Call once after decoding, before serving readers.
// Uncompiled values retain the original ordered linear lookup semantics.
func (m *Manifest) CompileRoutes() error {
	routes := append([]Route(nil), m.Routes...)
	patterns := make([]string, len(routes))
	hasRoots := false
	for i, route := range routes {
		patterns[i] = route.Pattern
		if route.Root != "" {
			if !ValidConfigurationID(route.Root) {
				return fmt.Errorf("invalid route root identity")
			}
			hasRoots = true
		}
	}
	index, err := routing.NewIndex(patterns)
	if err != nil {
		return err
	}
	m.routeIndex, m.compiledRoutes, m.hasRoots = index, routes, hasRoots
	return nil
}

func (m Manifest) routeForPath(escapedPath string) (Route, bool) {
	path := paths.Logical(escapedPath)
	if m.routeIndex != nil {
		if at, ok := m.routeIndex.Lookup(path); ok {
			return m.compiledRoutes[at], true
		}
		return Route{}, false
	}
	for _, route := range m.Routes {
		if _, ok := routing.Match(route.Pattern, path); ok {
			return route, true
		}
	}
	return Route{}, false
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
	if route, ok := m.routeForPath(escapedPath); ok {
		return route.Endpoint
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
