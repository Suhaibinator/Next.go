package nextgo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/cache/shared"
)

type pageCacheScopeKey struct{}
type pageCacheReadOnlyKey struct{}

var ErrCacheScope = errors.New("nextgo: cache invalidation requires an application scope")

func sharedPagePolicy(p *CachePolicy, life *cache.Lifetime, path string) shared.Policy {
	policy := shared.Policy{FreshFor: p.TTL, ExpireAfter: p.TTL, Tags: p.Tags}
	if life != nil {
		policy.FreshFor = life.Revalidate
		policy.ExpireAfter = life.Expire
	}
	if path != "" {
		policy.Paths = []string{path}
	}
	return policy
}
func (a *App) validateSharedPages() error {
	for _, route := range a.routes {
		p := route.Cache
		if p == nil || !p.Shared {
			continue
		}
		if a.config.SharedCache.ScopeID() == "" {
			return fmt.Errorf("route %s: shared page caching requires Config.SharedCache", route.Pattern)
		}
		if !a.config.Dev && a.config.BuildID == "development" {
			return fmt.Errorf("route %s: shared page caching requires an explicit code/build ID", route.Pattern)
		}
		life, _ := a.cacheLifetime(p)
		if route.API != nil || route.Prerender != nil && !route.Prerender.Shared || life == nil && p.TTL <= 0 {
			return fmt.Errorf("route %s: shared caching requires an ordinary page and active TTL/lifetime/profile", route.Pattern)
		}
		if err := sharedPagePolicy(p, life, "").Validate(); err != nil {
			return fmt.Errorf("route %s: invalid shared page policy: %w", route.Pattern, err)
		}
	}
	return nil
}

func (a *App) sharedPageBinding(route Route, host, uri string) string {
	// JSON strings are unambiguous after the caller's UTF-8 admission check.
	data, _ := json.Marshal([]string{"nextgo.page.v1", a.config.BuildID, a.configurationID, route.Pattern, host, uri})
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:])
}
func (a *App) lookupSharedPage(c *Context, route Route, inputLoader func(context.Context) (cachedPage, error), life *cache.Lifetime) (cachedPage, cache.Info, error) {
	binding := a.sharedPageBinding(route, c.Request.Host, c.Request.URL.RequestURI())
	path := c.Request.URL.Path
	if route.Prerender != nil {
		logical, err := staticPath(route.Pattern, Params(c.Params))
		if err != nil {
			return cachedPage{}, cache.Info{}, err
		}
		binding = a.sharedPrerenderBinding(route, logical)
		path = prerenderDecodedPath(logical)
	}
	options := shared.LoadOptions{KeepStaleOnError: keepPageSnapshot}
	if life != nil {
		options.ScheduleRefresh = CacheRefresh(c.Context)
	}
	value, info, err := shared.Lookup(c.Context, a.config.SharedCache, binding, sharedPagePolicy(route.Cache, life, path), func(ctx context.Context) (sharedPageSnapshot, error) {
		page, err := inputLoader(ctx)
		if err != nil {
			return sharedPageSnapshot{}, err
		}
		snapshot, err := encodeSharedPage(page, binding)
		if err != nil {
			// The ordinary renderer already validated this frozen result. A
			// stricter shared-codec admission limit must not break the page.
			return sharedPageSnapshot{}, uncachedSharedPage(page)
		}
		encoded, err := json.Marshal(snapshot)
		if err != nil {
			return sharedPageSnapshot{}, err
		}
		if len(encoded) > shared.MaxValueBytes {
			// Reuse the already frozen render. Cache admission must not turn a
			// valid large response into an error or invoke the page twice.
			return sharedPageSnapshot{}, uncachedSharedPage(page)
		}
		return snapshot, nil
	}, options)
	if err != nil {
		return cachedPage{}, cache.Info{}, err
	}
	page, err := decodeSharedPage(value, binding)
	if err != nil {
		return cachedPage{}, cache.Info{}, err
	}
	return page, info, nil
}
func uncachedSharedPage(page cachedPage) error {
	fresh := page.Page
	fresh.Body = renderedHTML(page.HTML)
	return &uncacheable{fresh, 200}
}
func keepPageSnapshot(err error) bool {
	if errors.Is(err, ErrPublicCacheResponse) {
		return false
	}
	var failed *uncacheable
	return !errors.As(err, &failed) || failed.status >= 500
}

// ExpireCache acknowledges expiration of configured local/shared cache stores.
// Invoke after backing commits. The stores are not a transaction: local entries
// are expired even when the shared call fails. Retrying is safe. Browser/CDN
// caches and independently configured data stores require their own policy.
// Key selectors use literal store keys; page callers normally use tag/path/prefix.
func (a *App) ExpireCache(ctx context.Context, selector shared.Selector) error {
	return a.changePageCaches(ctx, selector, 0)
}

// MarkCacheStale is ExpireCache with a last-good window. Zero strictly expires;
// shared.Never adds no deadline. Existing ages/deadlines are never extended.
func (a *App) MarkCacheStale(ctx context.Context, selector shared.Selector, window time.Duration) error {
	return a.changePageCaches(ctx, selector, window)
}
func (a *App) changePageCaches(ctx context.Context, selector shared.Selector, window time.Duration) error {
	if a == nil || a.config.Cache == nil || ctx == nil {
		return ErrCacheScope
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if ctx.Value(pageCacheReadOnlyKey{}) == true {
		return ErrPublicCacheResponse
	}
	if err := selector.Validate(); err != nil {
		return err
	}
	if window != 0 && window != shared.Never && (window < 0 || window > 365*24*time.Hour) {
		return shared.ErrInvalid
	}
	local := a.config.Cache
	var localErr error
	if window == 0 {
		switch selector.Kind {
		case "key":
			local.ExpireKey(selector.Value)
		case "tag":
			local.ExpireTag(selector.Value)
		case "path":
			local.ExpirePath(selector.Value)
		case "prefix":
			local.ExpirePathPrefix(selector.Value)
		case "all":
			local.ExpireAll()
		}
	} else {
		switch selector.Kind {
		case "key":
			_, localErr = local.MarkStaleKey(selector.Value, window)
		case "tag":
			_, localErr = local.MarkStaleTag(selector.Value, window)
		case "path":
			_, localErr = local.MarkStalePath(selector.Value, window)
		case "prefix":
			_, localErr = local.MarkStalePathPrefix(selector.Value, window)
		case "all":
			_, localErr = local.MarkStaleAll(window)
		}
	}
	var remoteErr error
	if a.config.SharedCache != nil {
		if window == 0 {
			_, remoteErr = a.config.SharedCache.Expire(ctx, selector)
		} else {
			_, remoteErr = a.config.SharedCache.MarkStale(ctx, selector, window)
		}
	}
	return errors.Join(localErr, remoteErr)
}

// ExpireCache resolves the application from a page/API/action/completion context.
func ExpireCache(ctx context.Context, selector shared.Selector) error {
	if ctx == nil {
		return ErrCacheScope
	}
	app, _ := ctx.Value(pageCacheScopeKey{}).(*App)
	return app.ExpireCache(ctx, selector)
}

// MarkCacheStale resolves the configured stores from the current application.
func MarkCacheStale(ctx context.Context, selector shared.Selector, window time.Duration) error {
	if ctx == nil {
		return ErrCacheScope
	}
	app, _ := ctx.Value(pageCacheScopeKey{}).(*App)
	return app.MarkCacheStale(ctx, selector, window)
}

func sharedPageInputsValid(c *Context) bool {
	return len(c.Request.URL.Path) <= 512 && utf8.ValidString(c.Request.Host) && utf8.ValidString(c.Request.URL.Path) && utf8.ValidString(c.Request.URL.RequestURI())
}
