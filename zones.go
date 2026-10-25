package nextgo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/internal/paths"
	"github.com/Suhaibinator/Next.go/internal/wirejson"
	"github.com/Suhaibinator/Next.go/metadatafiles"
	"github.com/Suhaibinator/Next.go/telemetry"
)

const MaxZones = 64
const zoneCloseTimeout = 5 * time.Second

// ZoneConfig declares one exact host and public Config.BasePath. Configure runs
// after Config.Zone is installed, before application construction or factories.
// It cannot change BasePath or the installed zone descriptor. Callbacks are
// trusted native startup code and must return promptly; they are not a sandbox.
type ZoneConfig struct {
	ID, Host, Tenant string
	Config           Config
	Configure        func(*Config) error
}

// ZoneInfo is trusted deployment selection, not an authenticated principal or
// authorization decision. Tenant is optional application-owned deployment data.
type ZoneInfo struct {
	ID, Tenant, Host, BasePath string
}

type zoneMount struct {
	info ZoneInfo
	app  *App
}

// Zones owns independently constructed applications. Exact configured hosts and
// complete literal public prefixes select a single app; its 404 never falls
// through. Applications share process authority, globals and browser origins.
// Authorize every tenant data operation separately and keep private values out
// of public caches, metadata and runtime configuration.
type Zones struct {
	mounts    []*zoneMount
	byHost    map[string][]*zoneMount
	closed    atomic.Bool
	closeOnce sync.Once
	closeDone chan struct{}
	closeErr  error
}

// NewZones freezes a bounded mount inventory, configures each application, then
// constructs it. Local cache stores and shared-cache namespaces cannot be reused
// between declarations. Arbitrary application data stores/closures remain the
// application's responsibility. This version requires assets at their app mount,
// forbids locale domains, and permits subpath locales only on leaf zones.
func NewZones(ctx context.Context, declarations []ZoneConfig) (*Zones, error) {
	if ctx == nil {
		return nil, fmt.Errorf("nextgo: zones require a startup context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(declarations) == 0 || len(declarations) > MaxZones {
		return nil, fmt.Errorf("nextgo: require 1 to %d zones", MaxZones)
	}
	declarations = slices.Clone(declarations)
	infos := make([]ZoneInfo, len(declarations))
	ids, mounts := map[string]bool{}, map[string]bool{}
	for i := range declarations {
		declaration := &declarations[i]
		if !validZoneIdentifier(declaration.ID, 64) || declaration.Tenant != "" && !validZoneIdentifier(declaration.Tenant, 128) {
			return nil, fmt.Errorf("nextgo: zone ID/tenant requires a bounded ASCII identifier")
		}
		host, err := paths.CanonicalHost(declaration.Host)
		if err != nil {
			return nil, fmt.Errorf("nextgo: zone %s requires an exact configured host", declaration.ID)
		}
		if err := (paths.Config{BasePath: declaration.Config.BasePath}).Validate(); err != nil {
			return nil, fmt.Errorf("nextgo: zone %s mount: %w", declaration.ID, err)
		}
		info := ZoneInfo{ID: strings.Clone(declaration.ID), Tenant: strings.Clone(declaration.Tenant), Host: strings.Clone(host), BasePath: strings.Clone(declaration.Config.BasePath)}
		key := host + "\x00" + info.BasePath
		if ids[info.ID] || mounts[key] {
			return nil, fmt.Errorf("nextgo: duplicate zone ID or host/mount")
		}
		ids[info.ID], mounts[key], infos[i] = true, true, info
		declaration.Config.BasePath = info.BasePath
		if err := setConfigZone(&declaration.Config, info); err != nil {
			return nil, err
		}
		if err := normalizeConfigPhase(&declaration.Config); err != nil {
			return nil, err
		}
	}
	// Basic inventory errors precede hooks; final application ownership is checked
	// afterward. Hooks may alter policy, but not the declared dispatch owner.
	for i := range declarations {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		declaration := &declarations[i]
		if err := configureZone(declaration); err != nil {
			return nil, fmt.Errorf("nextgo: zone %s configuration: %w", infos[i].ID, err)
		}
		if declaration.Config.BasePath != infos[i].BasePath || declaration.Config.Zone() != infos[i] {
			return nil, fmt.Errorf("nextgo: zone %s configuration changed its declared ownership", infos[i].ID)
		}
	}
	if err := validateZoneOwnership(declarations, infos); err != nil {
		return nil, err
	}
	group := &Zones{byHost: map[string][]*zoneMount{}}
	for i := range declarations {
		app, err := constructZone(ctx, declarations[i].Config)
		if err != nil {
			// Failed startup must not retain previously constructed app workers.
			// Cleanup has its own bounded context even when startup was canceled.
			cleanup, cancel := context.WithTimeout(context.Background(), zoneCloseTimeout)
			closeErr := group.Close(cleanup)
			cancel()
			return nil, errors.Join(fmt.Errorf("nextgo: zone %s startup: %w", infos[i].ID, err), closeErr)
		}
		mount := &zoneMount{info: infos[i], app: app}
		group.mounts = append(group.mounts, mount)
		group.byHost[infos[i].Host] = append(group.byHost[infos[i].Host], mount)
	}
	for _, hostMounts := range group.byHost {
		slices.SortFunc(hostMounts, func(a, b *zoneMount) int { return len(b.info.BasePath) - len(a.info.BasePath) })
	}
	return group, nil
}

func validZoneIdentifier(value string, limit int) bool {
	if value == "" || len(value) > limit || value == "." || value == ".." {
		return false
	}
	for i, c := range value {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || i > 0 && strings.ContainsRune("._-", c)) {
			return false
		}
	}
	return true
}

func configureZone(declaration *ZoneConfig) (err error) {
	defer func() {
		if cause := recover(); cause != nil {
			err = fmt.Errorf("configuration hook panicked (%T)", cause)
		}
	}()
	if declaration.Configure != nil {
		return declaration.Configure(&declaration.Config)
	}
	return nil
}

func constructZone(ctx context.Context, config Config) (app *App, err error) {
	defer func() {
		if cause := recover(); cause != nil {
			app, err = nil, fmt.Errorf("application startup panicked (%T)", cause)
		}
	}()
	return NewContext(ctx, config)
}

func validateZoneOwnership(declarations []ZoneConfig, infos []ZoneInfo) error {
	localStores := map[*cache.Store]string{}
	sharedNamespaces := map[string]string{}
	tracers := map[*telemetry.Tracer]string{}
	for i := range declarations {
		config, info := &declarations[i].Config, infos[i]
		if config.AssetPrefix != "" && config.AssetPrefix != info.BasePath && !(info.BasePath == "" && config.AssetPrefix == "/") {
			return fmt.Errorf("nextgo: zone %s assets must use its declared application mount", info.ID)
		}
		if config.Locales != nil && len(config.Locales.Domains) > 0 {
			return fmt.Errorf("nextgo: zone %s cannot also declare locale domains", info.ID)
		}
		if config.Cache != nil {
			if prior := localStores[config.Cache]; prior != "" {
				return fmt.Errorf("nextgo: zones %s and %s reuse a local cache store", prior, info.ID)
			}
			localStores[config.Cache] = info.ID
		}
		if identity := config.SharedCache.NamespaceIdentity(); identity != "" {
			if prior := sharedNamespaces[identity]; prior != "" {
				return fmt.Errorf("nextgo: zones %s and %s reuse a shared-cache namespace", prior, info.ID)
			}
			sharedNamespaces[identity] = info.ID
		}
		if config.Tracing != nil {
			if prior := tracers[config.Tracing]; prior != "" {
				return fmt.Errorf("nextgo: zones %s and %s reuse a tracer lifecycle", prior, info.ID)
			}
			tracers[config.Tracing] = info.ID
		}
		config.DocumentPaths = slices.Clone(config.DocumentPaths)
		for j, child := range infos {
			if i == j || info.Host != child.Host {
				continue
			}
			logical, inside := paths.Strip(info.BasePath, child.BasePath)
			if !inside {
				continue
			}
			if config.Locales != nil {
				return fmt.Errorf("nextgo: zone %s cannot combine subpath locales with nested zones", info.ID)
			}
			first, _, _ := strings.Cut(strings.TrimPrefix(logical, "/"), "/")
			if first == "api" || first == "assets" || first == "_nextgo" || metadatafiles.IsName(first) {
				return fmt.Errorf("nextgo: zone %s enters reserved endpoint ownership of %s", child.ID, info.ID)
			}
			for _, route := range config.Routes {
				if route.API != nil && zoneRouteIntersects(route.Pattern, logical) {
					return fmt.Errorf("nextgo: zone %s conflicts with API ownership in %s", child.ID, info.ID)
				}
			}
			for pattern := range config.Handlers {
				if pattern == logical || strings.HasPrefix(pattern, logical+"/") || strings.HasSuffix(pattern, "/") && strings.HasPrefix(logical+"/", pattern) {
					return fmt.Errorf("nextgo: zone %s conflicts with handler ownership in %s", child.ID, info.ID)
				}
			}
			if !slices.Contains(config.DocumentPaths, logical) {
				config.DocumentPaths = append(config.DocumentPaths, logical)
			}
		}
	}
	return nil
}

// The subtree is a validated literal prefix. A route intersects it when every
// shared leading segment can match and the route reaches or enters the subtree.
func zoneRouteIntersects(pattern, subtree string) bool {
	routeParts, subtreeParts := segments(pattern), segments(subtree)
	for i, part := range routeParts {
		if i == len(subtreeParts) || strings.HasPrefix(part, "*") {
			return true
		}
		if !strings.HasPrefix(part, ":") && part != subtreeParts[i] {
			return false
		}
	}
	return len(routeParts) == len(subtreeParts)
}

// ServeHTTP checks authority and literal path ownership before any application
// middleware, assets, metadata, actions or loaders. Forwarded headers never
// select a zone; the original URL, Host and TLS state reach the selected App.
func (z *Zones) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if z == nil || z.closed.Load() {
		zoneHTTPError(w, http.StatusServiceUnavailable, "Zone applications are closed")
		return
	}
	if r == nil || r.URL == nil {
		zoneHTTPError(w, http.StatusBadRequest, "Invalid zone request")
		return
	}
	host, err := paths.CanonicalHost(r.Host)
	if err != nil {
		zoneHTTPError(w, http.StatusBadRequest, "Invalid zone host")
		return
	}
	hostMounts := z.byHost[host]
	if len(hostMounts) == 0 {
		zoneHTTPError(w, http.StatusMisdirectedRequest, "Unconfigured zone host")
		return
	}
	if r.URL.User != nil || r.URL.Scheme != "" && r.URL.Scheme != "http" && r.URL.Scheme != "https" {
		zoneHTTPError(w, http.StatusBadRequest, "Invalid zone request authority")
		return
	}
	if r.URL.Host != "" {
		urlHost, hostErr := paths.CanonicalHost(r.URL.Host)
		if hostErr != nil || urlHost != host || r.URL.Scheme != "http" && r.URL.Scheme != "https" || r.URL.User != nil {
			zoneHTTPError(w, http.StatusBadRequest, "Invalid zone request authority")
			return
		}
	}
	escaped := r.URL.EscapedPath()
	if len(escaped) > 8192 || r.URL.Opaque != "" || r.URL.Fragment != "" {
		zoneHTTPError(w, http.StatusBadRequest, "Invalid zone path")
		return
	}
	if _, err := (paths.Config{}).URL(escaped); err != nil {
		zoneHTTPError(w, http.StatusBadRequest, "Invalid zone path")
		return
	}
	for _, mount := range hostMounts {
		if _, inside := paths.Strip(mount.info.BasePath, escaped); inside {
			mount.app.ServeHTTP(w, r.WithContext(withZoneContext(r.Context(), mount.info)))
			return
		}
	}
	zoneHTTPError(w, http.StatusNotFound, "No zone owns this path")
}

func zoneHTTPError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.Error(w, message, status)
}

// ParseZoneHosts validates explicit operator JSON overrides. It reads neither
// environment nor requests. Empty input keeps declared hosts; returned entries
// contain canonical exact authorities only, and must name known zone IDs.
func ParseZoneHosts(value string, knownIDs []string) (map[string]string, error) {
	if len(knownIDs) == 0 || len(knownIDs) > MaxZones {
		return nil, fmt.Errorf("nextgo: host overrides require 1 to %d known zone IDs", MaxZones)
	}
	known := make(map[string]bool, len(knownIDs))
	for _, id := range knownIDs {
		if !validZoneIdentifier(id, 64) || known[id] {
			return nil, fmt.Errorf("nextgo: invalid or repeated known zone ID")
		}
		known[id] = true
	}
	if len(value) > 64<<10 || !utf8.ValidString(value) {
		return nil, fmt.Errorf("nextgo: zone host overrides exceed JSON byte/encoding limits")
	}
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	data := []byte(value)
	if err := wirejson.Unique(data); err != nil {
		return nil, fmt.Errorf("nextgo: invalid or duplicate zone host override JSON")
	}
	var raw map[string]json.RawMessage
	if err := wirejson.Strict(data, &raw); err != nil || raw == nil || len(raw) > MaxZones {
		return nil, fmt.Errorf("nextgo: zone host overrides require a bounded JSON object")
	}
	result := make(map[string]string, len(raw))
	for id, encoded := range raw {
		if !known[id] {
			return nil, fmt.Errorf("nextgo: host override names an unknown zone")
		}
		var host string
		if err := json.Unmarshal(encoded, &host); err != nil {
			return nil, fmt.Errorf("nextgo: host override requires an exact host string")
		}
		canonical, err := paths.CanonicalHost(host)
		if err != nil {
			return nil, fmt.Errorf("nextgo: host override requires an exact configured authority")
		}
		result[strings.Clone(id)] = strings.Clone(canonical)
	}
	return result, nil
}

// FlushTelemetry forwards the outer platform lifecycle to every child app.
// In particular Lambda adapters call this after accepted completion work, before
// freezing the worker. Exporter hooks remain cooperative native application code.
func (z *Zones) FlushTelemetry(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("nextgo: zone telemetry flush requires a context")
	}
	if z == nil {
		return nil
	}
	var result error
	for _, mount := range z.mounts {
		if err := ctx.Err(); err != nil {
			return errors.Join(result, err)
		}
		result = errors.Join(result, mount.app.FlushTelemetry(mount.app.bindZoneContext(ctx)))
	}
	return result
}

// Close rejects further requests, stops all app completion schedulers and closes
// native resources concurrently, once. Its total budget is at most five seconds
// or the caller's earlier deadline. Uncooperative trusted hooks may outlive that
// deadline, but cannot create another close attempt or block the caller forever.
func (z *Zones) Close(ctx context.Context) error {
	if z == nil {
		return nil
	}
	if ctx == nil {
		return fmt.Errorf("nextgo: zone close requires a context")
	}
	z.closeOnce.Do(func() {
		z.closeDone = make(chan struct{})
		z.closed.Store(true)
		work, cancel := context.WithTimeout(ctx, zoneCloseTimeout)
		go func() {
			defer cancel()
			defer close(z.closeDone)
			results := make(chan error, len(z.mounts))
			for _, mount := range z.mounts {
				go func() {
					var err error
					defer func() {
						if cause := recover(); cause != nil {
							err = fmt.Errorf("nextgo: zone close panicked (%T)", cause)
						}
						results <- err
					}()
					err = mount.app.Close(mount.app.bindZoneContext(work))
				}()
			}
			for range z.mounts {
				select {
				case err := <-results:
					z.closeErr = errors.Join(z.closeErr, err)
				case <-work.Done():
					z.closeErr = errors.Join(z.closeErr, work.Err())
					return
				}
			}
		}()
	})
	select {
	case <-z.closeDone:
		return z.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
