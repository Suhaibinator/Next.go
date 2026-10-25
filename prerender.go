package nextgo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/internal/prerenderid"
	"github.com/Suhaibinator/Next.go/internal/wirejson"
	"github.com/Suhaibinator/Next.go/scripts"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

// PrerenderFallback controls paths omitted from a dynamic route's StaticParams.
type PrerenderFallback string

const (
	FallbackBlocking PrerenderFallback = "blocking"
	FallbackNotFound PrerenderFallback = "not-found"
)

// PrerenderPolicy explicitly declares public output independent of credentials,
// request headers and the runtime host. Build requests contain no query/body or
// credentials. Never read private data in a prerendered page or its layouts.
// Expire defaults to cache.Never. Shared opts into Config.SharedCache and an
// online build authority; otherwise runtime regeneration is process-local.
type PrerenderPolicy struct {
	Shared bool `json:",omitempty"`
	// Partial captures a public shell containing registered ui.RequestRegion
	// fallbacks. Only the regions execute against each incoming request.
	Partial         bool `json:",omitempty"`
	HostIndependent bool
	Revalidate      time.Duration
	Expire          time.Duration
	Tags            []string
	Fallback        PrerenderFallback
}

type prerenderCaptureKey struct{}
type prerenderCapture struct {
	pattern, shape, path string
	value                cachedPage
	err                  error
	seen                 bool
}
type prerenderScope struct {
	app  *App
	host string
}
type prerenderManifest struct {
	Version     int                        `json:"version"`
	Code        string                     `json:"code"`
	Pages       []prerenderPage            `json:"pages"`
	SharedSeeds []prerenderSeedReservation `json:"sharedSeeds,omitempty"`
}
type prerenderSeedReservation struct {
	Scope  string `json:"scope"`
	Ticket string `json:"ticket"`
}
type prerenderPage struct {
	Shape       string            `json:"shape"`
	Pattern     string            `json:"pattern"`
	Path        string            `json:"path"`
	GeneratedAt time.Time         `json:"generatedAt"`
	HTML        string            `json:"html"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Meta        map[string]string `json:"meta,omitempty"`
	Document    Document          `json:"document"`
	Head        []seo.Element     `json:"head"`
	HeadHTML    string            `json:"headHTML"`
	Regions     []ui.RegionSpec   `json:"regions,omitempty"`
}

const maxPrerenderBytes = 32 << 20

// Prerendering reports a build-time render. Do not use this to select different
// public output; it is useful for diagnostics and build-only data validation.
func (c *Context) Prerendering() bool { return c.Value(prerenderCaptureKey{}) != nil }

func (a *App) initPrerenderPolicies() error {
	for i := range a.routes {
		r := &a.routes[i]
		if r.Prerender == nil {
			continue
		}
		if r.API != nil || r.Cache != nil || len(r.Slots) > 0 || hasPrimaries(*r) {
			return fmt.Errorf("route %s: prerender requires an ordinary page without Cache or parallel owners", r.Pattern)
		}
		p := *r.Prerender
		if p.Partial && len(r.Regions) == 0 {
			return fmt.Errorf("route %s: partial prerendering requires registered request regions", r.Pattern)
		}
		p.Tags = slices.Clone(p.Tags)
		if !p.HostIndependent {
			return fmt.Errorf("route %s: prerender requires explicit HostIndependent", r.Pattern)
		}
		if p.Expire == 0 {
			p.Expire = cache.Never
		}
		if p.Fallback == "" {
			p.Fallback = FallbackBlocking
		}
		if p.Fallback != FallbackBlocking && p.Fallback != FallbackNotFound {
			return fmt.Errorf("route %s: invalid prerender fallback", r.Pattern)
		}
		life := cache.Lifetime{Revalidate: p.Revalidate, Expire: p.Expire}
		if err := life.Validate(); err != nil {
			return fmt.Errorf("route %s: %w", r.Pattern, err)
		}
		if len(p.Tags) > 64 {
			return fmt.Errorf("route %s: too many prerender tags", r.Pattern)
		}
		for _, tag := range p.Tags {
			if len(tag) == 0 || len(tag) > 256 {
				return fmt.Errorf("route %s: invalid prerender tag", r.Pattern)
			}
		}
		r.Prerender = &p
		r.Cache = &CachePolicy{Shared: p.Shared, Lifetime: &life, Tags: slices.Clone(p.Tags)}
	}
	return nil
}
func (a *App) prerenderIdentity() string {
	var values []any
	for _, r := range a.routes {
		if r.Prerender != nil {
			values = append(values, struct {
				Pattern    string
				Policy     PrerenderPolicy
				Parameters [][2]string
				Regions    []string
			}{r.Pattern, *r.Prerender, staticParameterIdentity(r), requestRegionNames(r)})
		}
	}
	if len(values) == 0 {
		return ""
	}
	data, _ := json.Marshal(values)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func (a *App) prerenderScopes() []prerenderScope {
	defaultHost := "prerender.invalid"
	if zone := a.config.Zone(); zone.Host != "" {
		defaultHost = zone.Host
	}
	if a.locales == nil {
		return []prerenderScope{{a, defaultHost}}
	}
	var scopes []prerenderScope
	for _, s := range a.locales.mounts {
		if !s.alias {
			host := s.host
			if host == "" {
				host = defaultHost
			}
			scopes = append(scopes, prerenderScope{s.app, host})
		}
	}
	return scopes
}
func (a *App) prerenderKey(logical string) string { return a.configurationID + "|prerender|" + logical }
func (a *App) prerenderPaths(ctx context.Context) ([]string, error) {
	copy := *a
	copy.locales = nil
	copy.routes = nil
	for _, r := range a.routes {
		if r.Prerender != nil {
			copy.routes = append(copy.routes, r)
		}
	}
	// Keep the full route selection table for shadow checks.
	paths, err := copy.StaticPaths(ctx)
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		for _, r := range a.routes {
			if _, ok := match(r.Pattern, path); ok {
				if r.Prerender == nil {
					return nil, fmt.Errorf("prerender path is shadowed by %s", r.Pattern)
				}
				break
			}
		}
	}
	return paths, nil
}

// BuildPrerender renders explicitly opted-in paths through the full middleware
// chain. The returned bounded private artifact must be embedded with the final
// PrerenderBuildID. It is trusted executable data, never an upload format.
func (a *App) BuildPrerender(ctx context.Context) ([]byte, error) {
	if ctx == nil || !a.config.PrerenderBuildMode {
		return nil, fmt.Errorf("nextgo: prerender build mode and context required")
	}
	ctx = context.WithValue(ctx, localeStaticBudgetKey{}, &localeStaticBudget{})
	result := prerenderManifest{Version: 1, Code: a.config.BuildID}
	// Reserve before StaticParams, pages, layouts or metadata read application
	// data. Any acknowledged invalidation during the build revokes this ticket.
	reservations, err := a.reservePrerenderSeeds(ctx)
	if err != nil {
		return nil, err
	}
	if len(reservations) > 0 {
		result.Version = 2
		result.SharedSeeds = reservations
	}
	total := 0
	for _, scope := range a.prerenderScopes() {
		paths, err := scope.app.prerenderPaths(ctx)
		if err != nil {
			return nil, err
		}
		for _, logical := range paths {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if len(result.Pages) >= 10000 {
				return nil, fmt.Errorf("nextgo: prerender exceeds 10000 pages")
			}
			capture := &prerenderCapture{shape: scope.app.prerenderShape, path: logical}
			for _, r := range scope.app.routes {
				if _, ok := match(r.Pattern, logical); ok {
					capture.pattern = r.Pattern
					break
				}
			}
			public, err := scope.app.paths.URL(logical)
			if err != nil {
				return nil, err
			}
			request, err := http.NewRequestWithContext(context.WithValue(ctx, prerenderCaptureKey{}, capture), "GET", "http://"+scope.host+public, nil)
			if err != nil {
				return nil, err
			}
			response := &prerenderWriter{header: make(http.Header)}
			a.ServeHTTP(response, request)
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			if capture.err != nil {
				return nil, fmt.Errorf("prerender %s: %w", logical, capture.err)
			}
			if !capture.seen || response.status != 200 || response.overflow || privatePrerenderHeader(response.header) {
				return nil, fmt.Errorf("prerender %s: middleware, response controls, redirects or private output cannot be captured", logical)
			}
			v := capture.value
			if strings.Contains(v.HTML, a.config.BuildID) || strings.Contains(v.Page.preparedHead.markup, a.config.BuildID) {
				return nil, fmt.Errorf("prerender %s: output depends on provisional build ID", logical)
			}
			if len(v.Regions) > 0 {
				result.Version = 3
			}
			result.Pages = append(result.Pages, prerenderPage{Shape: capture.shape, Pattern: capture.pattern, Path: logical, GeneratedAt: v.GeneratedAt, HTML: v.HTML, Title: v.Page.Title, Description: v.Page.Description, Meta: v.Page.Meta, Document: *v.Page.document, Head: v.Page.preparedHead.elements, HeadHTML: v.Page.preparedHead.markup, Regions: v.Regions})
			total += len(v.HTML) + len(v.Page.preparedHead.markup) + len(logical)
			for _, region := range v.Regions {
				total += 96 + len(region.ID) + len(region.Region) + len(region.Props)
			}
			if total > maxPrerenderBytes/2 {
				return nil, fmt.Errorf("nextgo: prerender artifact exceeds budget")
			}
		}
	}
	if _, _, err := a.collectPrerenderMembership(ctx, result, a.prerenderAllScopes()); err != nil {
		return nil, err
	}
	for _, page := range result.Pages {
		encoded, _ := json.Marshal(page)
		if strings.Contains(string(encoded), a.config.BuildID) {
			return nil, fmt.Errorf("nextgo: prerender output depends on provisional build ID")
		}
	}
	data, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	if len(data) > maxPrerenderBytes {
		return nil, fmt.Errorf("nextgo: prerender artifact exceeds 32 MiB")
	}
	return data, nil
}

// PrerenderBuildID binds the private snapshot bytes to the compiled code ID.
func PrerenderBuildID(code string, data []byte) string { return prerenderid.Build(code, data) }
func privatePrerenderHeader(h http.Header) bool {
	policy := strings.ToLower(strings.Join(h.Values("Cache-Control"), ","))
	return len(h.Values("Set-Cookie")) > 0 || strings.Contains(policy, "private") || strings.Contains(policy, "no-store") || h.Get("X-Nextgo-Nonce") != ""
}

type prerenderWriter struct {
	header        http.Header
	status, bytes int
	overflow      bool
}

func (w *prerenderWriter) Header() http.Header { return w.header }
func (w *prerenderWriter) WriteHeader(code int) {
	if w.status == 0 {
		w.status = code
	}
}
func (w *prerenderWriter) Write(p []byte) (int, error) {
	if w.status == 0 {
		w.status = 200
	}
	w.bytes += len(p)
	if w.bytes > 4<<20 {
		w.overflow = true
		return 0, fmt.Errorf("prerender document exceeds 4 MiB")
	}
	return len(p), nil
}

func (a *App) capturePrerender(c *Context, r Route, w http.ResponseWriter, capture *prerenderCapture) (Page, int) {
	if scripts.Nonce(c.Context) != "" {
		capture.err = fmt.Errorf("per-request CSP nonces require dynamic rendering")
		return errorPage(), 500
	}
	if !a.config.PrerenderBuildMode || capture.seen || capture.pattern != r.Pattern || capture.path != c.Request.URL.EscapedPath() || capture.shape != a.prerenderShape || r.Prerender == nil || cache.Bypassed(c.Context) || ruleSensitive(c.Context) || scripts.Nonce(c.Context) != "" || c.Request.URL.RawQuery != "" || c.Request.URL.ForceQuery || len(c.Request.Header.Values("Cookie")) > 0 || len(c.Request.Header.Values("Authorization")) > 0 || privatePrerenderHeader(w.Header()) {
		capture.err = fmt.Errorf("unsafe or mismatched build request")
		return errorPage(), 500
	}
	capture.seen = true
	v, err := a.snapshotPage(c.Context, c.Request, c.Params, c.Query, r)
	capture.err = err
	capture.value = v
	if err != nil {
		return errorPage(), 500
	}
	if w.Header().Get("Cache-Control") == "" {
		w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
	}
	page := v.Page
	page.Body = renderedHTML(v.HTML)
	return page, 200
}

func (a *App) initPrerenderRuntime() error {
	if a.config.prerenderDeferred || a.config.PrerenderBuildMode {
		return nil
	}
	scopes := a.prerenderAllScopes()
	hasPolicy := false
	for _, scope := range scopes {
		for _, r := range scope.app.routes {
			hasPolicy = hasPolicy || r.Prerender != nil
		}
	}
	if !hasPolicy {
		return nil
	}
	if a.config.Phase == PhaseStaticExport {
		// Export renders its own phase configuration. Enumerate fallback
		// membership, but never read/import production artifacts or consume
		// their local/shared bootstrap tickets into this separate namespace.
		_, known, err := a.collectPrerenderMembership(a.config.startupContext, prerenderManifest{}, scopes)
		if err != nil {
			return err
		}
		for app, paths := range known {
			app.prerenderKnown = paths
		}
		return nil
	}
	var manifest prerenderManifest
	if !a.config.Dev {
		if a.config.PrerenderFS == nil {
			return fmt.Errorf("nextgo: prerender pages require a private build artifact; add prerender.go for CLI builds or supply PrerenderFS")
		}
		file, err := a.config.PrerenderFS.Open("prerender.json")
		if err != nil {
			return fmt.Errorf("nextgo: missing prerender artifact: %w", err)
		}
		data, err := io.ReadAll(io.LimitReader(file, maxPrerenderBytes+1))
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
		if len(data) > maxPrerenderBytes {
			return fmt.Errorf("nextgo: prerender artifact exceeds 32 MiB")
		}
		if err := wirejson.Unique(data); err != nil {
			return fmt.Errorf("nextgo: invalid prerender artifact: %w", err)
		}
		if err := wirejson.Strict(data, &manifest); err != nil {
			return fmt.Errorf("nextgo: invalid prerender artifact: %w", err)
		}
		if manifest.Version != 1 && manifest.Version != 2 && manifest.Version != 3 || manifest.Version == 1 && len(manifest.SharedSeeds) > 0 || manifest.Code == "" || prerenderid.Build(manifest.Code, data) != a.config.BuildID || len(manifest.Pages) > 10000 {
			return fmt.Errorf("nextgo: prerender artifact/build mismatch")
		}
	}
	groups, known, err := a.collectPrerenderMembership(a.config.startupContext, manifest, scopes)
	if err != nil {
		return err
	}
	for app, paths := range known {
		app.prerenderKnown = paths
	}
	for store, seeds := range groups {
		if _, err := cache.Bootstrap(a.config.startupContext, store, seeds); err != nil {
			if !errors.Is(err, cache.ErrAlreadyUsed) {
				return err
			}
			a.config.Logger.Printf("nextgo prerender: cache already used; build snapshots skipped")
		}
	}
	if err := a.initSharedPrerenderSeeds(manifest, scopes); err != nil {
		return err
	}
	return nil
}

func prerenderDecodedPath(path string) string { decoded, _ := url.PathUnescape(path); return decoded }

func (a *App) prerenderAllScopes() []prerenderScope {
	scopes := a.prerenderScopes()
	if a.locales != nil {
		scopes = nil
		for _, scope := range a.locales.mounts {
			scopes = append(scopes, prerenderScope{app: scope.app, host: scope.host})
		}
	}
	return scopes
}

func (a *App) collectPrerenderMembership(ctx context.Context, manifest prerenderManifest, scopes []prerenderScope) (map[*cache.Store][]cache.Seed[cachedPage], map[*App]map[string]bool, error) {
	budget := &localeStaticBudget{}
	enumerationCtx, cancel := context.WithTimeout(context.WithValue(ctx, localeStaticBudgetKey{}, budget), 30*time.Second)
	defer cancel()
	knownBytes := 0
	groups := map[*cache.Store][]cache.Seed[cachedPage]{}
	known := map[*App]map[string]bool{}
	for _, scope := range scopes {
		app := scope.app
		known[app] = map[string]bool{}
		matched := false
		seen := map[string]bool{}
		for _, p := range manifest.Pages {
			if p.Shape != app.prerenderShape {
				continue
			}
			matched = true
			if seen[p.Path] || len(p.Path) > 8192 || p.GeneratedAt.IsZero() {
				return nil, nil, fmt.Errorf("nextgo: invalid prerender entry")
			}
			seen[p.Path] = true
			var selected *Route
			for i := range app.routes {
				if _, ok := match(app.routes[i].Pattern, p.Path); ok {
					selected = &app.routes[i]
					break
				}
			}
			if selected == nil || selected.Prerender == nil || selected.Pattern != p.Pattern {
				return nil, nil, fmt.Errorf("nextgo: prerender route mismatch")
			}
			if budget.rows.Add(1) > 10000 {
				return nil, nil, fmt.Errorf("nextgo: prerender membership exceeds 10000 paths")
			}
			known[app][p.Path] = true
			value := ownCachedPage(cachedPage{Page: Page{Title: p.Title, Description: p.Description, Meta: maps.Clone(p.Meta), document: &p.Document, preparedHead: &renderedHead{elements: p.Head, markup: p.HeadHTML}}, HTML: p.HTML, Regions: p.Regions, GeneratedAt: p.GeneratedAt, Prerendered: true})
			if manifest.Version != 3 && len(p.Regions) > 0 {
				return nil, nil, fmt.Errorf("nextgo: partial shell requires artifact version 3")
			}
			if err := validatePageShell(value, *selected); err != nil {
				return nil, nil, fmt.Errorf("nextgo: invalid prerender shell: %w", err)
			}
			if !selected.Prerender.Shared {
				groups[app.config.Cache] = append(groups[app.config.Cache], cache.Seed[cachedPage]{Key: app.prerenderKey(p.Path), Value: value, LoadedAt: p.GeneratedAt, Options: cache.Options{Lifetime: selected.Cache.Lifetime, Tags: selected.Cache.Tags, Paths: []string{prerenderDecodedPath(p.Path)}}})
			}
		}
		if !matched {
			// Runtime mount/public/locale settings can differ from the build. Enumerate
			// only for fallback membership; no mismatched HTML is reused.
			paths, err := app.prerenderPaths(enumerationCtx)
			if err != nil {
				return nil, nil, err
			}
			for _, p := range paths {
				known[app][p] = true
			}
		}
		for path := range known[app] {
			knownBytes += len(path)
			if knownBytes > 16<<20 {
				return nil, nil, fmt.Errorf("nextgo: prerender membership exceeds 16 MiB")
			}
		}
	}
	return groups, known, nil
}
