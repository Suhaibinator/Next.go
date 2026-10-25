package nextgo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/internal/actionview"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/paths"
	"github.com/Suhaibinator/Next.go/internal/postresponse"
	"github.com/Suhaibinator/Next.go/telemetry"
	"github.com/Suhaibinator/Next.go/ui"
)

func (a *App) validateConfig() error {
	for _, route := range a.routes {
		if policy := route.Cache; policy != nil && policy.Lifetime != nil {
			if policy.TTL != 0 {
				return fmt.Errorf("route %s: choose cache TTL or Lifetime", route.Pattern)
			}
			if err := policy.Lifetime.Validate(); err != nil {
				return fmt.Errorf("route %s: %w", route.Pattern, err)
			}
		}
	}
	for path, handler := range a.config.Handlers {
		if !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || handler == nil {
			return fmt.Errorf("invalid configured handler %q", path)
		}
	}
	return nil
}
func localURL(value string) bool {
	u, err := url.Parse(value)
	return err == nil && strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//") && !strings.ContainsAny(value, "\\\r\n") && u.Host == "" && u.Scheme == ""
}
func (a *App) serveConfigured(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path == "/_nextgo/draft" {
		telemetry.SetRoute(r.Context(), "/_nextgo/draft")
		telemetry.Handle(w, r, telemetry.Info{Name: "nextgo.draft"}, a.serveDraftProbe)
		return true
	}
	if a.config.Actions != nil && strings.HasPrefix(r.URL.Path, "/_nextgo/actions/") {
		telemetry.SetRoute(r.Context(), "/_nextgo/actions/:action")
		if a.redirectCanonical(w, r) {
			return true
		}
		if len(r.Header.Values(actionview.Header)) > 0 {
			telemetry.Handle(w, r, telemetry.Info{Name: "nextgo.action"}, a.serveActionView)
			return true
		}
		telemetry.Handle(w, r, telemetry.Info{Name: "nextgo.action"}, http.StripPrefix("/_nextgo/actions/", a.config.Actions).ServeHTTP)
		return true
	}
	if a.config.Images != nil && r.URL.Path == "/_nextgo/image" {
		telemetry.SetRoute(r.Context(), "/_nextgo/image")
		if a.redirectCanonical(w, r) {
			return true
		}
		a.config.Images.ServeHTTP(w, r)
		return true
	}
	if r.URL.Path == "/_nextgo/loading" {
		telemetry.SetRoute(r.Context(), "/_nextgo/loading")
		if a.redirectCanonical(w, r) {
			return true
		}
		a.serveLoading(w, r)
		return true
	}
	keys := make([]string, 0, len(a.config.Handlers))
	for path := range a.config.Handlers {
		keys = append(keys, path)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	source := a.sourceRequest(r)
	for _, path := range keys {
		prefix := strings.HasSuffix(path, "/")
		if r.URL.Path == path || source.URL.Path == path || prefix && strings.HasPrefix(source.URL.Path, path) {
			telemetry.SetRoute(r.Context(), path)
			if !prefix && a.redirectCanonical(w, r) {
				return true
			}
			telemetry.Handle(w, source, telemetry.Info{Name: "nextgo.handler"}, a.config.Handlers[path].ServeHTTP)
			return true
		}
	}
	for _, rule := range a.redirects {
		if params, ok := rule.match(r); ok {
			telemetry.SetRoute(r.Context(), rule.source)
			target, err := rule.destination(params, r.URL)
			if err != nil {
				http.Error(w, "Invalid rule destination", 400)
				return true
			}
			location := target.String()
			if !rule.external() {
				location, err = a.paths.URL(location)
			}
			if err != nil {
				http.Error(w, "Invalid rule destination", 400)
				return true
			}
			if ruleSensitive(r.Context()) {
				w.Header().Set("Cache-Control", "private, no-store")
			}
			http.Redirect(w, r, location, rule.status)
			return true
		}
	}
	return false
}
func (a *App) resolveRewrite(r *http.Request) (*compiledRule, *url.URL, error) {
	for i := range a.rewrites {
		rule := &a.rewrites[i]
		if params, ok := rule.match(r); ok {
			target, err := rule.destination(params, r.URL)
			return rule, target, err
		}
	}
	return nil, nil, nil
}
func (a *App) applyRewrites(w http.ResponseWriter, r *http.Request) bool {
	rule, target, err := a.resolveRewrite(r)
	if err != nil {
		http.Error(w, "Invalid rule destination", 400)
		return true
	}
	if rule == nil {
		return false
	}
	if rule.external() {
		telemetry.SetRoute(r.Context(), rule.source)
		a.serveProxy(w, r, target, rule.source)
		return true
	}
	escaped := paths.Logical(target.EscapedPath())
	target.Path, _ = url.PathUnescape(escaped)
	target.RawPath = escaped
	r.URL.Path, r.URL.RawPath, r.URL.RawQuery, r.URL.ForceQuery = target.Path, target.RawPath, target.RawQuery, target.ForceQuery
	return false
}

func (a *App) serveLoading(w http.ResponseWriter, r *http.Request) {
	a.draftResponse(w, r)
	w.Header().Set(navigation.ConfigurationHeader, a.configurationID)
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != "GET" {
		w.WriteHeader(405)
		return
	}
	path := r.URL.Query().Get("path")
	if !localURL(path) {
		http.Error(w, "Invalid path", 400)
		return
	}
	u, _ := url.Parse(path)
	local, ok := a.paths.Strip(u.EscapedPath())
	if !ok {
		http.Error(w, "Invalid path", 400)
		return
	}
	decoded, err := url.PathUnescape(local)
	if err != nil {
		http.Error(w, "Invalid path", 400)
		return
	}
	if a.paths.TrailingSlash {
		local = paths.Logical(local)
		decoded, _ = url.PathUnescape(local)
	}
	u.Path, u.RawPath = decoded, local
	logical := r.Clone(r.Context())
	logical.URL = u
	rule, target, rewriteErr := a.resolveRewrite(logical)
	if rewriteErr != nil {
		http.Error(w, "Invalid rule destination", 400)
		return
	}
	if rule != nil {
		if rule.external() {
			w.WriteHeader(204)
			return
		}
		u = target
		logical.URL = u
		logical = a.logicalRequest(logical)
		u = logical.URL
	}
	for _, route := range a.routes {
		if params, ok := match(route.Pattern, u.EscapedPath()); ok && route.Loading != nil {
			ctx := &Context{Context: r.Context(), Request: r, Params: params, Query: u.Query()}
			ctx.Request = r.Clone(r.Context())
			ctx.Request.URL = u
			var node ui.Node
			var err error
			func() {
				defer func() {
					if p := recover(); p != nil {
						err = fmt.Errorf("loading panic: %v", p)
					}
				}()
				node, err = route.Loading(ctx)
			}()
			status := http.StatusOK
			if err != nil {
				status = http.StatusInternalServerError
			}
			status, redirected, responseErr := a.applyResponse(w, r, ctx, status)
			if responseErr != nil {
				a.config.Logger.Printf("loading response controls: %v", responseErr)
				http.Error(w, "Unable to load fallback", http.StatusInternalServerError)
				return
			}
			if redirected {
				return
			}
			if err != nil {
				a.config.Logger.Printf("loading: %v", err)
				http.Error(w, "Unable to load fallback", 500)
				return
			}
			html, err := ui.HTMLContext(ctx.Context, node)
			if err != nil {
				http.Error(w, "Unable to render fallback", 500)
				return
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			a.draftResponse(w, r)
			w.Header().Set(navigation.ConfigurationHeader, a.configurationID)
			w.WriteHeader(status)
			if status == http.StatusNoContent || status == http.StatusResetContent || status == http.StatusNotModified {
				return
			}
			_, _ = io.WriteString(w, html)
			return
		}
	}
	w.WriteHeader(204)
}

type cachedPage struct {
	Prerendered bool
	Page        Page
	HTML        string
	GeneratedAt time.Time
	Regions     []ui.RegionSpec
}

// CacheSize accounts the frozen HTML/head payload and conservative containers.
// Framework snapshots own their strings; this does not measure the complete Go
// heap, transient rendering buffers or container/allocator implementation details.
func (p cachedPage) CacheSize() int64 {
	size := int64(512 + len(p.HTML) + len(p.Page.Title) + len(p.Page.Description))
	for _, region := range p.Regions {
		size += int64(192 + len(region.ID) + len(region.Region) + len(region.Props))
	}
	if p.Page.document != nil {
		size += int64(128 + len(p.Page.document.Language) + len(p.Page.document.Direction) + len(p.Page.document.Viewport))
	}
	for key, value := range p.Page.Meta {
		size += int64(96 + len(key) + len(value))
	}
	if head := p.Page.preparedHead; head != nil {
		size += int64(len(head.markup))
		for _, element := range head.elements {
			size += int64(256 + len(element.Tag) + len(element.Text))
			for key, value := range element.Attrs {
				size += int64(96 + len(key) + len(value))
			}
		}
	}
	return size
}

type renderedHTML string

func (h renderedHTML) Render(w io.Writer) error { _, err := io.WriteString(w, string(h)); return err }

type uncacheable struct {
	page     Page
	status   int
	snapshot *cachedPage
}

func (e *uncacheable) Error() string { return "uncacheable render" }

func (a *App) loadPage(c *Context, route Route, w http.ResponseWriter, span *telemetry.Span) (Page, int) {
	span.Cache("disabled")
	if capture, ok := c.Value(offlineCaptureKey{}).(*offlineCapture); ok {
		return a.captureOffline(c, route, w, capture)
	}
	if len(route.Regions) > 0 && c.Value(prerenderCaptureKey{}) == nil {
		w.Header().Set("X-Nextgo-Request-Regions", "1")
		w.Header().Set("Cache-Control", "private, no-store")
		clearNavigationReuse(w.Header())
	}
	if capture, ok := c.Value(prerenderCaptureKey{}).(*prerenderCapture); ok {
		return a.capturePrerender(c, route, w, capture)
	}
	if route.Prerender != nil && route.Prerender.Partial {
		return a.loadPartialPage(c, route, w, span)
	}
	logical := c.Request.URL.EscapedPath()
	if route.Prerender != nil {
		canonical, err := staticPath(route.Pattern, Params(c.Params))
		if err != nil {
			return a.renderRouteNotFound(c, route)
		}
		logical = canonical
		if route.Prerender.Fallback == FallbackNotFound && !a.prerenderKnown[logical] {
			return a.renderRouteNotFound(c, route)
		}
	}
	policy := route.Cache
	if cache.Bypassed(c.Context) {
		span.Cache("bypass")
		w.Header().Set("Cache-Control", "private, no-store")
		clearNavigationReuse(w.Header())
		return a.render(c, route)
	}
	if len(route.Slots) > 0 || hasPrimaries(route) {
		span.Cache("bypass")
		w.Header().Set("Cache-Control", "private, no-store")
		clearNavigationReuse(w.Header())
		return a.render(c, route)
	}
	lifetime, clientStale := a.cacheLifetime(policy)
	if policy != nil && (a.config.Dev || route.Prerender != nil && (privatePrerenderHeader(w.Header()) || c.Request.URL.RawQuery != "" || c.Request.URL.ForceQuery) || (len(route.Slots) > 0 || hasPrimaries(route)) || ruleSensitive(c.Context) || len(c.Request.Header.Values("Cookie")) > 0 || len(c.Request.Header.Values("Authorization")) > 0 || len(w.Header().Values("Set-Cookie")) > 0 || c.Request.Body != nil && c.Request.Body != http.NoBody) {
		span.Cache("bypass")
		w.Header().Set("Cache-Control", "no-store")
		clearNavigationReuse(w.Header())
		return a.render(c, route)
	}
	if policy == nil || policy.TTL <= 0 && lifetime == nil {
		return a.render(c, route)
	}
	if policy.Shared && !sharedPageInputsValid(c) {
		span.Cache("bypass")
		w.Header().Set("Cache-Control", "private, no-store")
		clearNavigationReuse(w.Header())
		return a.render(c, route)
	}
	// Refresh can run after middleware and the request finish. Freeze inputs
	// now, then give each load/retry its own maps and request clone. Public cache
	// keys do not vary by request bodies, so requests carrying a body bypass it.
	input := c.Request.Clone(c.Context)
	// GET and HEAD share one representation. A HEAD-first fill must not retain
	// an application shortcut that omits its body; outer response handling still
	// suppresses the body for the actual HEAD request.
	input.Method = http.MethodGet
	input.Body = http.NoBody
	if route.Prerender != nil {
		input.Header = make(http.Header)
		input.Host = "prerender.invalid"
		if a.config.localeScope != nil && a.config.localeScope.host != "" {
			input.Host = a.config.localeScope.host
		}
		input.URL.Host = input.Host
		input.URL.Scheme = "http"
	}
	params, query := maps.Clone(c.Params), cloneQuery(c.Query)
	key := a.configurationID + "|" + c.Request.Host + "|" + c.Request.URL.RequestURI()
	if route.Prerender != nil {
		key = a.prerenderKey(logical)
	}
	if lifetime != nil {
		w.Header().Set("Cache-Control", "no-store")
		clearNavigationReuse(w.Header())
	}
	lookupStarted := time.Now()
	loader := func(ctx context.Context) (cachedPage, error) {
		return a.snapshotPage(ctx, input, params, query, route)
	}
	var value cachedPage
	var info cache.Info
	var err error
	if policy.Shared {
		value, info, err = a.lookupSharedPage(c, route, loader, lifetime)
	} else {
		value, info, err = cache.Lookup[cachedPage](c.Context, a.config.Cache, key, cache.Options{TTL: policy.TTL, Lifetime: lifetime, ScheduleRefresh: CacheRefresh(c.Context), KeepStaleOnError: keepPageSnapshot, Tags: policy.Tags, Paths: []string{c.Request.URL.Path}}, loader)
	}
	span.Cache(string(info.State))
	if err != nil {
		var e *uncacheable
		if errors.As(err, &e) {
			if policy.Shared {
				w.Header().Set("Cache-Control", "private, no-store")
				clearNavigationReuse(w.Header())
			}
			return e.page, e.status
		}
		a.config.Logger.Printf("cached render: %v", err)
		return errorPage(), 500
	}
	if policy.Shared {
		w.Header().Set("X-Nextgo-Cache", string(info.State))
	}
	if route.Prerender != nil {
		source := "runtime"
		if value.Prerendered {
			source = "build"
		}
		w.Header().Set("X-Nextgo-Prerender", source)
		w.Header().Set("X-Nextgo-Cache", string(info.State))
	}
	page := value.Page
	page.Body = renderedHTML(value.HTML)
	if lifetime != nil {
		// The Go browser cache is opt-in and independent of intermediary caches.
		// Budget from this generation, including hard expiry; elapsed lookup
		// time is deducted conservatively rather than extending its deadline.
		budget := info.ReuseFor(clientStale) - time.Since(lookupStarted)
		if millis := budget.Milliseconds(); millis > 0 {
			w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
			w.Header().Set("X-Nextgo-Prefetch", "public")
			w.Header().Set(navigation.StaleHeader, fmt.Sprint(millis))
			w.Header().Add("Vary", "Cookie, Authorization")
		}
		return page, 200
	}
	ttl := int(policy.TTL / time.Second)
	if ttl > 30 {
		ttl = 30
	}
	if ttl > 0 {
		w.Header().Set("X-Nextgo-Prefetch", "public")
		w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", ttl))
		w.Header().Add("Vary", "Cookie, Authorization")
		age := int(info.Age / time.Second)
		// HTTP Age has whole-second precision. Round up so a partial second
		// cannot extend the advertised legacy window past this generation.
		if info.Age%time.Second > 0 {
			age++
		}
		if age < 0 {
			age = 0
		}
		w.Header().Set("Age", fmt.Sprint(age))
	}
	return page, 200
}

func cloneQuery(values url.Values) url.Values {
	copy := make(url.Values, len(values))
	for key, items := range values {
		copy[key] = append([]string(nil), items...)
	}
	return copy
}

// RevalidateTag expires matching process-local entries and retries readers
// still awaiting an obsolete load. Call after the backing write commits.
func (a *App) RevalidateTag(tag string) { a.config.Cache.ExpireTag(tag) }

// RevalidatePath is RevalidateTag for path labels at or beneath path.
func (a *App) RevalidatePath(path string) { a.config.Cache.ExpirePathPrefix(path) }

func (a *App) snapshotPage(ctx context.Context, input *http.Request, params map[string]string, query url.Values, route Route) (cachedPage, error) {
	ctx = a.offlinePublicContext(a.bindZoneContext(ctx))
	partial := route.Prerender != nil && route.Prerender.Partial
	if partial {
		ctx = a.publicShellContext(ctx)
		input = a.publicShellRequest(input, ctx)
		logical, err := staticPath(route.Pattern, Params(params))
		if err != nil {
			return cachedPage{}, err
		}
		input.URL.Path, _ = url.PathUnescape(logical)
		input.URL.RawPath = logical
		input.RequestURI = logical
		query = make(url.Values)
	}
	// Public cache flights receive a fresh memo table, never private work
	// performed by middleware in the initiating request.
	ctx = context.WithValue(WithRequestMemo(postresponse.BlockRegistration(ctx, ErrAfterCache)), pageCacheReadOnlyKey{}, true)
	request := input.Clone(ctx)
	copyContext := &Context{Context: ctx, Request: request, Params: maps.Clone(params), Query: cloneQuery(query), response: responseState{public: true}}
	defer copyContext.response.seal()
	page, status := a.render(copyContext, route)
	if status != 200 || page.Stream && !partial {
		if copyContext.response.seal().violated {
			return cachedPage{}, ErrPublicCacheResponse
		}
		return cachedPage{}, &uncacheable{page: page, status: status}
	}
	page = a.localeMetadata(page)
	head, headHTML, err := pageHeadPaths(page, a.paths)
	if err != nil {
		return cachedPage{}, err
	}
	var html string
	var regions []ui.RegionSpec
	if partial {
		var shell ui.CapturedShell
		shell, err = ui.CaptureShell(ctx, page.Body)
		html, regions = shell.HTML, shell.Regions
		if err == nil {
			err = validatePageShell(cachedPage{HTML: html, Regions: regions}, route)
		}
	} else {
		html, err = ui.HTMLContext(ctx, page.Body)
	}
	if copyContext.response.seal().violated {
		return cachedPage{}, ErrPublicCacheResponse
	}
	if err != nil {
		return cachedPage{}, err
	}
	// Retain a validated value snapshot rather than component/resource
	// closures or mutable metadata maps from application code.
	title, description := page.Title, page.Description
	if title == "" {
		title = page.Metadata.Title
	}
	if description == "" {
		description = page.Metadata.Description
	}
	meta := make(map[string]string, len(page.Meta))
	for key, value := range page.Meta {
		meta[key] = value
	}
	document, err := pageDocument(page)
	if err != nil {
		return cachedPage{}, err
	}
	page = Page{document: &document, Title: title, Description: description, Meta: meta, preparedHead: &renderedHead{elements: head, markup: headHTML}}
	return ownCachedPage(cachedPage{Page: page, HTML: html, Regions: regions, GeneratedAt: time.Now()}), nil
}
