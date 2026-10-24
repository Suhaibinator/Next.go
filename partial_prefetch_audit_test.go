package nextgo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/postresponse"
	"github.com/Suhaibinator/Next.go/scripts"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

func partialShellAuditRead(t *testing.T, app *App, target, purpose, capability, hint string, stream bool, change func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest("GET", target, nil)
	r.Header.Set("X-Nextgo-Navigate", "1")
	r.Header.Set("Cookie", "visitor=private-audit-visitor")
	r.Header.Set("Authorization", "private-audit-authorization")
	if purpose != "" {
		r.Header.Set(navigation.PurposeHeader, purpose)
	}
	if capability != "" {
		r.Header.Set(navigation.ShellProtocolHeader, capability)
	}
	if hint != "" {
		r.Header.Set(navigation.ShellHeader, hint)
	}
	if stream {
		r.Header.Set(navigation.StreamHeader, "1")
	}
	r.SetPathValue("private", "private-path-value")
	r = r.WithContext(context.WithValue(r.Context(), partialPrivateKey{}, "private-audit-context"))
	if change != nil {
		change(r)
	}
	// Reject scheduled refresh work so clock/binding tests inspect the selected
	// generation without a background reader/loader changing it underneath them.
	ctx, completion := postresponse.Collect(r.Context())
	r = r.WithContext(ctx)
	defer completion.Finish(context.Background(), false)
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	return w
}
func partialShellAuditEnvelope(t *testing.T, w *httptest.ResponseRecorder) Envelope {
	t.Helper()
	var e Envelope
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatal(err, w.Code, w.Body.String())
	}
	return e
}
func partialShellAuditStream(t *testing.T, w *httptest.ResponseRecorder) (Envelope, string) {
	t.Helper()
	var e Envelope
	var chunks strings.Builder
	count := 0
	decoder := &navigation.StreamDecoder{}
	if err := decoder.Feed(w.Body.Bytes(), func(frame navigation.StreamFrame) error {
		switch frame.Type {
		case "shell":
			if err := json.Unmarshal(frame.Page, &e); err != nil {
				return err
			}
		case "chunk":
			count++
			chunks.WriteString(frame.HTML)
		}
		return nil
	}); err != nil {
		t.Fatal(err, w.Code, w.Body.String())
	}
	if err := decoder.Finish(); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatal("expected both current request regions", count)
	}
	return e, chunks.String()
}
func partialShellAuditPrivacy(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Header().Get("Cache-Control") != "private, no-store" || w.Header().Get(navigation.StaleHeader) != "" || w.Header().Get("Age") != "" {
		t.Fatal("shell negotiation changed ordinary privacy policy", w.Header())
	}
}
func partialShellAuditPrefetch(t *testing.T, w *httptest.ResponseRecorder, positive bool) Envelope {
	t.Helper()
	partialShellAuditPrivacy(t, w)
	if w.Code != 200 || w.Header().Get("X-Nextgo-Prefetch") != "shell" || w.Header().Get(navigation.ShellProtocolHeader) != "1" {
		t.Fatal("public shell prefetch not negotiated", w.Code, w.Header(), w.Body.String())
	}
	e := partialShellAuditEnvelope(t, w)
	if !navigation.ValidShellID(e.Shell) || e.ShellRef || !strings.Contains(e.HTML, "waiting first") || strings.Contains(e.HTML, "first:one:") || e.Title != "Static title 1" {
		t.Fatal("prefetch is not a full public fallback shell", e)
	}
	for _, private := range []string{"private-audit-visitor", "private-audit-authorization", "private-audit-context", "private-path-value", "private-prefetch-query"} {
		if strings.Contains(e.HTML, private) || strings.Contains(e.Title, private) || strings.Contains(e.Description, private) {
			t.Fatal("private input reached public shell values", private)
		}
	}
	millis, err := strconv.Atoi(w.Header().Get(navigation.ShellStaleHeader))
	if err != nil || millis < 0 || millis > navigation.MaxShellStaleMillis || positive && millis <= 0 || !positive && millis != 0 {
		t.Fatal("invalid bounded shell retention hint", millis, err, w.Header())
	}
	return e
}
func TestPartialShellAuditPrefetchResumeLocalAndShared(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(fmt.Sprint(remote), func(t *testing.T) {
			var pages, regions, middleware atomic.Int32
			var broken atomic.Bool
			route := partialTestRoute(&pages, &regions, &broken)
			config := Config{Routes: []Route{route}, Middleware: []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					middleware.Add(1)
					if r.Header.Get("X-Audit-Deny") == "1" {
						http.Error(w, "denied", 403)
						return
					}
					next.ServeHTTP(w, r)
				})
			}}}
			if remote {
				_, config.SharedCache = sharedPrerenderMemory(t)
				config.Routes[0].Prerender.Shared = true
			}
			c, _ := buildSharedPrerenderTest(t, config)
			broken.Store(true)
			app := testApp(t, c)
			start := middleware.Load()
			for _, capability := range []string{"", "0", "2", "01", "1, 1", "not-a-version"} {
				w := partialShellAuditRead(t, app, "/article/one", "prefetch", capability, "", false, nil)
				if w.Code != 204 || w.Body.Len() != 0 || w.Header().Get(navigation.ShellStaleHeader) != "" || w.Header().Get("X-Nextgo-Prefetch") != "" {
					t.Fatal("legacy prefetch changed", capability, w.Code, w.Header(), w.Body.String())
				}
			}
			prefetched := partialShellAuditPrefetch(t, partialShellAuditRead(t, app, "/article/one?message=private-prefetch-query", "prefetch", "1", "", false, nil), true)
			duplicateCapability := partialShellAuditRead(t, app, "/article/one", "prefetch", "1", "", false, func(r *http.Request) { r.Header.Add(navigation.ShellProtocolHeader, "1") })
			if duplicateCapability.Code != 204 || duplicateCapability.Body.Len() != 0 {
				t.Fatal("ambiguous duplicate capabilities authorized shell-only output", duplicateCapability.Code, duplicateCapability.Body.String())
			}
			if pages.Load() != 1 || regions.Load() != 0 {
				t.Fatal("prefetch evaluated static or current request work", pages.Load(), regions.Load())
			}
			for _, hint := range []string{prefetched.Shell, "", strings.Repeat("f", 64), "bad", strings.ToUpper(prefetched.Shell)} {
				before := regions.Load()
				w := partialShellAuditRead(t, app, "/article/one?message=active-private", "", "1", hint, true, nil)
				partialShellAuditPrivacy(t, w)
				e, chunks := partialShellAuditStream(t, w)
				match := hint == prefetched.Shell
				if w.Code != 200 || e.Shell != prefetched.Shell || e.ShellRef != match || match && e.HTML != "" || !match && !strings.Contains(e.HTML, "waiting first") {
					t.Fatal("hint did not select exact public shell ownership", hint, e, w.Header())
				}
				if regions.Load() != before+2 || !strings.Contains(chunks, "first:one:active-private:private-audit-visitor:private-audit-context") || strings.Contains(chunks, "waiting first") {
					t.Fatal("resumed regions did not execute current request", regions.Load(), chunks)
				}
				if w.Header().Get("X-Nextgo-Prefetch") != "" || w.Header().Get(navigation.StaleHeader) != "" {
					t.Fatal("active response acquired full-page cache admission", w.Header())
				}
			}
			duplicateHint := partialShellAuditRead(t, app, "/article/one?message=duplicate-current", "", "1", prefetched.Shell, true, func(r *http.Request) { r.Header.Add(navigation.ShellHeader, prefetched.Shell) })
			duplicateEnvelope, duplicateChunks := partialShellAuditStream(t, duplicateHint)
			if duplicateEnvelope.ShellRef || duplicateEnvelope.HTML == "" || !strings.Contains(duplicateChunks, "duplicate-current") {
				t.Fatal("ambiguous duplicate hints authorized a reference", duplicateEnvelope, duplicateChunks)
			}
			// No capability means the established stream remains self-contained. Even
			// with capability, legacy buffered JSON must never become a shell reference.
			for _, item := range []struct {
				cap    string
				stream bool
			}{{"", true}, {"1", false}} {
				w := partialShellAuditRead(t, app, "/article/one?message=legacy-current", "", item.cap, prefetched.Shell, item.stream, nil)
				var e Envelope
				if item.stream {
					e, _ = partialShellAuditStream(t, w)
				} else {
					e = partialShellAuditEnvelope(t, w)
					if !strings.Contains(e.HTML, "legacy-current") {
						t.Fatal(e)
					}
				}
				if e.Shell != "" || e.ShellRef || e.HTML == "" {
					t.Fatal("legacy response contains shell-only markers", e)
				}
			}
			before := regions.Load()
			for _, purpose := range []string{"", "prefetch"} {
				w := partialShellAuditRead(t, app, "/article/one", purpose, "1", prefetched.Shell, true, func(r *http.Request) { r.Header.Set("X-Audit-Deny", "1") })
				if w.Code != 403 || bytes.Contains(w.Body.Bytes(), []byte(`"shell"`)) || bytes.Contains(w.Body.Bytes(), []byte("public shell")) {
					t.Fatal("a shell hint skipped current authorization", w.Code, w.Body.String())
				}
			}
			if regions.Load() != before || middleware.Load() < start+15 {
				t.Fatal("negotiation skipped middleware or invoked denied regions", middleware.Load()-start, regions.Load()-before)
			}
		})
	}
}
func TestPartialShellAuditFreshnessInvalidationAndReadonlyRequests(t *testing.T) {
	var pages, regions atomic.Int32
	var broken atomic.Bool
	route := partialTestRoute(&pages, &regions, &broken)
	route.Prerender.Revalidate = 2 * time.Second
	route.Prerender.Expire = 5 * time.Second
	c, data := buildPrerenderTest(t, Config{Routes: []Route{route}})
	var manifest prerenderManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	var clock atomic.Int64
	clock.Store(manifest.Pages[0].GeneratedAt.Add(250 * time.Millisecond).UnixNano())
	c.Cache = cache.New(16, cache.WithClock(func() time.Time { return time.Unix(0, clock.Load()) }))
	app := testApp(t, c)
	response := partialShellAuditRead(t, app, "/article/one", "prefetch", "1", "", false, nil)
	first := partialShellAuditPrefetch(t, response, true)
	millis, _ := strconv.Atoi(response.Header().Get(navigation.ShellStaleHeader))
	if millis > 1750 {
		t.Fatal("client retention exceeds original fresh deadline", millis)
	}
	clock.Store(manifest.Pages[0].GeneratedAt.Add(3 * time.Second).UnixNano())
	partialShellAuditPrefetch(t, partialShellAuditRead(t, app, "/article/one", "prefetch", "1", "", false, nil), false)
	if regions.Load() != 0 || pages.Load() != 1 {
		t.Fatal("stale prefetch executed private work or discarded refresh ownership", pages.Load(), regions.Load())
	}
	clock.Store(manifest.Pages[0].GeneratedAt.Add(6 * time.Second).UnixNano())
	regenerated := partialShellAuditRead(t, app, "/article/one?message=after-expiry", "", "1", first.Shell, true, nil)
	e, chunks := partialShellAuditStream(t, regenerated)
	if e.ShellRef || e.Shell == first.Shell || e.HTML == "" || !strings.Contains(chunks, "after-expiry") || pages.Load() != 2 {
		t.Fatal("hard-expired shell was referenced", e, pages.Load(), chunks)
	}
	old := e.Shell
	app.RevalidateTag("partial-shell")
	w := partialShellAuditRead(t, app, "/article/one?message=after-invalidation", "", "1", old, true, nil)
	e, _ = partialShellAuditStream(t, w)
	if e.ShellRef || e.Shell == old || e.HTML == "" || pages.Load() != 3 {
		t.Fatal("invalidation did not replace shell identity", e, pages.Load())
	}
	before := regions.Load()
	for _, mode := range []string{"body", "bypass", "nonce"} {
		t.Run(mode, func(t *testing.T) {
			var reads atomic.Int32
			w := partialShellAuditRead(t, app, "/article/one", "prefetch", "1", old, false, func(r *http.Request) {
				switch mode {
				case "body":
					r.Body = &partialShellAuditBody{reads: &reads}
					r.ContentLength = 19
				case "bypass":
					*r = *r.WithContext(cache.WithBypass(r.Context()))
				case "nonce":
					ctx, err := scripts.WithNonce(r.Context(), strings.Repeat("a", 24))
					if err != nil {
						t.Fatal(err)
					}
					*r = *r.WithContext(ctx)
				}
			})
			if w.Code != 204 || w.Body.Len() != 0 || w.Header().Get("X-Nextgo-Prefetch") != "" || w.Header().Get(navigation.ShellStaleHeader) != "" || reads.Load() != 0 {
				t.Fatal("unsafe input acquired public shell admission or consumed request body", mode, w.Code, w.Header(), reads.Load(), w.Body.String())
			}
		})
	}
	if regions.Load() != before {
		t.Fatal("bypassed prefetch evaluated private regions")
	}
}

type partialShellAuditBody struct{ reads *atomic.Int32 }

func (b *partialShellAuditBody) Read([]byte) (int, error) { b.reads.Add(1); return 0, io.EOF }
func (*partialShellAuditBody) Close() error               { return nil }

func TestPartialShellAuditIdentityBindsAllPublicInputs(t *testing.T) {
	var pages, regions atomic.Int32
	var broken atomic.Bool
	route := partialTestRoute(&pages, &regions, &broken)
	c, _ := buildPrerenderTest(t, Config{Routes: []Route{route}})
	app := testApp(t, c)
	route = app.routes[0]
	key := app.prerenderKey("/article/one")
	original, _, err := cache.Lookup(context.Background(), app.config.Cache, key, cache.Options{Lifetime: route.Cache.Lifetime}, func(context.Context) (cachedPage, error) { return cachedPage{}, fmt.Errorf("unexpected loader") })
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := app.partialShellID(route, "/article/one", original)
	if err != nil {
		t.Fatal(err)
	}
	changes := map[string]func(*App, *Route, *string, *cachedPage){
		"build":         func(a *App, _ *Route, _ *string, _ *cachedPage) { a.config.BuildID = "other-build" },
		"configuration": func(a *App, _ *Route, _ *string, _ *cachedPage) { a.configurationID = "other-configuration" },
		"pattern":       func(_ *App, r *Route, _ *string, _ *cachedPage) { r.Pattern = "/other/:slug" },
		"path":          func(_ *App, _ *Route, path *string, _ *cachedPage) { *path = "/article/two" },
		"body": func(_ *App, _ *Route, _ *string, v *cachedPage) {
			v.HTML = strings.Replace(v.HTML, "public shell", "PUBLIC shell", 1)
		},
		"title":       func(_ *App, _ *Route, _ *string, v *cachedPage) { v.Page.Title = "changed title" },
		"description": func(_ *App, _ *Route, _ *string, v *cachedPage) { v.Page.Description = "changed description" },
		"metadata":    func(_ *App, _ *Route, _ *string, v *cachedPage) { v.Page.Meta = map[string]string{"audit": "changed"} },
		"document":    func(_ *App, _ *Route, _ *string, v *cachedPage) { v.Page.document.Direction = "rtl" },
		"head": func(_ *App, _ *Route, _ *string, v *cachedPage) {
			v.Page.preparedHead.elements = append(v.Page.preparedHead.elements, seo.Element{Tag: "meta", Attrs: map[string]string{"name": "audit", "content": "changed"}})
		},
		"props": func(_ *App, _ *Route, _ *string, v *cachedPage) {
			v.Regions[0].Props = json.RawMessage(`{"prefix":"changed"}`)
		},
		"region-id": func(_ *App, _ *Route, _ *string, v *cachedPage) {
			v.Regions[0].ID = "visitor-other"
			v.HTML = strings.Replace(v.HTML, "visitor-first", "visitor-other", 1)
		},
		"registration": func(_ *App, r *Route, _ *string, v *cachedPage) {
			r.Regions = maps.Clone(r.Regions)
			r.Regions["visitor-alias"] = r.Regions["visitor"]
			v.Regions[0].Region = "visitor-alias"
		},
		"generation": func(_ *App, _ *Route, _ *string, v *cachedPage) { v.GeneratedAt = v.GeneratedAt.Add(time.Second) },
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			copyApp := *app
			copyRoute := route
			path := "/article/one"
			value := ownCachedPage(original)
			change(&copyApp, &copyRoute, &path, &value)
			token, err := copyApp.partialShellID(copyRoute, path, value)
			if err != nil {
				t.Fatal(err)
			}
			if !navigation.ValidShellID(token) || token == baseline {
				t.Fatal("shell identity omitted a public input", name, token)
			}
		})
	}
	if regions.Load() != 0 {
		t.Fatal("identity computation ran a private region")
	}
}
func TestPartialShellAuditOversizedSerializedPrefetchSkipsRetention(t *testing.T) {
	var pages, regions atomic.Int32
	var broken atomic.Bool
	route := partialTestRoute(&pages, &regions, &broken)
	route.Page = func(*Context) (Page, error) {
		pages.Add(1)
		return Page{Title: "large public shell", Body: ui.Fragment(ui.Text(strings.Repeat("<&>", 150000)), ui.RequestRegion("large-hole", "visitor", map[string]string{"prefix": "large"}, ui.Text("waiting")))}, nil
	}
	c, _ := buildPrerenderTest(t, Config{Routes: []Route{route}})
	app := testApp(t, c)
	w := partialShellAuditRead(t, app, "/article/one", "prefetch", "1", "", false, nil)
	if w.Code != 204 || w.Body.Len() != 0 || w.Header().Get("X-Nextgo-Prefetch") != "" || w.Header().Get(navigation.ShellProtocolHeader) != "" || w.Header().Get(navigation.ShellStaleHeader) != "" || regions.Load() != 0 || pages.Load() != 1 {
		t.Fatal("oversized serialized shell advertised reusable admission", w.Code, w.Header(), regions.Load(), pages.Load(), w.Body.Len())
	}
}

func TestPartialShellAuditResponseCookieCannotAdvertiseBrowserRetention(t *testing.T) {
	var pages, regions atomic.Int32
	var broken atomic.Bool
	route := partialTestRoute(&pages, &regions, &broken)
	config := Config{Routes: []Route{route}, Middleware: []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("X-Audit-Response-Cookie") == "1" {
				http.SetCookie(w, &http.Cookie{Name: "private-response-session", Value: "private-response-cookie-secret", Path: "/", HttpOnly: true, Secure: true})
			}
			next.ServeHTTP(w, r)
		})
	}}}
	c, _ := buildPrerenderTest(t, config)
	broken.Store(true)
	app := testApp(t, c)
	w := partialShellAuditRead(t, app, "/article/one?message=private-prefetch-query", "prefetch", "1", "", false, func(r *http.Request) { r.Header.Set("X-Audit-Response-Cookie", "1") })
	e := partialShellAuditPrefetch(t, w, false)
	if len(w.Header().Values("Set-Cookie")) != 1 || strings.Contains(e.HTML, "private-response-cookie-secret") || regions.Load() != 0 || pages.Load() != 1 {
		t.Fatal("cookie response retained private output or evaluated a region", w.Header(), regions.Load(), pages.Load())
	}
}
