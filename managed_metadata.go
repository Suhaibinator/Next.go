package nextgo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"strings"

	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/paths"
	"github.com/Suhaibinator/Next.go/metadatafiles"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/telemetry"
)

// Freeze the public set before locale construction changes a child's BasePath.
// The shared Set owns its bytes; neither producer closures nor source FS values
// remain attached to a serving application after startup.
func freezeManagedMetadata(c *Config) error {
	if c.metadataSet != nil {
		return nil
	}
	deployment := paths.Config{BasePath: c.BasePath}
	if err := deployment.Validate(); err != nil {
		return err
	}
	set, err := metadatafiles.New(c.startupContext, c.MetadataFiles, metadatafiles.Options{BasePath: c.BasePath})
	if err != nil {
		return err
	}
	for _, entry := range set.Entries() {
		logical := "/" + entry.Name
		for _, route := range c.Routes {
			if route.Pattern == logical {
				return fmt.Errorf("nextgo: managed metadata %s conflicts with route %s", entry.Name, route.Pattern)
			}
		}
		for pattern := range c.Handlers {
			if pattern == logical || strings.HasSuffix(pattern, "/") && strings.HasPrefix(logical, pattern) {
				return fmt.Errorf("nextgo: managed metadata %s conflicts with handler %s", entry.Name, pattern)
			}
		}
	}
	c.metadataSet = set
	c.metadataBasePath = strings.Clone(c.BasePath)
	c.MetadataFiles = metadatafiles.Sources{}
	return nil
}
func configurationMetadataPolicy(cacheID, digest string) string {
	if digest == "" {
		return cacheID
	}
	sum := sha256.Sum256([]byte("nextgo.cache.metadata.v1\x00" + cacheID + "\x00" + digest))
	return hex.EncodeToString(sum[:])
}
func (a *App) metadataEndpointPatterns() []string {
	if a.config.metadataSet == nil || a.paths.BasePath != a.config.metadataBasePath {
		return nil
	}
	var patterns []string
	for _, entry := range a.config.metadataSet.Entries() {
		patterns = append(patterns, "/"+entry.Name)
	}
	return patterns
}

// Managed files are explicitly public startup bytes, like the alternate asset
// mount. They bypass application page middleware, rewrites, locale negotiation
// and draft binding. Header rules still match the global logical resource URL;
// conditional header rules retain their normal private-response behavior.
func (a *App) serveManagedMetadata(w http.ResponseWriter, r *http.Request) bool {
	set := a.config.metadataSet
	if set == nil || !set.Handles(r.URL.EscapedPath()) {
		return false
	}
	if a.config.PrerenderBuildMode && r.Context().Value(prerenderCaptureKey{}) == nil {
		w.Header().Set("Cache-Control", "no-store")
		http.Error(w, "build probe cannot serve public requests", http.StatusServiceUnavailable)
		return true
	}
	local, ok := localRequest(r, a.config.metadataBasePath)
	if !ok {
		return false
	}
	for key, values := range a.config.Headers {
		w.Header()[key] = append([]string(nil), values...)
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if w.Header().Get("Referrer-Policy") == "" {
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
	}
	var headersOK bool
	w, _, headersOK = a.bindHeaderRules(w, local)
	if !headersOK {
		return true
	}
	w.Header().Set(navigation.BuildHeader, a.config.BuildID)
	route := local.URL.EscapedPath()
	telemetry.SetRoute(r.Context(), route)
	telemetry.Handle(w, r, telemetry.Info{Name: "nextgo.metadata", Route: route}, set.ServeHTTP)
	return true
}

// Called after route metadata has merged, before cached/prerender head capture.
// Prepared heads already contain final defaults and must never append them again.
func (a *App) withManagedMetadataHead(page Page) Page {
	if page.preparedHead != nil {
		return page
	}
	page.managedHead = nil
	if a.config.metadataSet == nil {
		return page
	}
	for _, element := range a.config.metadataSet.Head() {
		switch element.Attrs["rel"] {
		case "manifest":
			if page.Metadata.Manifest != nil {
				continue
			}
		case "icon", "apple-touch-icon":
			if page.Metadata.Icons != nil {
				continue
			}
		}
		page.managedHead = append(page.managedHead, element)
	}
	return page
}

// Clone final default links when appending so rendering never lends a Set-owned
// map (or a previous render's mutable element map) to the response/cache.
func appendManagedHead(head []seo.Element, defaults []seo.Element) []seo.Element {
	for _, element := range defaults {
		attrs := make(map[string]string, len(element.Attrs))
		for key, value := range element.Attrs {
			attrs[key] = value
		}
		element.Attrs = attrs
		head = append(head, element)
	}
	return head
}
