package nextgo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/actions"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/postresponse"
	"github.com/Suhaibinator/Next.go/ui"
)

const actionViewTestName = "view-audit/v1"

type actionViewTestFrame struct {
	Version int             `json:"version"`
	Type    string          `json:"type"`
	Result  json.RawMessage `json:"result"`
	Page    json.RawMessage `json:"page"`
	ID      string          `json:"id"`
	HTML    string          `json:"html"`
	Failed  bool            `json:"failed"`
	Error   *struct {
		Code, Message string
		Status        int
	} `json:"error"`
}

func actionViewTestScope(a *App, source string) *App {
	if a.locales == nil {
		return a
	}
	u, _ := url.Parse(source)
	selected := a
	for _, scope := range a.locales.mounts {
		prefix := scope.app.paths.BasePath
		if (u.Path == prefix || strings.HasPrefix(u.Path, prefix+"/")) && len(prefix) >= len(selected.paths.BasePath) {
			selected = scope.app
		}
	}
	return selected
}
func actionViewTestRequest(a *App, source string) *http.Request {
	scope := actionViewTestScope(a, source)
	endpoint, err := scope.paths.URL("/_nextgo/actions/" + actionViewTestName)
	if err != nil {
		panic(err)
	}
	r := httptest.NewRequest("POST", "http://example.com"+endpoint, strings.NewReader(`{}`))
	r.Header.Set("Origin", "http://example.com")
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/x-nextgo-action-view+json")
	r.Header.Set("X-Nextgo-Action-Protocol", "1")
	r.Header.Set("X-Nextgo-Action-Revision", "v1")
	r.Header.Set("X-Nextgo-Action-View", "1")
	r.Header.Set("X-Nextgo-Action-URL", source)
	r.Header.Set("X-Nextgo-Action-Build", scope.config.BuildID)
	r.Header.Set(navigation.ConfigurationHeader, scope.configurationID)
	r.Header.Set(navigation.DraftHeader, "")
	return r
}
func actionViewTestServe(a *App, r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	a.ServeHTTP(w, r)
	return w
}
func actionViewTestFrames(t *testing.T, w *httptest.ResponseRecorder) []actionViewTestFrame {
	t.Helper()
	if w.Code != 200 || strings.Split(w.Header().Get("Content-Type"), ";")[0] != "application/x-nextgo-action-view+json" || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("not a private action-view stream: %d %v %s", w.Code, w.Header(), w.Body.String())
	}
	var frames []actionViewTestFrame
	for _, line := range bytes.Split(w.Body.Bytes(), []byte{'\n'}) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var f actionViewTestFrame
		if err := json.Unmarshal(line, &f); err != nil || f.Version != 1 {
			t.Fatalf("invalid action-view frame: %s (%v)", line, err)
		}
		frames = append(frames, f)
	}
	if len(frames) == 0 || frames[0].Type != "result" {
		t.Fatal("typed confirmation is not first", w.Body.String())
	}
	return frames
}
func actionViewTestPage(t *testing.T, frames []actionViewTestFrame) Envelope {
	t.Helper()
	for _, f := range frames {
		if f.Type == "shell" {
			var page Envelope
			if err := json.Unmarshal(f.Page, &page); err != nil {
				t.Fatal(err)
			}
			return page
		}
	}
	t.Fatal("missing refreshed shell", frames)
	return Envelope{}
}
func actionViewTestRegistrar(handler func(*actions.Context) (actions.Result, error)) func(*actions.Router) {
	return func(r *actions.Router) { r.HandleContract(actionViewTestName, "v1", handler) }
}

func TestActionViewFreshGETMiddlewareMemoAndQuery(t *testing.T) {
	type methodKey struct{}
	var value, posts, gets atomic.Int64
	app := testApp(t, Config{BuildID: "action-view-test", BasePath: "/docs", Middleware: []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == "POST" {
				posts.Add(1)
				r.SetPathValue("post-only", "must-not-leak")
				r.Form = url.Values{"post": []string{"private"}}
				r.PostForm = url.Values{"post": []string{"private"}}
				_, _ = Memo(r.Context(), "action-view-value", func(context.Context) (int64, error) { return value.Load(), nil })
			} else if r.URL.Path == "/count" {
				gets.Add(1)
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), methodKey{}, r.Method)))
		})
	}}, Routes: []Route{{Pattern: "/count", Cache: &CachePolicy{TTL: time.Hour, Tags: []string{"count"}}, Page: func(c *Context) (Page, error) {
		if c.Request.Method != "GET" || c.Value(methodKey{}) != "GET" || c.Request.Body != http.NoBody && c.Request.Body != nil || c.Request.ContentLength != 0 || c.Request.PathValue("post-only") != "" || len(c.Request.PostForm) != 0 || len(c.Request.Form) != 0 {
			return Page{}, fmt.Errorf("GET representation retained POST state")
		}
		current, err := Memo(c.Context, "action-view-value", func(context.Context) (int64, error) { return value.Load(), nil })
		return Page{Title: "Updated title", Body: ui.Text(fmt.Sprintf("value:%d query:%s", current, c.Query.Get("q")))}, err
	}}}, ActionRegistrars: []func(*actions.Router){actionViewTestRegistrar(func(*actions.Context) (actions.Result, error) {
		return actions.Result{Data: value.Add(1)}, nil
	})}})
	defer app.Close(context.Background())
	prime := request(app, "/docs/count?q=fresh", false)
	if prime.Code != 200 {
		t.Fatal(prime.Code, prime.Body.String())
	}
	frames := actionViewTestFrames(t, actionViewTestServe(app, actionViewTestRequest(app, "/docs/count?q=fresh")))
	if !strings.Contains(string(frames[0].Result), `"data":1`) {
		t.Fatal("typed data lost", string(frames[0].Result))
	}
	page := actionViewTestPage(t, frames)
	if page.HTML != "value:1 query:fresh" || page.URL != "/docs/count?q=fresh" || page.Title != "Updated title" || posts.Load() != 1 || gets.Load() < 2 {
		t.Fatal("stale or malformed GET refresh", page, posts.Load(), gets.Load())
	}
	if frames[len(frames)-1].Type != "done" {
		t.Fatal("view stream incomplete", frames)
	}
}

func TestActionViewPreflightRejectsBeforeMutationOrEndpoint(t *testing.T) {
	var effects, endpoints atomic.Int64
	app := testApp(t, Config{BuildID: "action-view-test", Routes: []Route{{Pattern: "/", Page: static("home")}, {Pattern: "/api/health", API: func(http.ResponseWriter, *http.Request) { endpoints.Add(1) }}}, Handlers: map[string]http.Handler{"/configured/": http.HandlerFunc(func(http.ResponseWriter, *http.Request) { endpoints.Add(1) })}, Rewrites: []RewriteRule{{Source: "/api-alias", Destination: "/api/health"}, {Source: "/proxy", Destination: "http://127.0.0.1:1/private"}}, ActionRegistrars: []func(*actions.Router){actionViewTestRegistrar(func(*actions.Context) (actions.Result, error) {
		effects.Add(1)
		return actions.Result{Data: "saved"}, nil
	})}})
	defer app.Close(context.Background())
	for _, test := range []struct {
		name   string
		change func(*http.Request)
	}{
		{"build", func(r *http.Request) { r.Header.Set("X-Nextgo-Action-Build", "old-build") }}, {"configuration", func(r *http.Request) { r.Header.Set(navigation.ConfigurationHeader, strings.Repeat("a", 64)) }}, {"draft", func(r *http.Request) { r.Header.Set(navigation.DraftHeader, "1") }}, {"missing-url", func(r *http.Request) { r.Header.Del("X-Nextgo-Action-URL") }}, {"absolute-url", func(r *http.Request) { r.Header.Set("X-Nextgo-Action-URL", "https://foreign.test/") }}, {"fragment", func(r *http.Request) { r.Header.Set("X-Nextgo-Action-URL", "/#private") }}, {"long-url", func(r *http.Request) { r.Header.Set("X-Nextgo-Action-URL", "/?q="+strings.Repeat("x", 8192)) }}, {"duplicate-capability", func(r *http.Request) { r.Header.Add("X-Nextgo-Action-View", "1") }}, {"invalid-slots", func(r *http.Request) { r.Header.Set(navigation.SlotsHeader, `{"configuration":"bad"}`) }}, {"api", func(r *http.Request) { r.Header.Set("X-Nextgo-Action-URL", "/api/health") }}, {"configured", func(r *http.Request) { r.Header.Set("X-Nextgo-Action-URL", "/configured/item") }}, {"rewrite-api", func(r *http.Request) { r.Header.Set("X-Nextgo-Action-URL", "/api-alias") }}, {"proxy", func(r *http.Request) { r.Header.Set("X-Nextgo-Action-URL", "/proxy") }},
	} {
		t.Run(test.name, func(t *testing.T) {
			beforeEffects, beforeEndpoints := effects.Load(), endpoints.Load()
			r := actionViewTestRequest(app, "/")
			test.change(r)
			w := actionViewTestServe(app, r)
			if w.Code < 400 || effects.Load() != beforeEffects || endpoints.Load() != beforeEndpoints {
				t.Fatal("preflight reached effect or endpoint", w.Code, effects.Load(), endpoints.Load(), w.Body.String())
			}
		})
	}
}

func TestActionViewLocaleAndOuterPlatformCompletion(t *testing.T) {
	var effects, completions atomic.Int64
	app := testApp(t, Config{BuildID: "action-view-locales", BasePath: "/docs", Locales: &LocaleConfig{Codes: []string{"en", "fr"}, Default: "en", PrefixDefault: true}, Routes: []Route{{Pattern: "/count", Page: func(c *Context) (Page, error) {
		if err := After(c.Context, func(work context.Context) error {
			if CurrentLocale(work) != "fr" {
				return fmt.Errorf("lost locale")
			}
			completions.Add(1)
			return nil
		}); err != nil {
			return Page{}, err
		}
		return Page{Body: ui.Text(CurrentLocale(c.Context) + ":" + c.Query.Get("q"))}, nil
	}}}, ActionRegistrars: []func(*actions.Router){actionViewTestRegistrar(func(c *actions.Context) (actions.Result, error) {
		effects.Add(1)
		if err := After(c.Context, func(context.Context) error { completions.Add(1); return nil }); err != nil {
			return actions.Result{}, err
		}
		return actions.Result{Data: CurrentLocale(c.Context)}, nil
	})}})
	defer app.Close(context.Background())
	ctx, collector := postresponse.Collect(context.Background())
	defer collector.Finish(context.Background(), false)
	r := actionViewTestRequest(app, "/docs/fr/count?q=oui").WithContext(ctx)
	frames := actionViewTestFrames(t, actionViewTestServe(app, r))
	page := actionViewTestPage(t, frames)
	if !strings.Contains(string(frames[0].Result), `"data":"fr"`) || page.HTML != "fr:oui" || page.URL != "/docs/fr/count?q=oui" || completions.Load() != 0 || effects.Load() != 1 {
		t.Fatal("locale or completion boundary changed", string(frames[0].Result), page, completions.Load(), effects.Load())
	}
	deadline, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	collector.Finish(deadline, true)
	if completions.Load() != 2 {
		t.Fatal("outer platform completion did not release both jobs", completions.Load())
	}
}

func TestActionViewStreamConfirmsDataBeforeDeferredRegionCompletes(t *testing.T) {
	release := make(chan struct{})
	var released atomic.Bool
	unblock := func() {
		if released.CompareAndSwap(false, true) {
			close(release)
		}
	}
	defer unblock()
	app := testApp(t, Config{BuildID: "action-view-stream", Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) {
		return Page{Stream: true, Body: ui.Defer("held", ui.Text("fresh fallback"), func(ctx context.Context) (ui.Node, error) {
			select {
			case <-release:
				return ui.Text("fresh final"), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		})}, nil
	}}}, ActionRegistrars: []func(*actions.Router){actionViewTestRegistrar(func(*actions.Context) (actions.Result, error) { return actions.Result{Data: "confirmed"}, nil })}})
	defer func() { unblock(); app.Close(context.Background()) }()
	w := &runtimeStreamRecorder{ResponseRecorder: httptest.NewRecorder(), flushes: make(chan string, 8)}
	done := make(chan struct{})
	go func() { defer close(done); app.ServeHTTP(w, actionViewTestRequest(app, "/")) }()
	var initial string
	for i := 0; i < 4; i++ {
		initial = runtimeStreamFlush(t, w)
		if strings.Contains(initial, `"type":"shell"`) {
			break
		}
	}
	if !strings.Contains(initial, `"type":"result"`) || !strings.Contains(initial, `"data":"confirmed"`) || !strings.Contains(initial, "fresh fallback") || strings.Contains(initial, "fresh final") {
		t.Fatal("typed result/fallback waits for region or includes private future", initial)
	}
	select {
	case <-done:
		t.Fatal("held region unexpectedly completed")
	default:
	}
	unblock()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not complete")
	}
	frames := actionViewTestFrames(t, w.ResponseRecorder)
	if frames[len(frames)-1].Type != "done" || !strings.Contains(w.Body.String(), "fresh final") {
		t.Fatal("deferred result missing", w.Body.String())
	}
}
