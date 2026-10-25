package nextgo

import (
	"context"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/Suhaibinator/Next.go/internal/paths"
	"github.com/Suhaibinator/Next.go/internal/publicorigin"
	"github.com/Suhaibinator/Next.go/internal/releaseassets"
	"github.com/Suhaibinator/Next.go/internal/zonecontract"
)

// ZoneReleaseConfig binds one independently deployed application to a fixed
// public identity. BackendHosts are exact transport authorities accepted by
// this endpoint; forwarded headers never establish identity or origin.
type ZoneReleaseConfig struct {
	Zone         ZoneConfig
	Release      ZoneReleaseSpec
	BackendHosts []string
}

// ZoneRelease owns one application and its frozen versioned asset tree. Private
// expectation headers detect a mismatched ingress deployment; they do not
// authenticate ingress or replace network policy and application authorization.
type ZoneRelease struct {
	app      *App
	spec     ZoneReleaseSpec
	backends map[string]bool
	closed   atomic.Bool
}

func NewZoneRelease(ctx context.Context, declaration ZoneReleaseConfig) (*ZoneRelease, error) {
	if ctx == nil {
		return nil, fmt.Errorf("nextgo: zone release requires a startup context")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	spec, err := zonecontract.NormalizeReleaseSpec(declaration.Release)
	if err != nil {
		return nil, err
	}
	zone := declaration.Zone
	host, err := paths.CanonicalHost(zone.Host)
	if err != nil || zone.ID != spec.Zone.ID || host != spec.Zone.Host {
		return nil, fmt.Errorf("nextgo: zone release does not match its declared owner")
	}
	if !validZoneIdentifier(zone.ID, 64) || zone.Tenant != "" && !validZoneIdentifier(zone.Tenant, 128) {
		return nil, fmt.Errorf("nextgo: invalid zone release identity")
	}
	if len(declaration.BackendHosts) == 0 || len(declaration.BackendHosts) > 32 {
		return nil, fmt.Errorf("nextgo: zone release requires 1 to 32 exact backend hosts")
	}
	backends, total := map[string]bool{}, 0
	for _, raw := range declaration.BackendHosts {
		host, err := paths.CanonicalHost(raw)
		total += len(raw)
		if err != nil || backends[host] || total > 4096 {
			return nil, fmt.Errorf("nextgo: invalid or duplicate zone release backend host")
		}
		backends[strings.Clone(host)] = true
	}
	c := zone.Config
	if c.BasePath != "" && c.BasePath != spec.Zone.BasePath || c.BuildID != "" && c.BuildID != spec.RuntimeID || c.AssetPrefix != "" && c.AssetPrefix != spec.AssetPrefix {
		return nil, fmt.Errorf("nextgo: zone release configuration changes declared paths or runtime")
	}
	c.BasePath, c.AssetPrefix, c.BuildID = spec.Zone.BasePath, spec.AssetPrefix, spec.RuntimeID
	c.DocumentPaths = append(slices.Clone(c.DocumentPaths), spec.DocumentPaths...)
	if err := normalizeConfigPhase(&c); err != nil {
		return nil, err
	}
	if err := validateReleaseFeatures(c); err != nil {
		return nil, err
	}
	info := ZoneInfo{ID: zone.ID, Tenant: zone.Tenant, Host: spec.Zone.Host, BasePath: spec.Zone.BasePath}
	if err := setConfigZone(&c, info); err != nil {
		return nil, err
	}
	origin, err := publicorigin.Parse(spec.PublicOrigin)
	if err != nil {
		return nil, err
	}
	c.publicOrigin = origin
	zone.Config = c
	if err := configureZone(&zone); err != nil {
		return nil, fmt.Errorf("nextgo: zone release configuration: %w", err)
	}
	c = zone.Config
	if c.Zone() != info || c.BasePath != spec.Zone.BasePath || c.AssetPrefix != spec.AssetPrefix || c.BuildID != spec.RuntimeID || c.publicOrigin != origin {
		return nil, fmt.Errorf("nextgo: zone release hook changed fixed deployment identity")
	}
	for _, path := range spec.DocumentPaths {
		if !slices.Contains(c.DocumentPaths, path) {
			return nil, fmt.Errorf("nextgo: zone release hook removed a peer document boundary")
		}
	}
	if err := validateReleaseFeatures(c); err != nil {
		return nil, err
	}
	frozen, digest, err := releaseassets.Freeze(ctx, c.Assets, spec.RuntimeID)
	if err != nil {
		return nil, err
	}
	if digest != spec.AssetsSHA256 {
		return nil, fmt.Errorf("nextgo: zone release asset identity differs from its artifact")
	}
	c.Assets = frozen
	app, err := constructZone(ctx, c)
	if err != nil {
		return nil, err
	}
	return &ZoneRelease{app: app, spec: spec, backends: backends}, nil
}

func validateReleaseFeatures(c Config) error {
	if c.Phase != PhaseProduction || c.Dev || c.PrerenderBuildMode || c.PrerenderFS != nil {
		return fmt.Errorf("nextgo: independent zone releases require ordinary production execution without prerender artifacts")
	}
	if c.Images != nil || c.Locales != nil || c.MetadataFiles.Static != nil || c.MetadataFiles.Robots != nil || c.MetadataFiles.Sitemap != nil || c.MetadataFiles.Manifest != nil {
		return fmt.Errorf("nextgo: independent zone release v1 requires pinned public/runtime assets; images, locales and managed metadata are not supported")
	}
	for _, route := range c.Routes {
		if route.Prerender != nil {
			return fmt.Errorf("nextgo: independent zone release v1 does not support prerender policies")
		}
	}
	return nil
}

func (z *ZoneRelease) Release() ZoneReleaseSpec { return z.spec.Clone() }

func (z *ZoneRelease) Close(ctx context.Context) error {
	if z == nil || z.app == nil {
		return nil
	}
	if ctx == nil {
		return fmt.Errorf("nextgo: zone release close requires a context")
	}
	z.closed.Store(true)
	return z.app.Close(z.app.bindZoneContext(ctx))
}

func (z *ZoneRelease) FlushTelemetry(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("nextgo: zone release telemetry flush requires a context")
	}
	if z == nil || z.app == nil {
		return nil
	}
	return z.app.FlushTelemetry(z.app.bindZoneContext(ctx))
}

func (z *ZoneRelease) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if z == nil || z.app == nil || z.closed.Load() {
		zoneHTTPError(w, http.StatusServiceUnavailable, "Zone release is closed")
		return
	}
	writer := &zoneReleaseWriter{ResponseWriter: w, expected: z.spec.Expectation()}
	w = writer
	defer func() {
		if failure := recover(); failure != nil {
			panic(failure)
		}
		if !writer.wrote {
			writer.WriteHeader(http.StatusOK)
		}
	}()
	if r == nil || r.URL == nil {
		zoneHTTPError(w, http.StatusBadRequest, "Invalid zone release request")
		return
	}
	host, err := paths.CanonicalHost(r.Host)
	if err != nil {
		zoneHTTPError(w, http.StatusBadRequest, "Invalid backend authority")
		return
	}
	if !z.backends[host] {
		zoneHTTPError(w, http.StatusMisdirectedRequest, "Unconfigured backend authority")
		return
	}
	if r.URL.User != nil || r.URL.Opaque != "" || r.URL.Fragment != "" || r.URL.Scheme != "" && r.URL.Scheme != "http" && r.URL.Scheme != "https" {
		zoneHTTPError(w, http.StatusBadRequest, "Invalid request authority")
		return
	}
	if r.URL.Host != "" {
		urlHost, err := paths.CanonicalHost(r.URL.Host)
		if err != nil || urlHost != host || r.URL.Scheme == "" {
			zoneHTTPError(w, http.StatusBadRequest, "Invalid absolute request authority")
			return
		}
	}
	escaped := r.URL.EscapedPath()
	if len(escaped) > 8192 {
		zoneHTTPError(w, http.StatusBadRequest, "Invalid zone release path")
		return
	}
	if _, err := (paths.Config{}).URL(escaped); err != nil {
		zoneHTTPError(w, http.StatusBadRequest, "Invalid zone release path")
		return
	}
	if err := zonecontract.CheckExpectation(r.Header, z.spec.Expectation()); err != nil {
		zoneHTTPError(w, http.StatusConflict, "Zone release identity changed")
		return
	}
	local, inside := paths.Strip(z.spec.Zone.BasePath, escaped)
	if !inside {
		zoneHTTPError(w, http.StatusNotFound, "No zone release owns this path")
		return
	}
	for _, peer := range z.spec.DocumentPaths {
		if _, belongs := paths.Strip(peer, local); belongs {
			zoneHTTPError(w, http.StatusNotFound, "Another zone owns this path")
			return
		}
	}
	if r.Header.Get("Upgrade") != "" {
		zoneHTTPError(w, http.StatusBadRequest, "Zone release upgrades are unsupported")
		return
	}
	request := r.Clone(r.Context())
	zonecontract.StripExpectation(request.Header)
	origin := z.app.config.publicOrigin
	request.Host = origin.Host()
	request.URL.Scheme, request.URL.Host = origin.Scheme(), origin.Host()
	request = request.WithContext(bindConfigPublicOrigin(bindConfigZone(request.Context(), z.app.config), z.app.config))
	if suffix, ok := paths.Strip(z.spec.AssetPrefix, escaped); ok {
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			zoneHTTPError(w, http.StatusMethodNotAllowed, "Release assets require GET or HEAD")
			return
		}
		asset, ok := localRequest(request, z.spec.AssetPrefix)
		literalTree := strings.HasPrefix(suffix, "/assets/") || strings.HasPrefix(suffix, "/_nextgo/"+z.spec.RuntimeID+"/")
		if !ok || !literalTree || !z.assetFile(asset.URL.Path) {
			zoneHTTPError(w, http.StatusNotFound, "Release asset not found")
			return
		}
		writer.asset = true
		z.app.AssetHandler().ServeHTTP(w, asset)
		return
	}
	if zoneUnversionedReservedPath(local) {
		zoneHTTPError(w, http.StatusNotFound, "Unversioned release asset not found")
		return
	}
	z.app.ServeHTTP(w, request)
}

// Call only after literal mount and versioned asset selection. Decode to deny
// reserved-root aliases, never to admit a framework service under a new spelling.
func zoneUnversionedReservedPath(local string) bool {
	decoded, err := url.PathUnescape(local)
	if err != nil || decoded == "/assets" || strings.HasPrefix(decoded, "/assets/") {
		return true
	}
	if decoded != "/_nextgo" && !strings.HasPrefix(decoded, "/_nextgo/") {
		return false
	}
	if strings.HasPrefix(local, "/_nextgo/") {
		name, _, _ := strings.Cut(strings.TrimPrefix(local, "/_nextgo/"), "/")
		switch name {
		case "actions", "loading", "draft", "image", "events":
			return false
		}
	}
	return true
}

func (z *ZoneRelease) assetFile(local string) bool {
	var name string
	switch {
	case strings.HasPrefix(local, "/assets/"):
		name = "public/" + strings.TrimPrefix(local, "/assets/")
	case strings.HasPrefix(local, "/_nextgo/"+z.spec.RuntimeID+"/"):
		name = strings.TrimPrefix(local, "/")
	default:
		return false
	}
	info, err := fs.Stat(z.app.config.Assets, name)
	return err == nil && info.Mode().IsRegular()
}

type zoneReleaseWriter struct {
	http.ResponseWriter
	expected zonecontract.Expectation
	asset    bool
	wrote    bool
}

func (w *zoneReleaseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *zoneReleaseWriter) WriteHeader(status int) {
	if w.wrote {
		return
	}
	if status >= 200 || status == http.StatusSwitchingProtocols {
		w.wrote = true
	}
	zonecontract.SetExpectation(w.Header(), w.expected)
	if w.asset {
		w.Header().Del("Set-Cookie")
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *zoneReleaseWriter) Write(data []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(data)
}
func (w *zoneReleaseWriter) Flush() {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	_ = http.NewResponseController(w.ResponseWriter).Flush()
}
