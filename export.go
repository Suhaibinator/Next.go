package nextgo

import (
	"fmt"

	"github.com/Suhaibinator/Next.go/metadatafiles"
)

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
	Version  int                   `json:"version"`
	Build    string                `json:"build"`
	Paths    PathConfig            `json:"paths"`
	Scopes   []StaticExportScope   `json:"scopes"`
	Domain   string                `json:"domain,omitempty"`
	Metadata []metadatafiles.Entry `json:"metadata,omitempty"`
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
