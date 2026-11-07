package nextgo

import (
	"fmt"
	"strings"

	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/paths"
	"github.com/Suhaibinator/Next.go/internal/routing"

	"github.com/Suhaibinator/Next.go/metadatafiles"
)

// StaticShell is an explicit public-build-data opt-in for one generic client
// shell. Pattern must have a nonempty literal prefix and terminal optional
// catchall; Component names the one ui.ClientIsland in its zero-tail seed.
// Native Page functions remain trusted code; this is not privacy analysis.
type StaticShell struct {
	Pattern   string
	Component string
}

// StaticShellDeclaration is the versioned native build-tool declaration.
// SeedPath is derived from Pattern and is application-relative, before BasePath.
type StaticShellDeclaration struct {
	Version   int    `json:"version"`
	Pattern   string `json:"pattern"`
	Component string `json:"component"`
	SeedPath  string `json:"seedPath"`
}

// StaticExportScope identifies one document/navigation ownership namespace.
// Prefix is relative to the application's global BasePath. Configuration is
// the same opaque identity used by this scope's HTML and navigation envelopes.
type StaticExportScope struct {
	Prefix        string `json:"prefix"`
	Configuration string `json:"configuration"`
	Locale        string `json:"locale,omitempty"`
}

// ExportConfig is the bounded native build-tool contract for static publication.
// It contains public URL/compatibility information, never loaders or secrets.
type ExportConfig struct {
	StaticShell *StaticShellDeclaration `json:"staticShell,omitempty"`
	Version     int                     `json:"version"`
	Build       string                  `json:"build"`
	Paths       PathConfig              `json:"paths"`
	Scopes      []StaticExportScope     `json:"scopes"`
	Domain      string                  `json:"domain,omitempty"`
	Metadata    []metadatafiles.Entry   `json:"metadata,omitempty"`
}

// StaticExportOptions selects one configured public host. Domain routing must
// publish a separate artifact per host; automatic negotiation requires a server.
type StaticExportOptions struct{ Domain string }

// CheckStaticExport rejects request-time routing that a plain static host cannot
// implement. Page-specific streaming/actions/image restrictions are checked by
// the exporter against the rendered output before anything is published.
func (a *App) CheckStaticExport() error {
	_, err := a.StaticExportConfig()
	return err
}

// StaticExportConfig returns owned scope declarations. StaticPaths returns
// global-app-relative localized paths; navigation data belongs under each
// scope's prefix, e.g. /fr/_nextgo/data/about.json, rather than a shared namespace.
func (a *App) StaticExportConfig(options ...StaticExportOptions) (ExportConfig, error) {
	if a.config.Draft != nil {
		return ExportConfig{}, fmt.Errorf("nextgo: managed draft mode requires an HTTP server; disable draft configuration for static publication")
	}
	if len(options) > 1 {
		return ExportConfig{}, fmt.Errorf("nextgo: provide at most one static export options value")
	}
	for _, rules := range [][]compiledRule{a.redirects, a.rewrites} {
		for _, rule := range rules {
			if rule.external() || len(rule.conditions) > 0 {
				return ExportConfig{}, fmt.Errorf("nextgo: conditional/external request rules require an HTTP server; static-host rule publication is not implemented")
			}
		}
	}
	if len(a.headerRules) > 0 {
		return ExportConfig{}, fmt.Errorf("nextgo: response header rules require an HTTP server; static-host header publication is not implemented")
	}

	domain := ""
	if len(options) == 1 {
		domain = options[0].Domain
	}
	if a.locales != nil && a.locales.detect {
		return ExportConfig{}, fmt.Errorf("nextgo: managed locale static export is not implemented for request-time negotiation; disable Detect, or use the native HTTP or Lambda server")
	}
	if a.locales == nil {
		if domain != "" {
			return ExportConfig{}, fmt.Errorf("nextgo: static export domain requires configured locale domains")
		}
	} else {
		var err error
		domain, err = a.locales.exportDomain(domain)
		if err != nil {
			return ExportConfig{}, err
		}
	}
	result := ExportConfig{Version: 1, Build: a.config.BuildID, Paths: a.Paths()}
	if a.config.metadataSet != nil {
		result.Metadata = a.config.metadataSet.Entries()
		if len(result.Metadata) > 0 {
			// Earlier exporters must reject rather than omit declared resources.
			result.Version = 2
		}
	}
	result.Domain = domain
	if a.locales == nil {
		result.Scopes = []StaticExportScope{{Configuration: a.configurationID}}
	} else {
		for _, scope := range a.locales.mounts {
			if !scope.alias && scope.host == domain {
				for _, rules := range [][]compiledRule{scope.app.redirects, scope.app.rewrites} {
					for _, rule := range rules {
						if rule.external() || len(rule.conditions) > 0 {
							return ExportConfig{}, fmt.Errorf("nextgo: conditional/external request rules require an HTTP server")
						}
					}
				}
				result.Scopes = append(result.Scopes, StaticExportScope{Prefix: scope.prefix, Configuration: scope.app.configurationID, Locale: scope.code})
			}
		}
	}
	if a.config.StaticShell != nil {
		declaration, err := a.staticShellDeclaration()
		if err != nil {
			return ExportConfig{}, err
		}
		result.Version, result.StaticShell = 3, declaration
	}
	return result, nil
}

func (r *localeRouter) exportDomain(input string) (string, error) {
	if len(r.domains) == 0 {
		if input != "" {
			return "", fmt.Errorf("nextgo: static export domain requires configured locale domains")
		}
		return "", nil
	}
	if input == "" {
		return "", fmt.Errorf("nextgo: domain routing requires an explicit static export domain; select one configured host with -domain")
	}
	host, err := localeHost(input)
	if err != nil {
		return "", fmt.Errorf("nextgo: invalid static export domain: %w", err)
	}
	for _, d := range r.domains {
		candidate, _ := localeDomainHost(host, d.HTTP)
		if d.Host == candidate {
			return d.Host, nil
		}
	}
	return "", fmt.Errorf("nextgo: static export domain %q is not configured", input)
}

// This proof uses effective native registrations, rather than the discovered
// file inventory, because Configure can add endpoints and handler mounts.
func (a *App) staticShellDeclaration() (*StaticShellDeclaration, error) {
	declaration := a.config.StaticShell
	seed, err := navigation.ValidateStaticShellDeclaration(declaration.Pattern, declaration.Component)
	if err != nil {
		return nil, fmt.Errorf("nextgo: static shell: %w", err)
	}
	if _, err := navigation.StaticShellEnvelopeURL(a.paths, a.config.BuildID, strings.Repeat("0", 64)); err != nil {
		return nil, fmt.Errorf("nextgo: static shell v1 requires a paired CLI-generated build: %w", err)
	}
	if a.locales != nil || a.config.Locales != nil || a.config.localeScope != nil || a.config.Offline != nil || a.config.OfflineBuild != nil || a.config.Draft != nil || a.config.PreserveLayouts || a.parallelIdentity() != "" {
		return nil, fmt.Errorf("nextgo: static shell v1 forbids locales, managed offline/draft, retained layouts and parallel routing")
	}
	if len(a.redirects) != 0 || len(a.rewrites) != 0 || len(a.headerRules) != 0 || a.config.Actions != nil || len(a.config.ActionRegistrars) != 0 || a.config.Images != nil {
		return nil, fmt.Errorf("nextgo: static shell v1 forbids request rules, server actions and optimized images")
	}
	found := false
	for _, route := range a.routes {
		if route.Pattern == declaration.Pattern {
			if found || route.Page == nil || route.API != nil || len(route.Layouts) != 0 || len(route.LayoutIDs) != 0 || len(route.LayoutBases) != 0 || len(route.Slots) != 0 || route.Primary != nil || len(route.Primaries) != 0 || len(route.Regions) != 0 || route.Prerender != nil || route.Cache != nil {
				return nil, fmt.Errorf("nextgo: static shell requires one ordinary page without layouts, slots, regions, prerendering or page caches")
			}
			found = true
		} else if staticShellPatternOverlap(declaration.Pattern, route.Pattern) {
			return nil, fmt.Errorf("nextgo: static shell overlaps route %q", route.Pattern)
		}
	}
	if !found {
		return nil, fmt.Errorf("nextgo: static shell pattern must select an existing page exactly")
	}
	for handler := range a.config.Handlers {
		pattern, err := staticShellHandlerPattern(handler)
		if err != nil || staticShellPatternOverlap(declaration.Pattern, pattern) {
			return nil, fmt.Errorf("nextgo: static shell overlaps or cannot validate handler %q", handler)
		}
	}
	for _, pattern := range documentPathPatterns(a.config.DocumentPaths) {
		if staticShellPatternOverlap(declaration.Pattern, pattern) {
			return nil, fmt.Errorf("nextgo: static shell overlaps DocumentPaths")
		}
	}
	for _, pattern := range a.metadataEndpointPatterns() {
		if staticShellPatternOverlap(declaration.Pattern, pattern) {
			return nil, fmt.Errorf("nextgo: static shell overlaps managed metadata")
		}
	}
	// Runtime services and local assets dispatch before application routes.
	for _, namespace := range []string{"/_nextgo/*nextgoReserved?", "/assets/*nextgoAssets?"} {
		if staticShellPatternOverlap(declaration.Pattern, namespace) {
			return nil, fmt.Errorf("nextgo: static shell overlaps a reserved namespace")
		}
	}
	publicPattern := a.paths.BasePath + declaration.Pattern
	for _, namespace := range []string{a.paths.AssetPath() + "/_nextgo/*nextgoReserved?", a.paths.AssetPath() + "/assets/*nextgoAssets?"} {
		if staticShellPatternOverlap(publicPattern, namespace) {
			return nil, fmt.Errorf("nextgo: static shell overlaps the configured asset mount")
		}
	}
	return &StaticShellDeclaration{Version: 1, Pattern: declaration.Pattern, Component: declaration.Component, SeedPath: seed}, nil
}

// One input is a validated literal-prefix optional catchall. Walking its fixed
// prefix against the route grammar proves whether their accepted path sets can
// intersect; raw string prefix checks miss parameter/catchall ancestors.
func staticShellPatternOverlap(shell, pattern string) bool {
	if _, err := routing.Validate(shell); err != nil {
		return true
	}
	if _, err := routing.Validate(pattern); err != nil {
		return true
	}
	prefix := strings.Split(strings.TrimPrefix(shell[:strings.LastIndexByte(shell, '/')], "/"), "/")
	parts := strings.Split(strings.TrimPrefix(pattern, "/"), "/")
	if pattern == "/" {
		parts = nil
	}
	for i, literal := range prefix {
		if i >= len(parts) {
			return false
		}
		if strings.HasPrefix(parts[i], "*") {
			return true
		}
		if !strings.HasPrefix(parts[i], ":") && parts[i] != literal {
			return false
		}
	}
	return true
}

func staticShellHandlerPattern(handler string) (string, error) {
	if handler == "/" {
		return "/*nextgoHandler?", nil
	}
	prefix := strings.TrimSuffix(handler, "/")
	if err := (paths.Config{BasePath: prefix}).Validate(); err != nil || prefix == "" {
		return "", fmt.Errorf("nonliteral handler")
	}
	if strings.HasSuffix(handler, "/") {
		return prefix + "/*nextgoHandler?", nil
	}
	return prefix, nil
}
