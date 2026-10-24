package nextgo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/paths"
	"github.com/Suhaibinator/Next.go/scripts"
	"github.com/Suhaibinator/Next.go/telemetry"
	"github.com/Suhaibinator/Next.go/ui"
)

var requestRegionName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,127}$`)

func requestRegionNames(route Route) []string {
	return slices.Sorted(maps.Keys(route.Regions))
}

func (a *App) initRequestRegions() error {
	for i := range a.routes {
		r := &a.routes[i]
		if len(r.Regions) > ui.MaxShellRegions {
			return fmt.Errorf("route %s: too many request region registrations", r.Pattern)
		}
		if len(r.Regions) > 0 && (r.API != nil || len(r.Slots) > 0 || hasPrimaries(*r)) {
			return fmt.Errorf("route %s: request regions require an ordinary page", r.Pattern)
		}
		if len(r.Regions) > 0 && r.Cache != nil && (r.Prerender == nil || !r.Prerender.Partial) {
			return fmt.Errorf("route %s: request regions cannot enter a full-page cache; use partial prerendering", r.Pattern)
		}
		for name, render := range r.Regions {
			if !requestRegionName.MatchString(name) || render == nil {
				return fmt.Errorf("route %s: invalid request region registration %q", r.Pattern, name)
			}
		}
		r.Regions = maps.Clone(r.Regions)
	}
	return nil
}

func validatePageShell(value cachedPage, route Route) error {
	partial := route.Prerender != nil && route.Prerender.Partial
	if partial != (len(value.Regions) > 0) {
		return fmt.Errorf("nextgo: partial shell declaration mismatch")
	}
	if !partial {
		return nil
	}
	if err := ui.ValidateShell(ui.CapturedShell{HTML: value.HTML, Regions: value.Regions}); err != nil {
		return err
	}
	for _, region := range value.Regions {
		if route.Regions[region.Region] == nil {
			return fmt.Errorf("nextgo: unregistered request region %q", region.Region)
		}
	}
	return nil
}

func materializePage(value cachedPage, route Route) (Page, error) {
	if err := validatePageShell(value, route); err != nil {
		return Page{}, err
	}
	page := value.Page
	page.Body = renderedHTML(value.HTML)
	if len(value.Regions) > 0 {
		body, err := ui.RestoreShell(ui.CapturedShell{HTML: value.HTML, Regions: value.Regions})
		if err != nil {
			return Page{}, err
		}
		page.Body, page.Stream = body, true
	}
	return page, nil
}

func (a *App) requestRegionResolver(c *Context, route Route) ui.RegionResolver {
	input, params, query := c.Request.Clone(c.Context), maps.Clone(c.Params), cloneQuery(c.Query)
	return func(ctx context.Context, name string, props json.RawMessage) (ui.Node, error) {
		fn := route.Regions[name]
		if fn == nil {
			return nil, fmt.Errorf("nextgo: unregistered request region %q", name)
		}
		regional := &Context{Context: ctx, Request: input.Clone(ctx), Params: maps.Clone(params), Query: cloneQuery(query), response: responseState{sealed: true}}
		return fn(regional, props)
	}
}

// Shell regeneration may be initiated by an authenticated request. Keep its
// cancellation/deadline but none of its middleware values or authentication.
// Reintroduce only immutable framework-owned public configuration below.
type shellContext struct{ context.Context }

func (shellContext) Value(any) any { return nil }

func (a *App) publicShellContext(ctx context.Context) context.Context {
	public := context.Context(shellContext{ctx})
	public = paths.WithContext(public, a.paths)
	public = context.WithValue(public, publicContextKey{}, a.config.Public)
	public = context.WithValue(public, cacheProfileContextKey{}, a.profiles)
	public = context.WithValue(public, pageCacheScopeKey{}, a)
	if a.config.localeScope != nil {
		public = context.WithValue(public, localeContextKey{}, a.config.localeScope)
	}
	if capture, ok := ctx.Value(prerenderCaptureKey{}).(*prerenderCapture); ok {
		public = context.WithValue(public, prerenderCaptureKey{}, capture)
	}
	return public
}

func (a *App) publicShellRequest(input *http.Request, ctx context.Context) *http.Request {
	// Build a new request rather than cloning hidden PathValue/routing state or
	// application-mutated forms, TLS, trailers and transport fields.
	url := *input.URL
	request := &http.Request{Method: http.MethodGet, URL: &url, Header: make(http.Header), Body: http.NoBody, Host: "prerender.invalid", Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1}
	if a.config.localeScope != nil && a.config.localeScope.host != "" {
		request.Host = a.config.localeScope.host
	}
	request.URL.Host, request.URL.Scheme, request.URL.User = request.Host, "http", nil
	request.URL.Opaque = ""
	request.URL.RawQuery, request.URL.ForceQuery, request.URL.Fragment, request.URL.RawFragment = "", false, "", ""
	request.RequestURI = request.URL.RequestURI()
	return request.WithContext(ctx)
}

func (a *App) loadPartialPage(c *Context, route Route, w http.ResponseWriter, span *telemetry.Span) (Page, int) {
	logical, err := staticPath(route.Pattern, Params(c.Params))
	if err != nil || route.Prerender.Fallback == FallbackNotFound && !a.prerenderKnown[logical] {
		return a.renderRouteNotFound(c, route)
	}
	// Only a public shell is shared. Every final representation can contain
	// request-specific output and must stay outside browser/CDN page reuse.
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Nextgo-Partial", "1")
	clearNavigationReuse(w.Header())
	prefetch := c.Request.Header.Get("X-Nextgo-Navigate") == "1" && c.Request.Header.Get(navigation.PurposeHeader) == "prefetch"
	if prefetch && !shellProtocol(c.Request) {
		return Page{Stream: true}, 200
	}
	if a.config.Dev || route.Cache == nil || cache.Bypassed(c.Context) || ruleSensitive(c.Context) || scripts.Nonce(c.Context) != "" || c.Request.Body != nil && c.Request.Body != http.NoBody {
		if prefetch {
			return Page{Stream: true}, 200
		}
		span.Cache("bypass")
		page, status := a.render(c, route)
		if status == 200 {
			page.Stream = true
		}
		return page, status
	}
	input := a.publicShellRequest(c.Request, c.Context)
	params := maps.Clone(c.Params)
	loader := func(ctx context.Context) (cachedPage, error) {
		return a.snapshotPage(ctx, input, params, nil, route)
	}
	life, _ := a.cacheLifetime(route.Cache)
	var value cachedPage
	var info cache.Info
	started := time.Now()
	if route.Cache.Shared && (len(prerenderDecodedPath(logical)) > 512 || !utf8.ValidString(prerenderDecodedPath(logical))) {
		// A valid route may exceed the shared authority's path-label bound.
		// Render its public shell once without storing either shell or regions.
		value, err = loader(c.Context)
	} else if route.Cache.Shared {
		value, info, err = a.lookupSharedPage(c, route, loader, life)
	} else {
		value, info, err = cache.Lookup(c.Context, a.config.Cache, a.prerenderKey(logical), cache.Options{
			Lifetime: life, ScheduleRefresh: CacheRefresh(c.Context), KeepStaleOnError: keepPageSnapshot,
			Tags: route.Cache.Tags, Paths: []string{prerenderDecodedPath(logical)},
		}, loader)
	}
	span.Cache(string(info.State))
	if err != nil {
		var uncached *uncacheable
		if errors.As(err, &uncached) {
			if uncached.snapshot == nil {
				return uncached.page, uncached.status
			}
			value = *uncached.snapshot
		} else {
			a.config.Logger.Printf("partial shell: %v", err)
			return errorPage(), 500
		}
	}
	page, err := materializePage(value, route)
	if err != nil {
		a.config.Logger.Printf("partial shell: %v", err)
		return errorPage(), 500
	}
	if shellProtocol(c.Request) {
		token, err := a.partialShellID(route, logical, value)
		if err != nil {
			a.config.Logger.Printf("partial shell identity: %v", err)
			return errorPage(), 500
		}
		page.partial = &partialResponse{token: token, prefetch: prefetch, reuseUntil: time.Now().Add(shellReuseFor(info, time.Since(started)))}
		if prefetch {
			page.Body, page.Stream = renderedHTML(value.HTML), false
		}
	}
	w.Header().Add("Vary", navigation.ShellProtocolHeader+", "+navigation.ShellHeader)
	w.Header().Set("X-Nextgo-Partial", "1")
	w.Header().Set("X-Nextgo-Cache", string(info.State))
	source := "runtime"
	if value.Prerendered {
		source = "build"
	}
	w.Header().Set("X-Nextgo-Prerender", source)
	return page, 200
}
