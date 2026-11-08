package nextgo

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/Suhaibinator/Next.go/assets"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/routing"
	"github.com/Suhaibinator/Next.go/telemetry"
)

// Font sets already own their bytes. Freeze only the declarations before locale
// construction; children share the immutable inventory under their own mount.
func freezeManagedFonts(c *Config) error {
	if c.fontFiles != nil {
		return nil
	}
	if len(c.LocalFonts) > 32 {
		return fmt.Errorf("nextgo: managed fonts exceed 32 sets")
	}
	c.fontFiles = map[string]*assets.LocalFonts{}
	entries := map[string]assets.FontAsset{}
	var total int64
	for _, set := range c.LocalFonts {
		if set == nil || len(set.Paths()) == 0 {
			return fmt.Errorf("nextgo: managed fonts require constructed nonempty snapshots")
		}
		for _, entry := range set.Manifest().Assets {
			if prior, exists := entries[entry.URL]; exists {
				if prior != entry {
					return fmt.Errorf("nextgo: conflicting managed font declaration")
				}
				continue
			}
			if len(entries) == 256 || entry.Bytes > 64<<20-total {
				return fmt.Errorf("nextgo: managed fonts exceed 256 files or 64 MiB")
			}
			for _, route := range c.Routes {
				if _, ok := routing.Match(route.Pattern, entry.URL); ok {
					return fmt.Errorf("nextgo: managed font conflicts with route %s", route.Pattern)
				}
			}
			for pattern := range c.Handlers {
				if pattern == entry.URL || strings.HasSuffix(pattern, "/") && strings.HasPrefix(entry.URL, pattern) {
					return fmt.Errorf("nextgo: managed font conflicts with handler %s", pattern)
				}
			}
			total += entry.Bytes
			entries[entry.URL], c.fontFiles[entry.URL] = entry, set
			c.fontEntries = append(c.fontEntries, entry)
		}
	}
	sort.Slice(c.fontEntries, func(i, j int) bool { return c.fontEntries[i].URL < c.fontEntries[j].URL })
	c.LocalFonts = nil // Retain only the private, immutable registration.
	return nil
}

// Explicit public font files bypass page middleware, draft sessions and
// rewrites, just like managed metadata. They live under each document mount;
// the stylesheet's relative URLs keep fonts independent of AssetPrefix/CDNs.
func (a *App) serveManagedFonts(w http.ResponseWriter, r *http.Request) bool {
	if len(a.config.fontFiles) == 0 || !strings.HasPrefix(r.URL.Path, a.paths.BasePath+assets.LocalFontPrefix) {
		return false
	}
	local, ok := localRequest(r, a.paths.BasePath)
	if !ok {
		return false
	}
	set := a.config.fontFiles[local.URL.Path]
	if set == nil {
		return false
	}
	if a.config.PrerenderBuildMode && r.Context().Value(prerenderCaptureKey{}) == nil {
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "build probe cannot serve public requests", http.StatusServiceUnavailable)
		return true
	}
	for key, values := range a.config.Headers {
		w.Header()[key] = append([]string(nil), values...)
	}
	var headersOK bool
	w, _, headersOK = a.bindHeaderRules(w, local)
	if !headersOK {
		return true
	}
	w.Header().Set(navigation.BuildHeader, a.config.BuildID)
	telemetry.SetRoute(r.Context(), local.URL.Path)
	telemetry.Handle(w, local, telemetry.Info{Name: "nextgo.font", Route: local.URL.Path}, set.ServeHTTP)
	return true
}
