package nextgo

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

func TestAccessResultsDiscardPageAndEnforceStatusMetadataAndPrivacy(t *testing.T) {
	for _, denial := range []struct {
		err    error
		status int
		title  string
	}{{ErrUnauthorized, 401, "Sign in required"}, {fmt.Errorf("private detail: %w", ErrForbidden), 403, "Access denied"}} {
		for _, nav := range []bool{false, true} {
			called := false
			app := testApp(t, Config{Headers: http.Header{"Cache-Control": []string{"public, max-age=600"}}, Routes: []Route{{Pattern: "/private", Page: func(c *Context) (Page, error) {
				_ = c.SetStatus(200)
				return Page{Title: "secret", Body: ui.Text("private data must not render"), Metadata: seo.Metadata{Canonical: "https://secret.example.test"}}, denial.err
			}, Error: func(*Context, error) (Page, error) { called = true; return Page{Body: ui.Text("error boundary")}, nil }}}})
			w := request(app, "/private", nav)
			if w.Code != denial.status || called || strings.Contains(w.Body.String(), "private data") || strings.Contains(w.Body.String(), "secret.example") {
				t.Fatalf("denial leaked or lost: %d %s", w.Code, w.Body.String())
			}
			if w.Header().Get("Cache-Control") != "private, no-store" || w.Header().Get("X-Robots-Tag") != "noindex" || w.Header().Get("X-Nextgo-Prefetch") != "" {
				t.Fatal("denial privacy", w.Header())
			}
			if nav {
				var data Envelope
				if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil || data.Status != denial.status || data.Title != denial.title {
					t.Fatal(data, err)
				}
				if len(data.Head) != 1 || data.Head[0].Attrs["name"] != "robots" || data.Head[0].Attrs["content"] != "noindex" {
					t.Fatal("missing noindex", data.Head)
				}
			} else if !strings.Contains(w.Body.String(), `name="robots"`) {
				t.Fatal("SSR noindex missing")
			}
			head := httptest.NewRecorder()
			r := httptest.NewRequest("HEAD", "/private", nil)
			if nav {
				r.Header.Set("X-Nextgo-Navigate", "1")
			}
			app.ServeHTTP(head, r)
			if head.Code != denial.status || head.Body.Len() != 0 {
				t.Fatal("HEAD denial", head.Code, head.Body.String())
			}
		}
	}
}

func TestAccessBoundariesUseOnlySafeOuterLayouts(t *testing.T) {
	var failedLayoutCalls, deepBoundaryCalls int
	layout := func(_ *Context, n ui.Node) (ui.Node, error) {
		return ui.El("header", nil, ui.Text("public header"), n), nil
	}
	app := testApp(t, Config{Routes: []Route{{Pattern: "/private", Page: static("secret page"), Layouts: []LayoutFunc{layout, func(*Context, ui.Node) (ui.Node, error) { failedLayoutCalls++; return nil, ErrForbidden }, layout}, Access: []AccessBoundary{
		{Layouts: 1, Forbidden: func(c *Context) (Page, error) {
			_ = c.SetStatus(200)
			return Page{Title: "custom denial", Meta: map[string]string{"RoBoTs": "index"}, Metadata: seo.Metadata{Robots: "index"}, Stream: true, Body: ui.Text("outer denial")}, nil
		}},
		{Layouts: 3, Forbidden: func(*Context) (Page, error) {
			deepBoundaryCalls++
			return Page{Body: ui.Text("unsafe inner boundary")}, nil
		}},
	}}}})
	response := request(app, "/private", true)
	var data Envelope
	err := json.Unmarshal(response.Body.Bytes(), &data)
	if err != nil || data.Status != 403 || response.Code != 403 || data.HTML != "<header>public headerouter denial</header>" || data.Title != "custom denial" || failedLayoutCalls != 1 || deepBoundaryCalls != 0 {
		t.Fatalf("layout denial: %+v %v calls=%d/%d", data, err, failedLayoutCalls, deepBoundaryCalls)
	}
	if len(data.Meta) != 0 || len(data.Head) != 1 || data.Head[0].Attrs["content"] != "noindex" {
		t.Fatal("crawler directives", data.Meta, data.Head)
	}
}

func TestAccessBoundaryFallbackAndFailures(t *testing.T) {
	for _, mode := range []string{"global", "error", "panic"} {
		boundary := func(*Context) (Page, error) {
			if mode == "panic" {
				panic("private panic")
			}
			if mode == "error" {
				return Page{Body: ui.Text("private body")}, errors.New("private error")
			}
			return Page{Body: ui.Text("global denial")}, nil
		}
		app := testApp(t, Config{Unauthorized: boundary, Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) { return Page{}, ErrUnauthorized }, Access: []AccessBoundary{{Forbidden: static("unused")}}}}})
		w := request(app, "/", true)
		want := 401
		if mode != "global" {
			want = 500
		}
		if w.Code != want || strings.Contains(w.Body.String(), "private") {
			t.Fatal(mode, w.Code, w.Body.String())
		}
		if mode == "global" && !strings.Contains(w.Body.String(), "global denial") {
			t.Fatal("global fallback missing")
		}
	}
}

func TestAccessDenialsAreNeverRetainedAsPublicPageSnapshots(t *testing.T) {
	var calls atomic.Int32
	app := testApp(t, Config{Routes: []Route{{Pattern: "/", Cache: &CachePolicy{TTL: time.Minute}, Page: func(*Context) (Page, error) { calls.Add(1); return Page{}, ErrUnauthorized }}}})
	for i := 0; i < 3; i++ {
		w := request(app, "/", true)
		if w.Code != 401 || w.Header().Get("X-Nextgo-Prefetch") != "" {
			t.Fatal(w.Code, w.Header())
		}
	}
	if calls.Load() != 3 {
		t.Fatal("denial cached", calls.Load())
	}
}

func TestRejectInvalidAccessBoundaryRegistration(t *testing.T) {
	for _, boundaries := range [][]AccessBoundary{{{Layouts: -1, Unauthorized: static("x")}}, {{Layouts: 2, Unauthorized: static("x")}}, {{}}, {{Layouts: 1, Unauthorized: static("x")}, {Layouts: 0, Forbidden: static("x")}}} {
		if _, err := New(Config{Routes: []Route{{Pattern: "/", Page: static("x"), Layouts: []LayoutFunc{func(_ *Context, n ui.Node) (ui.Node, error) { return n, nil }}, Access: boundaries}}}); err == nil {
			t.Fatal("invalid prefix accepted", boundaries)
		}
	}
	if _, err := New(Config{Routes: []Route{{Pattern: "/", API: func(http.ResponseWriter, *http.Request) {}, Access: []AccessBoundary{{Unauthorized: static("x")}}}}}); err == nil {
		t.Fatal("page boundary accepted on API")
	}
}
