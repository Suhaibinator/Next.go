//go:build !js

package nextgo

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"mime"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/internal/paths"
	"github.com/Suhaibinator/Next.go/internal/postresponse"
	"github.com/Suhaibinator/Next.go/internal/publicorigin"
	"github.com/Suhaibinator/Next.go/offline"
	"github.com/Suhaibinator/Next.go/scripts"
)

// ErrOfflineAdmission means an explicitly selected representation could not be
// proven eligible for saved anonymous public use. It never describes user data.
var ErrOfflineAdmission = errors.New("nextgo: anonymous offline preparation refused")

// PreparedOfflineDocument owns one complete HTML representation. Fallback is an
// inert title/description document, deliberately not the route's body or head.
type PreparedOfflineDocument struct {
	Path, URL, ContentType, SHA256 string
	MaxAge                         time.Duration
	HTML                           []byte
	Fallback                       bool
}

// PreparedOffline contains native-approved public bytes only. It does not prove
// the hydration asset graph, create a catalog, install a worker or persist data.
// Identity binds the normalized policy, build and application configuration; it
// is separate from offline.Catalog's generation and policy identity.
type PreparedOffline struct {
	policy                   *offline.Policy
	build, configuration, id string
	documents                []PreparedOfflineDocument
}

func (p *PreparedOffline) Policy() offline.Config {
	if p == nil {
		return offline.Config{}
	}
	return p.policy.Config()
}
func (p *PreparedOffline) BuildID() string {
	if p == nil {
		return ""
	}
	return p.build
}
func (p *PreparedOffline) ConfigurationID() string {
	if p == nil {
		return ""
	}
	return p.configuration
}
func (p *PreparedOffline) Identity() string {
	if p == nil {
		return ""
	}
	return p.id
}
func (p *PreparedOffline) Admission() string {
	if p == nil || p.policy == nil || p.id == "" {
		return ""
	}
	return offline.NativeAdmissionV1
}
func (p *PreparedOffline) Documents() []PreparedOfflineDocument {
	if p == nil {
		return nil
	}
	out := slices.Clone(p.documents)
	for i := range out {
		out[i].HTML = bytes.Clone(out[i].HTML)
	}
	return out
}

type offlineCaptureKey struct{}
type offlineCapture struct {
	app                         *App
	path, pattern, host, scheme string
	fallback                    bool
	seen                        bool
	err                         error
	title, description          string
	fallbackHTML                []byte
	options                     *offlinePrepareOptions
}

// PrepareOffline constructs strictly anonymous GETs from trusted native policy,
// executes the complete middleware chain and admits each final bounded result.
// No incoming request or caller context values are reused. Cancellation/deadline
// are retained. Routes must explicitly declare ordinary public Cache policies.
// Public declaration is required: cookie absence alone never grants admission.
//
// V1 excludes development/nonproduction, locales, zones, alternate asset mounts,
// prerendering, streaming/composition and early scripts. Fallback uses only the
// approved title/description as escaped text, omitting app body/head and runtime.
// A failure returns no partial preparation. Arbitrary native code is trusted;
// cancellation cannot forcibly stop a middleware or loader that ignores it.
func (a *App) PrepareOffline(ctx context.Context, config offline.Config) (*PreparedOffline, error) {
	return a.prepareOffline(ctx, config, nil)
}

func (a *App) prepareOffline(ctx context.Context, config offline.Config, options *offlinePrepareOptions) (prepared *PreparedOffline, err error) {
	if ctx == nil || a == nil {
		return nil, ErrOfflineAdmission
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	defer func() {
		if recover() != nil {
			prepared, err = nil, ErrOfflineAdmission
		}
	}()
	policy, err := offline.Normalize(config)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid policy", ErrOfflineAdmission)
	}
	config = policy.Config()
	if a.config.Dev || a.config.Phase != PhaseProduction || a.config.PrerenderBuildMode || a.config.Draft != nil || a.locales != nil || a.config.localeScope != nil || a.config.Zone().ID != "" || a.paths.AssetPrefix != "" || a.paths.TrailingSlash || a.paths.BasePath != config.BasePath || len(a.config.EarlyScripts) != 0 || len(a.config.Rewrites) != 0 || len(a.config.Redirects) != 0 || !offlineHex(a.config.BuildID, 20) || !offlineHex(a.configurationID, 64) {
		return nil, ErrOfflineAdmission
	}
	if origin := a.config.publicOrigin.String(); origin != "" && origin != config.Origin {
		return nil, ErrOfflineAdmission
	}
	origin, _ := url.Parse(config.Origin)
	// Preflight every path before invoking any middleware/application callbacks.
	selected := make([]Route, len(config.Pages))
	for i, page := range config.Pages {
		public := config.BasePath + page.Path
		for path := range a.config.Handlers {
			if path == page.Path || path == public || strings.HasSuffix(path, "/") && (strings.HasPrefix(public, path) || strings.HasPrefix(page.Path, path)) {
				return nil, ErrOfflineAdmission
			}
		}
		for _, route := range a.routes {
			if _, ok := match(route.Pattern, page.Path); !ok {
				continue
			}
			if !a.offlineRoutePublic(route) {
				return nil, ErrOfflineAdmission
			}
			selected[i] = route
			break
		}
		if selected[i].Page == nil {
			return nil, ErrOfflineAdmission
		}
	}
	binding, _ := json.Marshal(struct {
		Domain               string
		Policy               offline.Config
		Build, Configuration string
	}{"nextgo.offline.native.v1", config, a.config.BuildID, a.configurationID})
	sum := sha256.Sum256(binding)
	prepared = &PreparedOffline{policy: policy, build: strings.Clone(a.config.BuildID), configuration: strings.Clone(a.configurationID), id: hex.EncodeToString(sum[:])}
	var total int64
	for i, page := range config.Pages {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		capture := &offlineCapture{app: a, path: page.Path, pattern: selected[i].Pattern, host: origin.Host, scheme: origin.Scheme, fallback: page.Path == config.Fallback, options: options}
		// shellContext forwards Done/Err/Deadline but hides all caller values.
		publicCtx := context.WithValue(postresponse.BlockRegistration(shellContext{ctx}, ErrOfflineAdmission), offlineCaptureKey{}, capture)
		documentURL, urlErr := policy.DocumentURL(page.Path)
		if urlErr != nil {
			return nil, ErrOfflineAdmission
		}
		r, requestErr := http.NewRequestWithContext(publicCtx, http.MethodGet, documentURL, nil)
		if requestErr != nil {
			return nil, ErrOfflineAdmission
		}
		r.Body, r.Header = http.NoBody, make(http.Header)
		response := &offlineWriter{header: make(http.Header), limit: config.Budget.DocumentBytes}
		a.ServeHTTP(response, r)
		response.inspect()
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if capture.fallback && !bytes.Equal(response.body.Bytes(), capture.fallbackHTML) {
			return nil, ErrOfflineAdmission
		}
		if capture.err != nil || !capture.seen || response.rejected || response.status != http.StatusOK || len(response.body.Bytes()) == 0 {
			return nil, ErrOfflineAdmission
		}
		media, params, mediaErr := mime.ParseMediaType(response.header.Get("Content-Type"))
		if mediaErr != nil || media != "text/html" || !strings.EqualFold(params["charset"], "utf-8") {
			return nil, ErrOfflineAdmission
		}
		body := bytes.Clone(response.body.Bytes())
		total += int64(len(body))
		if total > config.Budget.GenerationBytes || total > config.Budget.TotalBytes {
			return nil, ErrOfflineAdmission
		}
		digest := sha256.Sum256(body)
		prepared.documents = append(prepared.documents, PreparedOfflineDocument{Path: strings.Clone(page.Path), URL: documentURL, ContentType: "text/html; charset=utf-8", SHA256: hex.EncodeToString(digest[:]), MaxAge: page.MaxAge, HTML: body, Fallback: capture.fallback})
	}
	return prepared, nil
}

func offlineHex(value string, size int) bool {
	if len(value) != size {
		return false
	}
	for _, c := range value {
		if c < '0' || c > '9' {
			if c < 'a' || c > 'f' {
				return false
			}
		}
	}
	return true
}
func (a *App) offlineRoutePublic(route Route) bool {
	if route.Page == nil || route.API != nil || route.Cache == nil || route.Prerender != nil || len(route.Slots) != 0 || hasPrimaries(route) || len(route.Regions) != 0 || len(a.intercepts) != 0 {
		return false
	}
	life, _ := a.cacheLifetime(route.Cache)
	return route.Cache.TTL > 0 || life != nil
}

func (a *App) captureOffline(c *Context, route Route, w http.ResponseWriter, capture *offlineCapture) (Page, int) {
	if !a.offlineRequestValid(c.Request, capture) || capture.seen || capture.pattern != route.Pattern || cache.Bypassed(c.Context) || ruleSensitive(c.Context) || scripts.Nonce(c.Context) != "" || !a.offlineRoutePublic(route) {
		capture.err = ErrOfflineAdmission
		return errorPage(), 500
	}
	capture.seen = true
	// Inspect middleware safety flags above, then discard its auth/session values
	// and hidden request fields for the explicitly public application render.
	publicCtx := context.WithValue(a.publicShellContext(c.Context), offlineCaptureKey{}, capture)
	origin, err := publicorigin.Parse(capture.scheme + "://" + capture.host)
	if err != nil {
		capture.err = ErrOfflineAdmission
		return errorPage(), 500
	}
	publicCtx = publicorigin.WithContext(publicCtx, origin)
	publicCtx = postresponse.BlockRegistration(publicCtx, ErrOfflineAdmission)
	request, err := http.NewRequestWithContext(publicCtx, http.MethodGet, capture.scheme+"://"+capture.host+capture.path, nil)
	if err != nil {
		capture.err = ErrOfflineAdmission
		return errorPage(), 500
	}
	request.Body, request.Header, request.RequestURI = http.NoBody, make(http.Header), capture.path
	value, err := a.snapshotPage(publicCtx, request, c.Params, make(url.Values), route)
	if err != nil || len(value.Regions) != 0 {
		capture.err = ErrOfflineAdmission
		return errorPage(), 500
	}
	capture.title, capture.description = value.Page.Title, value.Page.Description
	page := value.Page
	page.Body = renderedHTML(value.HTML)
	if w.Header().Get("Cache-Control") == "" {
		w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
	}
	return page, http.StatusOK
}

func offlineCaptureFor(r *http.Request) *offlineCapture {
	capture, _ := r.Context().Value(offlineCaptureKey{}).(*offlineCapture)
	return capture
}

var offlineFallbackDocument = template.Must(template.New("offline-fallback").Parse(`<!doctype html><html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>{{.Title}}</title></head><body><main><h1>{{.Title}}</h1><p>{{.Description}}</p><p>This saved public page is available while offline. Reconnect to load current information.</p></main></body></html>`))

func respondOfflineFallback(w http.ResponseWriter, capture *offlineCapture, status int) {
	if status != http.StatusOK || !capture.seen || capture.err != nil {
		http.Error(w, "Offline preparation refused", 500)
		return
	}
	title, description := capture.title, capture.description
	if title == "" {
		title = "Offline"
	}
	if description == "" {
		description = "A saved anonymous public fallback."
	}
	var body bytes.Buffer
	if err := offlineFallbackDocument.Execute(&body, struct{ Title, Description string }{title, description}); err != nil {
		http.Error(w, "Offline preparation refused", 500)
		return
	}
	capture.fallbackHTML = bytes.Clone(body.Bytes())
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body.Bytes())
}

// The recorder inspects actual commit-time headers and the final mutable map.
// Sticky refusal catches middleware adding a private field then removing it
// after next returns. It never relays bytes or grants admission by headers alone.
type offlineWriter struct {
	header   http.Header
	body     bytes.Buffer
	status   int
	limit    int64
	rejected bool
}

func (w *offlineWriter) Header() http.Header { return w.header }
func (w *offlineWriter) inspect() {
	for name, values := range w.header {
		lower := strings.ToLower(name)
		if lower == "set-cookie" || lower == "set-cookie2" || lower == "location" || lower == "vary" || lower == "www-authenticate" || lower == "content-encoding" || lower == "content-security-policy" || lower == "content-security-policy-report-only" || lower == "trailer" || lower == "transfer-encoding" || lower == "x-nextgo-nonce" || strings.HasPrefix(lower, "trailer:") {
			w.rejected = true
		}
		if lower == "cache-control" {
			for _, value := range values {
				for _, directive := range strings.Split(value, ",") {
					key, _, _ := strings.Cut(strings.TrimSpace(directive), "=")
					if strings.EqualFold(key, "private") || strings.EqualFold(key, "no-store") {
						w.rejected = true
					}
				}
			}
		}
	}
}
func (w *offlineWriter) WriteHeader(status int) {
	w.inspect()
	if status != http.StatusOK {
		w.rejected = true
	}
	if w.status == 0 {
		w.status = status
	}
}
func (w *offlineWriter) Write(data []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	} else {
		w.inspect()
	}
	if int64(w.body.Len())+int64(len(data)) > w.limit {
		w.rejected = true
		return 0, ErrOfflineAdmission
	}
	return w.body.Write(data)
}
func (w *offlineWriter) Flush() { w.rejected = true }

// This private capture always requests identity HTML and cannot negotiate a
// representation. Skip only framework compression/Vary, keeping every app
// middleware and rejecting any Vary they publish at the final recorder.
func offlineResponseCompression(app *App, identity, compressed http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if capture := offlineCaptureFor(r); capture != nil && capture.app == app {
			identity.ServeHTTP(w, r)
			return
		}
		compressed.ServeHTTP(w, r)
	})
}

func (a *App) offlineRequestValid(r *http.Request, capture *offlineCapture) bool {
	return r != nil && r.URL != nil && capture != nil && capture.app == a && capture.path == r.URL.EscapedPath() && r.Host == capture.host && r.URL.Host == capture.host && r.URL.Scheme == capture.scheme && r.URL.User == nil && r.URL.Opaque == "" && r.URL.Fragment == "" && r.RemoteAddr == "" && len(r.Trailer) == 0 && r.ContentLength == 0 && len(r.TransferEncoding) == 0 && r.Method == http.MethodGet && len(r.Header) == 0 && r.Body == http.NoBody && r.URL.RawQuery == "" && !r.URL.ForceQuery && r.TLS == nil
}

func (a *App) offlinePublicContext(ctx context.Context) context.Context {
	capture, _ := ctx.Value(offlineCaptureKey{}).(*offlineCapture)
	if capture == nil || capture.app != a {
		return ctx
	}
	origin, err := publicorigin.Parse(capture.scheme + "://" + capture.host)
	if err != nil {
		capture.err = ErrOfflineAdmission
		return ctx
	}
	return paths.WithContext(publicorigin.WithContext(ctx, origin), a.offlineRenderPaths(ctx))
}
