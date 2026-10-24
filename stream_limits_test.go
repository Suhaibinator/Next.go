package nextgo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/ui"
)

func TestStreamingOptionsValidation(t *testing.T) {
	for _, options := range []StreamingOptions{
		{MaxStreams: -1}, {MaxStreams: 1025}, {MaxLoaders: -1}, {MaxLoaders: 4097},
		{Timeout: -time.Second}, {Timeout: time.Microsecond}, {Timeout: 5*time.Minute + 1},
	} {
		if _, err := New(Config{Streaming: options}); err == nil {
			t.Fatalf("accepted invalid streaming options: %+v", options)
		}
	}
	app := testApp(t, Config{})
	if got := app.config.Streaming; got.MaxStreams != 32 || got.MaxLoaders != 64 || got.Timeout != 30*time.Second {
		t.Fatal(got)
	}
}

func TestStreamingAdmissionAcrossRepresentationsAndLocales(t *testing.T) {
	for _, localized := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "managed-locales"}[localized], func(t *testing.T) {
			started := make(chan struct{}, 1)
			var calls atomic.Int32
			config := Config{Streaming: StreamingOptions{MaxStreams: 1, MaxLoaders: 1}, Routes: []Route{
				{Pattern: "/limited", Page: func(c *Context) (Page, error) {
					if c.Query.Get("immediate") == "1" {
						return Page{Stream: true, Body: ui.Text("available again")}, nil
					}
					return Page{Stream: true, Body: ui.Defer("held", ui.Text("pending"), func(ctx context.Context) (ui.Node, error) {
						calls.Add(1)
						started <- struct{}{}
						<-ctx.Done()
						return nil, ctx.Err()
					})}, nil
				}},
				{Pattern: "/ordinary", Page: func(*Context) (Page, error) { return Page{Body: ui.Text("ordinary page")}, nil }},
				{Pattern: "/api/health", API: func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("healthy")) }},
			}}
			first, second := "/limited", "/limited"
			prefix := ""
			if localized {
				config.Locales = &LocaleConfig{Codes: []string{"en", "fr"}, Default: "en", PrefixDefault: true}
				first, second, prefix = "/en/limited", "/fr/limited", "/fr"
			}
			app := testApp(t, config)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan struct{})
			go func() {
				defer close(done)
				app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", first, nil).WithContext(ctx))
			}()
			select {
			case <-started:
			case <-time.After(3 * time.Second):
				t.Fatal("initial loader did not start")
			}
			for _, representation := range []string{"document", "legacy-json", "navigation-stream", "head"} {
				r := httptest.NewRequest("GET", second, nil)
				if representation == "head" {
					r.Method = "HEAD"
				}
				if representation == "legacy-json" || representation == "navigation-stream" {
					r.Header.Set("X-Nextgo-Navigate", "1")
				}
				if representation == "navigation-stream" {
					r.Header.Set(navigation.StreamHeader, "1")
				}
				w := httptest.NewRecorder()
				app.ServeHTTP(w, r)
				if w.Code != 503 || w.Header().Get("Retry-After") != "1" || w.Header().Get(navigation.StreamAdmissionHeader) != "busy" || w.Header().Get("Cache-Control") != "private, no-store" || w.Header().Get(navigation.StreamHeader) != "" {
					t.Fatal(representation, w.Code, w.Header())
				}
				if r.Header.Get("X-Nextgo-Navigate") == "1" {
					var envelope Envelope
					if err := json.Unmarshal(w.Body.Bytes(), &envelope); err != nil || envelope.Status != 503 || envelope.Configuration == "" || envelope.URL != second || strings.Contains(envelope.HTML, "pending") {
						t.Fatal(representation, envelope, err)
					}
				} else if r.Method == "HEAD" && w.Body.Len() != 0 {
					t.Fatal("HEAD emitted a body")
				}
			}
			for _, path := range []string{"/ordinary", "/api/health"} {
				w := httptest.NewRecorder()
				app.ServeHTTP(w, httptest.NewRequest("GET", prefix+path, nil))
				if w.Code != 200 {
					t.Fatal(path, w.Code)
				}
			}
			r := httptest.NewRequest("GET", second, nil)
			r.Header.Set("X-Nextgo-Navigate", "1")
			r.Header.Set(navigation.StreamHeader, "1")
			r.Header.Set(navigation.PurposeHeader, "prefetch")
			w := httptest.NewRecorder()
			app.ServeHTTP(w, r)
			if w.Code != 204 || calls.Load() != 1 {
				t.Fatal("prefetch consumed admission or invoked loader", w.Code, calls.Load())
			}
			cancel()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("canceled render retained admission")
			}
			w = httptest.NewRecorder()
			app.ServeHTTP(w, httptest.NewRequest("GET", second+"?immediate=1", nil))
			if w.Code != 200 || !strings.Contains(w.Body.String(), "available again") {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

func TestStreamingCanceledCallbackKeepsPhysicalPermit(t *testing.T) {
	started, release, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	app := testApp(t, Config{Streaming: StreamingOptions{MaxStreams: 1, MaxLoaders: 1, Timeout: 250 * time.Millisecond}, Routes: []Route{
		{Pattern: "/", Page: func(*Context) (Page, error) {
			return Page{Stream: true, Body: ui.Defer("held", ui.Text("pending"), func(context.Context) (ui.Node, error) {
				if calls.Add(1) == 1 {
					close(started)
					<-release // Intentionally ignore cancellation to prove physical accounting.
					defer close(returned)
				}
				return ui.Text("completed"), nil
			})}, nil
		}},
		{Pattern: "/immediate", Page: func(*Context) (Page, error) { return Page{Stream: true, Body: ui.Text("immediate")}, nil }},
	}})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	done := make(chan struct{})
	go func() { defer close(done); app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil)) }()
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("loader did not start")
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("render deadline did not return")
	}
	w := httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest("GET", "/immediate", nil))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "immediate") {
		t.Fatal("render slot not released", w.Code)
	}
	// A legacy buffered request must share the same physical loader budget.
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Nextgo-Navigate", "1")
	w = httptest.NewRecorder()
	app.ServeHTTP(w, r)
	if calls.Load() != 1 || w.Code != 500 {
		t.Fatal("canceled callback's permit was reused", calls.Load(), w.Code)
	}
	close(release)
	select {
	case <-returned:
	case <-time.After(3 * time.Second):
		t.Fatal("loader did not return")
	}
	w = httptest.NewRecorder()
	app.ServeHTTP(w, r)
	if w.Code != 200 || calls.Load() != 2 || !strings.Contains(w.Body.String(), "completed") {
		t.Fatal("physical capacity did not recover", w.Code, calls.Load())
	}
}
