//go:build !js

package nextgo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/internal/publicorigin"
	"github.com/Suhaibinator/Next.go/offline"
	"github.com/Suhaibinator/Next.go/scripts"
	"github.com/Suhaibinator/Next.go/ui"
)

func offlineTestPolicy() offline.Config {
	return offline.Config{ID: "reader", Origin: "https://site.test", Semantics: offline.SavedAnonymousPublic, Fallback: "/fallback", Pages: []offline.Page{{Path: "/public", MaxAge: time.Hour}, {Path: "/fallback", MaxAge: time.Hour}}}
}
func offlineTestRoute(path string, page PageFunc) Route {
	return Route{Pattern: path, Cache: &CachePolicy{TTL: time.Minute}, Page: page}
}
func offlineTestApp(t *testing.T, config Config) *App {
	t.Helper()
	if config.BuildID == "" {
		config.BuildID = strings.Repeat("a", 20)
	}
	if config.Routes == nil {
		config.Routes = []Route{offlineTestRoute("/public", func(*Context) (Page, error) {
			return Page{Title: "Public", Body: ui.El("h1", nil, ui.Text("Meaningful public HTML"))}, nil
		}), offlineTestRoute("/fallback", func(*Context) (Page, error) {
			return Page{Title: "Fallback", Description: "Reconnect later.", Body: ui.El("p", nil, ui.Text("App body intentionally omitted"))}, nil
		})}
	}
	app, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := app.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return app
}

func TestPrepareOfflineOwnsAnonymousBufferedDocumentsAndIdentity(t *testing.T) {
	type privateKey struct{}
	calls, middleware := 0, 0
	app := offlineTestApp(t, Config{BasePath: "/reader", Middleware: []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			middleware++
			if r.Context().Value(privateKey{}) != nil || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" || len(r.Header) != 0 || r.Host != "site.test" || r.URL.Scheme != "https" || r.TLS != nil || r.Body != http.NoBody {
				t.Error("not a fresh anonymous request", r)
			}
			r = r.WithContext(context.WithValue(r.Context(), privateKey{}, "middleware auth secret"))
			r.SetPathValue("secret", "middleware hidden secret")
			r.Form = map[string][]string{"secret": {"middleware form secret"}}
			next.ServeHTTP(w, r)
		})
	}}, Routes: []Route{offlineTestRoute("/public", func(c *Context) (Page, error) {
		calls++
		if c.Value(privateKey{}) != nil || c.Request.PathValue("secret") != "" || c.Request.Form != nil || c.Request.RemoteAddr != "" || c.Request.TLS != nil || PublicOrigin(c.Context) != "https://site.test" {
			t.Error("private middleware/caller inputs reached loader")
		}
		return Page{Title: "Public", Body: ui.Text("Meaningful public HTML")}, nil
	}), offlineTestRoute("/fallback", func(*Context) (Page, error) {
		calls++
		return Page{Title: `<b>Offline</b>`, Description: `<script>private()</script>`, Body: ui.Text("App body intentionally omitted")}, nil
	})}})
	policy := offlineTestPolicy()
	policy.BasePath = "/reader"
	value, err := app.PrepareOffline(context.WithValue(context.Background(), privateKey{}, "private caller secret"), policy)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || middleware != 2 || value.BuildID() != app.config.BuildID || value.ConfigurationID() != app.configurationID || len(value.Identity()) != 64 || value.Admission() != offline.NativeAdmissionV1 {
		t.Fatal(calls, middleware, value)
	}
	docs := value.Documents()
	for _, doc := range docs {
		sum := sha256.Sum256(doc.HTML)
		if doc.SHA256 != hex.EncodeToString(sum[:]) || doc.ContentType != "text/html; charset=utf-8" || doc.MaxAge != time.Hour || doc.URL != "https://site.test/reader"+doc.Path {
			t.Fatal(doc)
		}
		if doc.Fallback {
			for _, forbidden := range []string{"App body", "data-nextgo-", "<script", "<link", "src=", "<b>Offline</b>"} {
				if strings.Contains(string(doc.HTML), forbidden) {
					t.Fatal("fallback is not inert", string(doc.HTML))
				}
			}
			if !strings.Contains(string(doc.HTML), "&lt;b&gt;Offline&lt;/b&gt;") {
				t.Fatal(string(doc.HTML))
			}
		} else if !strings.Contains(string(doc.HTML), "Meaningful public HTML") || !strings.Contains(string(doc.HTML), `data-nextgo-build="`+app.config.BuildID+`"`) {
			t.Fatal(string(doc.HTML))
		}
	}
	original := string(value.Documents()[0].HTML)
	docs[0].HTML[0] = '!'
	docs[0].Path = "/changed"
	copyPolicy := value.Policy()
	copyPolicy.Pages[0].Path = "/changed"
	policy.Pages[0].Path = "/input-changed"
	if string(value.Documents()[0].HTML) != original || value.Policy().Pages[0].Path == "/changed" {
		t.Fatal("preparation retained caller backing storage")
	}
	changed := offlineTestPolicy()
	changed.BasePath = "/reader"
	changed.Pages[0].MaxAge = 2 * time.Hour
	other, err := app.PrepareOffline(context.Background(), changed)
	if err != nil || other.Identity() == value.Identity() {
		t.Fatal("retention was not bound", err)
	}
}

func TestPrepareOfflineRefusesResponseMutationsAndUnsupportedPages(t *testing.T) {
	for _, kind := range []string{"cookie", "header", "status", "redirect", "stream", "error", "bypass", "nonce", "request-header", "request-query", "request-host", "request-tls-shape"} {
		t.Run(kind, func(t *testing.T) {
			app := offlineTestApp(t, Config{Middleware: []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch kind {
					case "bypass":
						r = r.WithContext(cache.WithBypass(r.Context()))
					case "nonce":
						ctx, err := scripts.WithNonce(r.Context(), strings.Repeat("a", 24))
						if err != nil {
							t.Fatal(err)
						}
						r = r.WithContext(ctx)
					case "request-header":
						r.Header.Set("Authorization", "private")
					case "request-query":
						r.URL.RawQuery = "private=1"
					case "request-host":
						r.Host = "other.test"
					case "request-tls-shape":
						r.RemoteAddr = "127.0.0.1:1234"
					}
					next.ServeHTTP(w, r)
				})
			}}, Routes: []Route{offlineTestRoute("/public", func(c *Context) (Page, error) {
				switch kind {
				case "cookie":
					_ = c.SetCookie(&http.Cookie{Name: "session", Value: "private"})
				case "header":
					_ = c.SetHeader("X-Public", "not permitted in public snapshot")
				case "status":
					_ = c.SetStatus(201)
				case "redirect":
					_ = c.Redirect("/elsewhere", 303)
				case "error":
					return Page{}, errors.New("private loader details")
				}
				return Page{Title: "Public", Stream: kind == "stream", Body: ui.Text("not approved")}, nil
			}), offlineTestRoute("/fallback", func(*Context) (Page, error) { return Page{Body: ui.Text("fallback")}, nil })}})
			value, err := app.PrepareOffline(context.Background(), offlineTestPolicy())
			if !errors.Is(err, ErrOfflineAdmission) || value != nil || strings.Contains(err.Error(), "private loader") {
				t.Fatal(value, err)
			}
		})
	}
}

func TestPrepareOfflineInspectsCompleteMiddlewareResponse(t *testing.T) {
	for _, kind := range []string{"late-cookie", "commit-hidden-cookie", "vary", "no-store", "private", "csp", "location", "status", "flush", "short-circuit", "overflow", "late-content-type", "fallback-script"} {
		t.Run(kind, func(t *testing.T) {
			middleware := func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					switch kind {
					case "short-circuit":
						_, _ = w.Write([]byte("<html>not a matched page</html>"))
						return
					case "commit-hidden-cookie":
						w = &offlineCookieWriter{ResponseWriter: w}
					case "vary":
						w.Header().Set("Vary", "Cookie")
					case "no-store":
						w.Header().Set("Cache-Control", "no-store")
					case "private":
						w.Header().Set("Cache-Control", "private")
					case "csp":
						w.Header().Set("Content-Security-Policy", "script-src 'none'")
					case "location":
						w.Header().Set("Location", "/elsewhere")
					case "status":
						w.WriteHeader(201)
					case "flush":
						w.(http.Flusher).Flush()
					}
					next.ServeHTTP(w, r)
					switch kind {
					case "late-cookie":
						w.Header().Set("Set-Cookie", "private=1")
					case "commit-hidden-cookie":
						w.Header().Del("Set-Cookie")
					case "overflow":
						_, _ = w.Write([]byte(strings.Repeat("x", 600<<10)))
					case "fallback-script":
						if r.URL.Path == "/fallback" {
							_, _ = w.Write([]byte(`<script src="/assets/x.js"></script>`))
						}
					case "late-content-type":
						w.Header().Set("Content-Type", "application/json")
					}
				})
			}
			app := offlineTestApp(t, Config{Middleware: []func(http.Handler) http.Handler{middleware}})
			value, err := app.PrepareOffline(context.Background(), offlineTestPolicy())
			if !errors.Is(err, ErrOfflineAdmission) || value != nil {
				t.Fatal(value, err)
			}
		})
	}
}

type offlineCookieWriter struct{ http.ResponseWriter }

func (w *offlineCookieWriter) WriteHeader(status int) {
	w.Header().Set("Set-Cookie", "private=1")
	w.ResponseWriter.WriteHeader(status)
	w.Header().Del("Set-Cookie")
}

func TestPrepareOfflinePreflightDoesNotExecuteAPIsOrUndeclaredPages(t *testing.T) {
	for _, kind := range []string{"no-cache", "inactive-cache", "api", "configured", "prerender", "regions", "missing", "dev", "slash", "asset-prefix", "phase", "origin-mismatch"} {
		t.Run(kind, func(t *testing.T) {
			calls := 0
			config := Config{Routes: []Route{offlineTestRoute("/public", func(*Context) (Page, error) { calls++; return Page{Body: ui.Text("public")}, nil }), offlineTestRoute("/fallback", func(*Context) (Page, error) { calls++; return Page{Body: ui.Text("fallback")}, nil })}}
			switch kind {
			case "no-cache":
				config.Routes[0].Cache = nil
			case "inactive-cache":
				config.Routes[0].Cache = &CachePolicy{}
			case "api":
				config.Routes[0].Page = nil
				config.Routes[0].API = func(http.ResponseWriter, *http.Request) { calls++ }
			case "configured":
				config.Handlers = map[string]http.Handler{"/public": http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ })}
			case "prerender":
				config.prerenderDeferred = true
				config.Routes[0].Cache = nil
				config.Routes[0].Prerender = &PrerenderPolicy{HostIndependent: true}
			case "regions":
				config.Routes[0].Cache = nil
				config.Routes[0].Regions = map[string]RegionFunc{"private": func(*Context, json.RawMessage) (ui.Node, error) { calls++; return ui.Text("private"), nil }}
			case "missing":
				config.Routes[0].Pattern = "/other"
			case "dev":
				config.Dev = true
			case "slash":
				config.TrailingSlash = true
			case "asset-prefix":
				config.AssetPrefix = "/other-assets"
			case "phase":
				config.Phase = PhaseStaticExport
			}
			app := offlineTestApp(t, config)
			if kind == "origin-mismatch" {
				origin, err := publicorigin.Parse("https://other.test")
				if err != nil {
					t.Fatal(err)
				}
				app.config.publicOrigin = origin
			}
			value, err := app.PrepareOffline(context.Background(), offlineTestPolicy())
			if !errors.Is(err, ErrOfflineAdmission) || value != nil || calls != 0 {
				t.Fatal(value, err, calls)
			}
		})
	}
}

func TestPrepareOfflineCancellationBoundsAndAllOrNothing(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	app := offlineTestApp(t, Config{})
	if value, err := app.PrepareOffline(ctx, offlineTestPolicy()); value != nil || !errors.Is(err, context.Canceled) {
		t.Fatal(value, err)
	}
	if value, err := app.PrepareOffline(nil, offlineTestPolicy()); value != nil || !errors.Is(err, ErrOfflineAdmission) {
		t.Fatal(value, err)
	}
	policy := offlineTestPolicy()
	policy.Budget.DocumentBytes = 8
	if value, err := app.PrepareOffline(context.Background(), policy); value != nil || !errors.Is(err, ErrOfflineAdmission) {
		t.Fatal(value, err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	app = offlineTestApp(t, Config{Routes: []Route{offlineTestRoute("/public", func(c *Context) (Page, error) { <-c.Done(); return Page{}, c.Err() }), offlineTestRoute("/fallback", func(*Context) (Page, error) { return Page{Body: ui.Text("fallback")}, nil })}})
	if value, err := app.PrepareOffline(ctx, offlineTestPolicy()); value != nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(value, err)
	}
}

func TestPrepareOfflineCannotDispatchSelectedRequestsToOtherEndpoints(t *testing.T) {
	for _, target := range []string{"api", "handler", "loading", "internal-rewrite", "external-rewrite"} {
		t.Run(target, func(t *testing.T) {
			calls := 0
			config := Config{Routes: []Route{offlineTestRoute("/public", func(*Context) (Page, error) { calls++; return Page{Body: ui.Text("public")}, nil }), offlineTestRoute("/fallback", func(*Context) (Page, error) { calls++; return Page{Body: ui.Text("fallback")}, nil }), {Pattern: "/api/x", API: func(http.ResponseWriter, *http.Request) { calls++ }}}}
			config.Routes[0].Loading = func(*Context) (ui.Node, error) { calls++; return ui.Text("loading"), nil }
			config.Handlers = map[string]http.Handler{"/other-handler": http.HandlerFunc(func(http.ResponseWriter, *http.Request) { calls++ })}
			if strings.Contains(target, "rewrite") {
				destination := "/api/x"
				if target == "external-rewrite" {
					destination = "https://backend.invalid/"
					config.Proxy.Transport = offlineRejectTransport{calls: &calls}
				}
				config.Rewrites = []RewriteRule{{Source: "/public", Destination: destination}}
			} else {
				config.Middleware = []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
					return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						switch target {
						case "api":
							r.URL.Path = "/api/x"
						case "handler":
							r.URL.Path = "/other-handler"
						case "loading":
							r.URL.Path = "/_nextgo/loading"
							r.URL.RawQuery = "path=%2Fpublic"
						}
						r.URL.RawPath = r.URL.Path
						next.ServeHTTP(w, r)
					})
				}}
			}
			app := offlineTestApp(t, config)
			value, err := app.PrepareOffline(context.Background(), offlineTestPolicy())
			if value != nil || !errors.Is(err, ErrOfflineAdmission) || calls != 0 {
				t.Fatal(value, err, calls)
			}
		})
	}
}

type offlineRejectTransport struct{ calls *int }

func (t offlineRejectTransport) RoundTrip(*http.Request) (*http.Response, error) {
	*t.calls++
	return nil, errors.New("unexpected external dispatch")
}

func TestPrepareOfflineLeavesOrdinaryCompressionAndVaryUnchanged(t *testing.T) {
	app := offlineTestApp(t, Config{})
	r := httptest.NewRequest(http.MethodGet, "https://site.test/public", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	vary := strings.Join(w.Header().Values("Vary"), ",")
	if w.Code != http.StatusOK || w.Header().Get("Content-Encoding") != "gzip" || !strings.Contains(vary, "Accept-Encoding") || !strings.Contains(vary, "X-Nextgo-Navigate") {
		t.Fatal(w.Code, w.Header())
	}
	if (*PreparedOffline)(nil).Admission() != "" || new(PreparedOffline).Admission() != "" {
		t.Fatal("zero preparation granted admission")
	}
}
