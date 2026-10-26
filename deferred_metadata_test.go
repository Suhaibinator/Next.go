package nextgo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const metadataBrowserUA = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 Chrome/124.0.0.0 Safari/537.36"

func deferredTestRequest(app *App, method, ua string, navigate, stream bool, metadata []string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, "/", nil)
	if ua != "" {
		r.Header.Set("User-Agent", ua)
	}
	if navigate {
		r.Header.Set("X-Nextgo-Navigate", "1")
	}
	if stream {
		r.Header.Set(navigation.StreamHeader, "1")
	}
	for _, v := range metadata {
		r.Header.Add(navigation.MetadataHeader, v)
	}
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	return w
}
func metadataTestPage(load MetadataFunc) Page {
	return Page{Stream: true, Metadata: seo.Metadata{Title: "Fallback title", Description: "Fallback description"}, DeferredMetadata: load, Body: ui.El("main", nil, ui.Text("stable body"))}
}

type metadataResource struct {
	calls   *atomic.Int32
	invalid string
}

func (r metadataResource) Elements() ([]seo.Element, error) {
	if r.calls.Add(1) != 1 {
		return nil, fmt.Errorf("resource invoked twice")
	}
	if r.invalid == "meta" {
		return []seo.Element{{Tag: "meta", Attrs: map[string]string{"name": "private-resource", "content": "secret-diagnostic"}}}, nil
	}
	if r.invalid == "canonical" {
		return []seo.Element{{Tag: "link", Attrs: map[string]string{"rel": "canonical", "href": "/secret-diagnostic"}}}, nil
	}
	return []seo.Element{{Tag: "link", Attrs: map[string]string{"rel": "stylesheet", "href": "/assets/metadata.css"}}}, nil
}

func TestDeferredMetadataBlockingModesAndLegacyPrecedence(t *testing.T) {
	for _, tc := range []struct {
		name, ua, method                          string
		html, navigate, stream, nonstream, static bool
		negotiation                               []string
	}{
		{name: "default", ua: metadataBrowserUA, method: "GET"},
		{name: "unknown", ua: "Unknown Fetcher", method: "GET", html: true},
		{name: "absent", method: "GET", html: true},
		{name: "crawler", ua: metadataBrowserUA + " Googlebot", method: "GET", html: true},
		{name: "head", ua: metadataBrowserUA, method: "HEAD", html: true},
		{name: "nonstream", ua: metadataBrowserUA, method: "GET", html: true, nonstream: true},
		{name: "old-nav", ua: metadataBrowserUA, method: "GET", html: true, navigate: true, stream: true},
		{name: "duplicate-nav", ua: metadataBrowserUA, method: "GET", html: true, navigate: true, stream: true, negotiation: []string{"1", "1"}},
		{name: "unknown-nav", ua: metadataBrowserUA, method: "GET", html: true, navigate: true, stream: true, negotiation: []string{"2"}},
		{name: "static-export", ua: metadataBrowserUA, method: "GET", html: true, static: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls atomic.Int32
			config := Config{Streaming: StreamingOptions{Metadata: MetadataStreamingOptions{HTML: tc.html}}, Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) {
				p := metadataTestPage(func(_ *Context, parent seo.Metadata) (seo.Metadata, error) {
					calls.Add(1)
					if parent.Title != "Fallback title" {
						return seo.Metadata{}, fmt.Errorf("wrong fallback")
					}
					return seo.Metadata{Title: "Final title", Description: "Final description"}, nil
				})
				p.Stream = !tc.nonstream
				return p, nil
			}}}}
			if tc.static {
				config.Phase = PhaseStaticExport
			}
			app := testApp(t, config)
			w := deferredTestRequest(app, tc.method, tc.ua, tc.navigate, tc.stream, tc.negotiation)
			if w.Code != 200 || calls.Load() != 1 || strings.Contains(w.Body.String(), "data-nextgo-metadata-owner") || strings.Contains(w.Body.String(), `"metadataPending":true`) {
				t.Fatal(w.Code, calls.Load(), w.Body.String())
			}
			if tc.method == "HEAD" {
				if w.Body.Len() != 0 {
					t.Fatal("HEAD body")
				}
			} else if !strings.Contains(w.Body.String(), "Final title") {
				t.Fatal("blocking response missing final metadata", w.Body.String())
			}
		})
	}
	app := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) {
		p := metadataTestPage(func(*Context, seo.Metadata) (seo.Metadata, error) {
			return seo.Metadata{Title: "Ignored", Description: "Ignored", Robots: "index"}, nil
		})
		p.Stream = false
		p.Title = "Legacy title"
		p.Description = "Legacy description"
		p.Meta = map[string]string{"ROBOTS": "noindex"}
		return p, nil
	}}}})
	w := deferredTestRequest(app, "GET", metadataBrowserUA, true, false, nil)
	var envelope Envelope
	if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || envelope.Title != "Legacy title" || envelope.Description != "Legacy description" || envelope.Meta["robots"] != "noindex" {
		t.Fatal(envelope, err)
	}
}

func TestDeferredMetadataReadonlyViewportPanicAndAfterRefusal(t *testing.T) {
	for name, load := range map[string]MetadataFunc{
		"header": func(c *Context, _ seo.Metadata) (seo.Metadata, error) {
			_ = c.SetHeader("X-Late", "secret-diagnostic")
			return seo.Metadata{Title: "bad"}, nil
		},
		"cookie": func(c *Context, _ seo.Metadata) (seo.Metadata, error) {
			_ = c.SetCookie(&http.Cookie{Name: "secret", Value: "diagnostic"})
			return seo.Metadata{}, nil
		},
		"status": func(c *Context, _ seo.Metadata) (seo.Metadata, error) {
			_ = c.SetStatus(201)
			return seo.Metadata{}, nil
		},
		"redirect": func(c *Context, _ seo.Metadata) (seo.Metadata, error) {
			_ = c.Redirect("/secret-diagnostic", 307)
			return seo.Metadata{}, nil
		},
		"after": func(c *Context, _ seo.Metadata) (seo.Metadata, error) {
			_ = After(c.Context, func(context.Context) error { return errors.New("must not run") })
			return seo.Metadata{}, nil
		},
		"language": func(*Context, seo.Metadata) (seo.Metadata, error) { return seo.Metadata{Language: "fr"}, nil },
		"viewport-colors": func(*Context, seo.Metadata) (seo.Metadata, error) {
			return seo.Metadata{Viewport: &seo.Viewport{ColorScheme: "dark", ThemeColors: []seo.ThemeColor{{Color: "red"}}}}, nil
		},
		"panic":  func(*Context, seo.Metadata) (seo.Metadata, error) { panic("secret-diagnostic") },
		"access": func(*Context, seo.Metadata) (seo.Metadata, error) { return seo.Metadata{}, ErrUnauthorized },
		"invalid": func(*Context, seo.Metadata) (seo.Metadata, error) {
			return seo.Metadata{Canonical: "javascript:secret-diagnostic"}, nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			app := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) { p := metadataTestPage(load); p.Stream = false; return p, nil }}}})
			w := deferredTestRequest(app, "GET", metadataBrowserUA, false, false, nil)
			if w.Code != 500 || w.Header().Get("X-Late") != "" || w.Header().Get("Location") != "" || len(w.Result().Cookies()) != 0 || strings.Contains(w.Body.String(), "secret-diagnostic") {
				t.Fatal(w.Code, w.Header(), w.Body.String())
			}
		})
	}
}

func TestDeferredMetadataValidatesFallbackBeforeCallbackAndFreezesResourcesOnce(t *testing.T) {
	for _, invalid := range []string{"meta", "canonical"} {
		t.Run(invalid, func(t *testing.T) {
			var resources, calls atomic.Int32
			app := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) {
				p := metadataTestPage(func(*Context, seo.Metadata) (seo.Metadata, error) { calls.Add(1); return seo.Metadata{}, nil })
				p.Resources = append(p.Resources, metadataResource{&resources, invalid})
				return p, nil
			}}}})
			w := deferredTestRequest(app, "GET", metadataBrowserUA, false, false, nil)
			if w.Code != 500 || calls.Load() != 0 || resources.Load() != 1 || strings.Contains(w.Body.String(), "secret-diagnostic") {
				t.Fatal(w.Code, calls.Load(), resources.Load())
			}
		})
	}
	var resources atomic.Int32
	data, err := seo.JSONLD(map[string]any{"@type": "Thing", "name": "frozen"})
	if err != nil {
		t.Fatal(err)
	}
	app := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) {
		p := metadataTestPage(func(*Context, seo.Metadata) (seo.Metadata, error) {
			if resources.Load() != 1 {
				return seo.Metadata{}, fmt.Errorf("resource not prepared exactly once")
			}
			return seo.Metadata{Title: "Final", Canonical: "https://example.test/final"}, nil
		})
		p.Resources = append(p.Resources, metadataResource{&resources, ""})
		p.JSONLD = []seo.StructuredData{data}
		p.Stream = false
		return p, nil
	}}}})
	w := deferredTestRequest(app, "GET", metadataBrowserUA, false, false, nil)
	if w.Code != 200 || resources.Load() != 1 || strings.Count(w.Body.String(), "metadata.css") != 1 || strings.Count(w.Body.String(), "application/ld+json") != 1 || !strings.Contains(w.Body.String(), "https://example.test/final") {
		t.Fatal(w.Code, resources.Load(), w.Body.String())
	}
}

func TestDeferredMetadataPublicSnapshotMemoAndPartialRefusal(t *testing.T) {
	var calls, loads atomic.Int32
	load := func(c *Context) (string, error) {
		return Memo(c.Context, "shared-metadata", func(context.Context) (string, error) { loads.Add(1); return "Memo final", nil })
	}
	app := testApp(t, Config{Routes: []Route{{Pattern: "/", Cache: &CachePolicy{TTL: time.Minute}, Page: func(c *Context) (Page, error) {
		if _, err := load(c); err != nil {
			return Page{}, err
		}
		return Page{Metadata: seo.Metadata{Title: "Fallback"}, Body: ui.Text("public body"), DeferredMetadata: func(c *Context, _ seo.Metadata) (seo.Metadata, error) {
			calls.Add(1)
			title, err := load(c)
			return seo.Metadata{Title: title}, err
		}}, nil
	}}}})
	for i := 0; i < 2; i++ {
		w := deferredTestRequest(app, "GET", metadataBrowserUA, true, false, nil)
		if w.Code != 200 || !strings.Contains(w.Body.String(), "Memo final") || strings.Contains(w.Body.String(), "metadataPending") {
			t.Fatal(w.Code, w.Body.String())
		}
	}
	if calls.Load() != 1 || loads.Load() != 1 {
		t.Fatal("public result not frozen or Memo scope lost", calls.Load(), loads.Load())
	}
	var deferred atomic.Int32
	partial := testApp(t, Config{Dev: true, Routes: []Route{{Pattern: "/", Prerender: &PrerenderPolicy{Partial: true, HostIndependent: true}, Regions: map[string]RegionFunc{"slot": func(*Context, json.RawMessage) (ui.Node, error) { return ui.Text("region"), nil }}, Page: func(*Context) (Page, error) {
		return Page{Body: ui.RequestRegion("slot-instance", "slot", nil, ui.Text("shell")), DeferredMetadata: func(*Context, seo.Metadata) (seo.Metadata, error) { deferred.Add(1); return seo.Metadata{}, nil }}, nil
	}}}})
	w := deferredTestRequest(partial, "GET", metadataBrowserUA, false, false, nil)
	if w.Code != 500 || deferred.Load() != 0 {
		t.Fatal("partial declaration was not refused", w.Code, deferred.Load())
	}
}

func TestDeferredMetadataPrefetchRedirectAndErrorSkipCallbacks(t *testing.T) {
	for _, mode := range []string{"prefetch", "redirect", "access"} {
		t.Run(mode, func(t *testing.T) {
			var calls atomic.Int32
			app := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: func(c *Context) (Page, error) {
				p := metadataTestPage(func(*Context, seo.Metadata) (seo.Metadata, error) { calls.Add(1); return seo.Metadata{}, nil })
				if mode == "redirect" {
					_ = c.Redirect("/target", 307)
				}
				if mode == "access" {
					return p, ErrUnauthorized
				}
				return p, nil
			}}}})
			r := httptest.NewRequest("GET", "/", nil)
			if mode == "prefetch" {
				r.Header.Set("X-Nextgo-Navigate", "1")
				r.Header.Set(navigation.PurposeHeader, "prefetch")
			}
			w := httptest.NewRecorder()
			app.ServeHTTP(w, r)
			if calls.Load() != 0 {
				t.Fatal("callback ran for terminal/speculation", mode)
			}
			want := map[string]int{"prefetch": 204, "redirect": 307, "access": 401}[mode]
			if w.Code != want {
				t.Fatal(w.Code, want)
			}
		})
	}
}

func TestDeferredMetadataNegotiatedTitleOnlyPendingIndicesAreExplicitEmpty(t *testing.T) {
	gate := make(chan struct{})
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(gate) }) })
	app := testApp(t, Config{Streaming: StreamingOptions{Metadata: MetadataStreamingOptions{HTML: true}}, Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) {
		return metadataTestPage(func(*Context, seo.Metadata) (seo.Metadata, error) {
			<-gate
			return seo.Metadata{Title: "Final title"}, nil
		}), nil
	}}}})
	// The immutable initial event is guaranteed to precede metadata even when
	// the loader resolves quickly; release before request to avoid timing sleeps.
	release.Do(func() { close(gate) })
	w := deferredTestRequest(app, "GET", metadataBrowserUA, true, true, []string{"1"})
	if w.Code != 200 || w.Header().Get(navigation.MetadataHeader) != "1" {
		t.Fatal(w.Code, w.Header())
	}
	lines := strings.Split(strings.TrimSpace(w.Body.String()), "\n")
	if len(lines) != 3 {
		t.Fatal("wrong stream completion", w.Body.String())
	}
	var shell map[string]json.RawMessage
	if err := json.Unmarshal([]byte(lines[0]), &shell); err != nil {
		t.Fatal(err)
	}
	if string(shell["metadataPending"]) != "true" || string(shell["metadataIndices"]) != "[]" {
		t.Fatal("title-only indices omitted", lines[0])
	}
	var metadata navigation.StreamFrame
	if err := json.Unmarshal([]byte(lines[1]), &metadata); err != nil || metadata.Type != "metadata" || metadata.Failed {
		t.Fatal(lines[1], err)
	}
	if !strings.Contains(lines[1], "Final title") || !strings.Contains(lines[2], `"type":"done"`) {
		t.Fatal(w.Body.String())
	}
}

func TestDeferredMetadataHTMLCarrierAndPolicyIdentity(t *testing.T) {
	app := testApp(t, Config{Streaming: StreamingOptions{Metadata: MetadataStreamingOptions{HTML: true}}, Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) {
		return metadataTestPage(func(*Context, seo.Metadata) (seo.Metadata, error) {
			return seo.Metadata{Title: "Final </template> & title"}, nil
		}), nil
	}}}})
	w := deferredTestRequest(app, "GET", metadataBrowserUA, false, false, nil)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `data-nextgo-metadata-version="1"`) || strings.Count(w.Body.String(), `<template data-nextgo-metadata=`) != 1 || strings.Count(w.Body.String(), `data-nextgo-metadata-ready=`) != 1 || strings.Contains(w.Body.String(), "Final </template>") {
		t.Fatal(w.Code, w.Body.String())
	}
	if !strings.Contains(w.Header().Get("Cache-Control"), "private") {
		t.Fatal("dynamic metadata reused")
	}
	for _, bad := range [][]string{{""}, {"bad\nagent"}, {strings.Repeat("x", 129)}, make([]string, 65)} {
		if _, err := New(Config{Streaming: StreamingOptions{Metadata: MetadataStreamingOptions{HTMLLimitedBots: bad}}}); err == nil {
			t.Fatal("invalid bot policy accepted")
		}
	}
	a := testApp(t, Config{Streaming: StreamingOptions{Metadata: MetadataStreamingOptions{HTMLLimitedBots: []string{"SPECIAL", "special"}}}})
	b := testApp(t, Config{Streaming: StreamingOptions{Metadata: MetadataStreamingOptions{HTMLLimitedBots: []string{"special"}}}})
	c := testApp(t, Config{Streaming: StreamingOptions{Metadata: MetadataStreamingOptions{HTML: true, HTMLLimitedBots: []string{"special"}}}})
	if a.configurationID != b.configurationID || a.configurationID == c.configurationID {
		t.Fatal("effective metadata policy not canonical/bound")
	}
}

func TestDeferredMetadataEffectiveViewportAndTitleTemplateApplyOnce(t *testing.T) {
	for _, mode := range []string{"inherit", "return-parent", "new-title", "legacy"} {
		t.Run(mode, func(t *testing.T) {
			app := testApp(t, Config{Routes: []Route{{Pattern: "/", Metadata: []MetadataLayer{{Load: func(*Context, seo.Metadata) (seo.Metadata, error) {
				return seo.Metadata{TitleTemplate: "%s | Site"}, nil
			}}}, Page: func(*Context) (Page, error) {
				p := metadataTestPage(func(_ *Context, parent seo.Metadata) (seo.Metadata, error) {
					if mode == "return-parent" {
						parent.Description = "changed"
						return parent, nil
					}
					if mode == "new-title" {
						return seo.Metadata{Title: "New"}, nil
					}
					return seo.Metadata{Language: "en", Direction: "ltr", Viewport: &seo.Viewport{Width: "device-width", InitialScale: 1}}, nil
				})
				p.Stream = false
				if mode == "legacy" {
					p.Title = "Legacy"
				}
				return p, nil
			}}}})
			w := deferredTestRequest(app, "GET", metadataBrowserUA, false, false, nil)
			want := "Fallback title | Site"
			if mode == "new-title" {
				want = "New | Site"
			}
			if mode == "legacy" {
				want = "Legacy"
			}
			if w.Code != 200 || !strings.Contains(w.Body.String(), "<title>"+want+"</title>") || strings.Contains(w.Body.String(), "| Site | Site") {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

func TestDeferredMetadataPreflightsRawBytesAndCounts(t *testing.T) {
	for name, value := range map[string]seo.Metadata{
		"title":      {Title: strings.Repeat("\"", navigation.MaxMetadataBytes+1)},
		"url":        {Canonical: strings.Repeat("x", navigation.MaxMetadataBytes+1)},
		"icons":      {Icons: make([]seo.Icon, navigation.MaxMetadataElements+1)},
		"attributes": {Icons: []seo.Icon{{URL: "/a", Sizes: strings.Repeat("x", navigation.MaxMetadataBytes+1)}}},
	} {
		t.Run(name, func(t *testing.T) {
			if err := preflightMetadata(value, "", "", nil); err == nil {
				t.Fatal("unbounded raw metadata accepted")
			}
			app := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) {
				p := metadataTestPage(func(*Context, seo.Metadata) (seo.Metadata, error) { return value, nil })
				p.Stream = false
				return p, nil
			}}}})
			if w := deferredTestRequest(app, "GET", metadataBrowserUA, false, false, nil); w.Code != 500 || w.Body.Len() > 32<<10 {
				t.Fatal("oversized metadata escaped into response", w.Code, w.Body.Len())
			}
		})
	}
}

type metadataFlushWriter struct {
	mu     sync.Mutex
	header http.Header
	body   strings.Builder
	status int
	first  chan string
	once   sync.Once
}

func (w *metadataFlushWriter) Header() http.Header { return w.header }
func (w *metadataFlushWriter) WriteHeader(status int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.status == 0 {
		w.status = status
	}
}
func (w *metadataFlushWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.status == 0 {
		w.status = 200
	}
	return w.body.Write(data)
}
func (w *metadataFlushWriter) Flush() {
	w.mu.Lock()
	snapshot := strings.Clone(w.body.String())
	w.mu.Unlock()
	w.once.Do(func() { w.first <- snapshot })
}
func (w *metadataFlushWriter) snapshot() (int, string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.status, strings.Clone(w.body.String())
}

func TestDeferredMetadataOneAdmissionAndLateFailureKeepsFallback(t *testing.T) {
	gate := make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(gate) })
	var calls atomic.Int32
	app := testApp(t, Config{Streaming: StreamingOptions{MaxStreams: 1, MaxLoaders: 1, Metadata: MetadataStreamingOptions{HTML: true}}, Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) {
		return metadataTestPage(func(*Context, seo.Metadata) (seo.Metadata, error) {
			calls.Add(1)
			<-gate
			return seo.Metadata{}, errors.New("secret-diagnostic")
		}), nil
	}}}})
	w := &metadataFlushWriter{header: make(http.Header), first: make(chan string, 1)}
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("User-Agent", metadataBrowserUA)
	done := make(chan struct{})
	go func() { defer close(done); app.ServeHTTP(w, r) }()
	select {
	case initial := <-w.first:
		if !strings.Contains(initial, "Fallback title") || !strings.Contains(initial, "stable body") {
			t.Fatal("fallback not committed", initial)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("progressive shell missing or duplicate admission")
	}
	busy := deferredTestRequest(app, "GET", metadataBrowserUA, false, false, nil)
	if busy.Code != 503 || busy.Header().Get("Retry-After") != "1" || calls.Load() > 1 {
		t.Fatal("render admission was not shared", busy.Code, calls.Load())
	}
	release.Do(func() { close(gate) })
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("metadata response did not complete")
	}
	status, body := w.snapshot()
	if status != 200 || strings.Contains(body, "secret-diagnostic") || strings.Count(body, "<title>") != 1 || !strings.Contains(body, `&#34;failed&#34;:true`) || strings.Count(body, `data-nextgo-metadata-ready=`) != 1 {
		t.Fatal(status, body)
	}
}

func TestDeferredMetadataPublicPrefetchMissDoesNotStartLoader(t *testing.T) {
	var calls atomic.Int32
	app := testApp(t, Config{Routes: []Route{{Pattern: "/", Cache: &CachePolicy{TTL: time.Minute}, Page: func(*Context) (Page, error) {
		return Page{Metadata: seo.Metadata{Title: "Fallback"}, Body: ui.Text("public"), DeferredMetadata: func(*Context, seo.Metadata) (seo.Metadata, error) {
			calls.Add(1)
			return seo.Metadata{Title: "Frozen"}, nil
		}}, nil
	}}}})
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Nextgo-Navigate", "1")
	r.Header.Set(navigation.PurposeHeader, "prefetch")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	if w.Code != 204 || calls.Load() != 0 {
		t.Fatal("speculative cache miss started callback", w.Code, calls.Load())
	}
	if w := deferredTestRequest(app, "GET", metadataBrowserUA, true, false, nil); w.Code != 200 || calls.Load() != 1 {
		t.Fatal(w.Code, calls.Load())
	}
	w = httptest.NewRecorder()
	app.ServeHTTP(w, r)
	if w.Code != 200 || calls.Load() != 1 {
		t.Fatal("frozen public prefetch changed", w.Code, calls.Load())
	}
}

func TestDeferredMetadataPreparationSnapshotsClearClosures(t *testing.T) {
	for _, mode := range []string{"public", "prerender", "offline"} {
		t.Run(mode, func(t *testing.T) {
			var calls, resources atomic.Int32
			route := Route{Pattern: "/", Cache: &CachePolicy{TTL: time.Minute}, Page: func(*Context) (Page, error) {
				p := metadataTestPage(func(*Context, seo.Metadata) (seo.Metadata, error) {
					calls.Add(1)
					return seo.Metadata{Title: "Prepared final"}, nil
				})
				p.Stream = false
				p.Resources = append(p.Resources, metadataResource{&resources, ""})
				return p, nil
			}}
			app := testApp(t, Config{Streaming: StreamingOptions{Metadata: MetadataStreamingOptions{HTML: true}}, Routes: []Route{route}})
			ctx := context.Background()
			if mode == "prerender" {
				ctx = context.WithValue(ctx, prerenderCaptureKey{}, &prerenderCapture{})
			}
			if mode == "offline" {
				ctx = context.WithValue(ctx, offlineCaptureKey{}, &offlineCapture{})
			}
			r := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
			r.Header.Set("User-Agent", metadataBrowserUA)
			r.Header.Set(navigation.MetadataHeader, "1")
			r.Header.Set(navigation.StreamHeader, "1")
			frozen, err := app.snapshotPage(ctx, r, nil, nil, route)
			if err != nil || calls.Load() != 1 || resources.Load() != 1 || frozen.Page.Title != "Prepared final" || frozen.Page.preparedHead == nil {
				t.Fatal(mode, err, calls.Load(), resources.Load(), frozen.Page.Title)
			}
			p := frozen.Page
			if p.DeferredMetadata != nil || p.deferredContext != nil || p.deferredPlan != nil || p.deferredJob != nil || p.Resources != nil || p.JSONLD != nil || strings.Contains(p.preparedHead.markup, "data-nextgo-metadata-owned") {
				t.Fatal("snapshot retained live work or pending ownership", mode)
			}
		})
	}
}

func TestDeferredMetadataPublicIgnoredMutationInvalidatesSnapshot(t *testing.T) {
	for _, mutation := range []func(*Context){func(c *Context) { _ = c.SetHeader("X-Late", "private") }, func(c *Context) { _ = After(c.Context, func(context.Context) error { return nil }) }} {
		route := Route{Pattern: "/", Cache: &CachePolicy{TTL: time.Minute}, Page: func(*Context) (Page, error) {
			return Page{Body: ui.Text("public"), DeferredMetadata: func(c *Context, _ seo.Metadata) (seo.Metadata, error) {
				mutation(c)
				return seo.Metadata{Title: "Must not freeze"}, nil
			}}, nil
		}}
		app := testApp(t, Config{Routes: []Route{route}})
		r := httptest.NewRequest("GET", "/", nil)
		if _, err := app.snapshotPage(r.Context(), r, nil, nil, route); !errors.Is(err, ErrPublicCacheResponse) {
			t.Fatal("ignored read-only mutation admitted public result", err)
		}
	}
}

func TestDeferredMetadataDefensiveParentCannotChangeFrozenPolicy(t *testing.T) {
	app := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) {
		p := metadataTestPage(func(_ *Context, parent seo.Metadata) (seo.Metadata, error) {
			parent.Viewport.ThemeColors[0].Color = "blue"
			parent.Alternates["en"] = "https://example.test/changed"
			return parent, nil
		})
		p.Stream = false
		p.Metadata.Viewport = &seo.Viewport{ColorScheme: "light", ThemeColors: []seo.ThemeColor{{Color: "red"}}}
		p.Metadata.Alternates = map[string]string{"en": "https://example.test/original"}
		return p, nil
	}}}})
	if w := deferredTestRequest(app, "GET", metadataBrowserUA, false, false, nil); w.Code != 500 {
		t.Fatal("mutation moved defensive viewport baseline", w.Code, w.Body.String())
	}
}

func TestDeferredMetadataCanceledMemoRetainsPhysicalLoaderSlot(t *testing.T) {
	app := testApp(t, Config{Streaming: StreamingOptions{MaxStreams: 2, MaxLoaders: 1, Timeout: time.Second}})
	physicalStarted, physicalRelease := make(chan struct{}), make(chan struct{})
	var release sync.Once
	defer release.Do(func() { close(physicalRelease) })
	makePlan := func(ctx context.Context, load MetadataFunc) *deferredMetadataPlan {
		t.Helper()
		ctx = WithRequestMemo(ctx)
		r := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
		plan, err := app.prepareDeferredMetadata(&Context{Context: ctx, Request: r}, metadataTestPage(load))
		if err != nil {
			t.Fatal(err)
		}
		return plan
	}
	first, cancelFirst := context.WithCancel(context.Background())
	defer cancelFirst()
	plan := makePlan(first, func(c *Context, _ seo.Metadata) (seo.Metadata, error) {
		_, err := Memo(c.Context, "physical", func(context.Context) (string, error) {
			close(physicalStarted)
			<-physicalRelease // Deliberately models an upstream ignoring cancellation.
			return "complete", nil
		})
		return seo.Metadata{}, err
	})
	work, cancel, finish, job, err := app.beginMetadata(first, plan)
	if err != nil {
		t.Fatal(err)
	}
	defer cancel()
	defer finish()
	select {
	case <-physicalStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("Memo callback never started")
	}
	cancelFirst()
	if result := waitMetadata(work, job); result.err == nil {
		t.Fatal("canceled metadata waiter succeeded")
	}
	finish() // Releases render admission, not the physically retained loader.
	var secondCalls atomic.Int32
	secondCtx, cancelSecond := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancelSecond()
	secondPlan := makePlan(secondCtx, func(*Context, seo.Metadata) (seo.Metadata, error) { secondCalls.Add(1); return seo.Metadata{}, nil })
	secondWork, cancelWork, finishWork, secondJob, err := app.beginMetadata(secondCtx, secondPlan)
	if err != nil {
		t.Fatal(err)
	}
	if result := waitMetadata(secondWork, secondJob); result.err == nil || secondCalls.Load() != 0 {
		t.Fatal("canceled waiter released physical Memo ownership", result.err, secondCalls.Load())
	}
	cancelWork()
	finishWork()
	release.Do(func() { close(physicalRelease) })
	thirdPlan := makePlan(context.Background(), func(*Context, seo.Metadata) (seo.Metadata, error) { return seo.Metadata{Title: "Recovered"}, nil })
	thirdWork, cancelThird, finishThird, thirdJob, err := app.beginMetadata(context.Background(), thirdPlan)
	if err != nil {
		t.Fatal(err)
	}
	defer cancelThird()
	defer finishThird()
	if result := waitMetadata(thirdWork, thirdJob); result.err != nil || result.page.Title != "Recovered" {
		t.Fatal("physical completion did not release capacity", result.err, result.page.Title)
	}
}

func TestDeferredMetadataRetainedMemoCannotPublishBeforeFinalReadonlyCheck(t *testing.T) {
	for _, mode := range []string{"navigation", "public"} {
		for _, mutation := range []string{"header", "after"} {
			t.Run(mode+"/"+mutation, func(t *testing.T) {
				allowCallbackReturn, allowMutation, allowMemoExit := make(chan struct{}), make(chan struct{}), make(chan struct{})
				var returnOnce, mutationOnce, exitOnce sync.Once
				defer returnOnce.Do(func() { close(allowCallbackReturn) })
				defer mutationOnce.Do(func() { close(allowMutation) })
				defer exitOnce.Do(func() { close(allowMemoExit) })
				callbackReturned := make(chan *Context, 1)
				attempted := make(chan error, 1)
				load := func(c *Context, _ seo.Metadata) (seo.Metadata, error) {
					waiter, cancelWaiter := context.WithCancel(c.Context)
					defer cancelWaiter()
					_, _ = Memo(waiter, "retained-response-attempt", func(context.Context) (string, error) {
						cancelWaiter() // Return the Memo waiter while its real work stays live.
						<-allowMutation
						var err error
						if mutation == "header" {
							err = c.SetHeader("X-Late", "must-not-publish")
						} else {
							err = After(c.Context, func(context.Context) error { return errors.New("must not register") })
						}
						attempted <- err
						<-allowMemoExit
						return "finished", nil
					})
					<-allowCallbackReturn
					defer func() { callbackReturned <- c }()
					return seo.Metadata{Title: "Must never publish success"}, nil
				}
				route := Route{Pattern: "/", Page: func(*Context) (Page, error) {
					p := metadataTestPage(load)
					p.Stream = mode == "navigation"
					return p, nil
				}}
				app := testApp(t, Config{Streaming: StreamingOptions{Timeout: 5 * time.Second}, Routes: []Route{route}})
				done := make(chan error, 1)
				var w *metadataFlushWriter
				if mode == "public" {
					r := httptest.NewRequest("GET", "/", nil)
					go func() { _, err := app.snapshotPage(r.Context(), r, nil, nil, route); done <- err }()
				} else {
					w = &metadataFlushWriter{header: make(http.Header), first: make(chan string, 1)}
					r := httptest.NewRequest("GET", "/", nil)
					r.Header.Set("User-Agent", metadataBrowserUA)
					r.Header.Set("X-Nextgo-Navigate", "1")
					r.Header.Set(navigation.StreamHeader, "1")
					r.Header.Set(navigation.MetadataHeader, "1")
					go func() { app.ServeHTTP(w, r); done <- nil }()
					select {
					case <-w.first:
					case <-time.After(3 * time.Second):
						t.Fatal("navigation shell did not commit")
					}
				}
				returnOnce.Do(func() { close(allowCallbackReturn) })
				var readonly *Context
				select {
				case readonly = <-callbackReturned:
				case <-time.After(3 * time.Second):
					t.Fatal("metadata callback did not return")
				}
				// Exercise the permanent guard even if a view has already been sealed.
				// A seal must not turn this later attempted mutation into an unrecorded
				// ErrHeadersCommitted. The tracked Memo is still physically live.
				readonly.response.seal()
				mutationOnce.Do(func() { close(allowMutation) })
				select {
				case err := <-attempted:
					want := ErrDeferredMetadataResponse
					if mutation == "after" {
						want = ErrAfterDeferredMetadata
					}
					if !errors.Is(err, want) {
						t.Fatal("late guard lost sticky attempt", err)
					}
				case <-time.After(3 * time.Second):
					t.Fatal("retained Memo did not attempt mutation")
				}
				select {
				case err := <-done:
					t.Fatal("result published before retained Memo completion", err)
				default:
				}
				if w != nil {
					_, body := w.snapshot()
					if strings.Contains(body, `"type":"metadata"`) {
						t.Fatal("early terminal frame", body)
					}
				}
				exitOnce.Do(func() { close(allowMemoExit) })
				select {
				case err := <-done:
					if mode == "public" {
						if !errors.Is(err, ErrPublicCacheResponse) {
							t.Fatal("public snapshot admitted late mutation", err)
						}
					} else {
						status, body := w.snapshot()
						if status != 200 || !strings.Contains(body, `"type":"metadata"`) || !strings.Contains(body, `"failed":true`) || strings.Contains(body, "Must never publish success") || strings.Contains(body, "must-not-publish") {
							t.Fatal("late mutation did not retain fallback with failed receipt", status, body)
						}
					}
				case <-time.After(3 * time.Second):
					t.Fatal("drained result did not finish")
				}
			})
		}
	}
}

func TestDeferredMetadataCanceledWaitRejectsAlreadyQueuedSuccess(t *testing.T) {
	for i := 0; i < 64; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		results := make(chan metadataResult, 1)
		results <- metadataResult{page: Page{Title: "Stale success"}}
		cancel()
		result := waitMetadata(ctx, &metadataJob{result: results})
		if !errors.Is(result.err, context.Canceled) || result.page.Title != "" {
			t.Fatal("canceled caller received queued success", result.err, result.page.Title)
		}
	}
}

func TestDeferredMetadataCanceledMuxRejectsQueuedSuccessAtShellCommit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := make(chan metadataResult, 1)
	results <- metadataResult{page: Page{Title: "Stale success"}}
	page := Page{Body: ui.Text("shell"), deferredJob: &metadataJob{result: results}}
	var terminals int
	err := renderMetadataEvents(ctx, page, func(event ui.StreamEvent) error {
		if event.ID == "" {
			cancel()
		}
		return nil
	}, func(result metadataResult) error {
		terminals++
		if !errors.Is(result.err, context.Canceled) || result.page.Title != "" {
			t.Error("canceled render emitted a queued successful terminal", result.err, result.page.Title)
		}
		return nil
	})
	if terminals != 1 {
		t.Fatal("missing or duplicated failed cancellation receipt", terminals)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatal("canceled render returned success", err)
	}
}
