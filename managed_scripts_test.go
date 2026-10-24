package nextgo

import (
	"context"
	"fmt"
	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/scripts"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEarlyScriptsFreezeAndParticipateInNavigationIdentity(t *testing.T) {
	input := []scripts.Script{{ID: "early", Src: "/library.js", Asset: true, Strategy: scripts.BeforeInteractive}}
	a, err := New(Config{EarlyScripts: input, Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) { return Page{Body: ui.Text("hello")}, nil }}}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close(context.Background())
	input[0].Src = "/changed.js"
	response := httptest.NewRecorder()
	a.ServeHTTP(response, httptest.NewRequest("GET", "/", nil))
	html := response.Body.String()
	if strings.Contains(html, "changed.js") || strings.Index(html, "/assets/library.js") > strings.Index(html, "wasm_exec.js") {
		t.Fatal("early config was mutable or ordered after runtime", html)
	}
	b, err := New(Config{EarlyScripts: input, Routes: a.routes})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close(context.Background())
	if a.configurationID == b.configurationID {
		t.Fatal("early config absent from navigation identity")
	}
	for _, invalid := range [][]scripts.Script{{{ID: "x", Src: "/x.js"}}, {{ID: "x", Src: "/x.js", Strategy: scripts.BeforeInteractive}, {ID: "x", Src: "/y.js", Strategy: scripts.BeforeInteractive}}} {
		if _, err := New(Config{EarlyScripts: invalid}); err == nil {
			t.Fatal("accepted invalid early config")
		}
	}
}
func TestNonceDocumentsBypassPublicCachesAndBindFrameworkAndStructuredData(t *testing.T) {
	store := cache.New(8)
	calls := 0
	middleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			nonce := fmt.Sprintf("synthetic_nonce_%016d", calls)
			ctx, err := scripts.WithNonce(r.Context(), nonce)
			if err != nil {
				t.Fatal(err)
			}
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
	data, _ := seo.JSONLD(map[string]any{"@context": "https://schema.org", "@type": "WebPage"})
	a, err := New(Config{Cache: store, Middleware: []func(http.Handler) http.Handler{middleware}, Routes: []Route{{Pattern: "/", Cache: &CachePolicy{TTL: time.Minute}, Page: func(*Context) (Page, error) {
		calls++
		return Page{Body: ui.Text("nonce page"), JSONLD: []seo.StructuredData{data}}, nil
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close(context.Background())
	for i := 0; i < 2; i++ {
		response := httptest.NewRecorder()
		a.ServeHTTP(response, httptest.NewRequest("GET", "/", nil))
		nonce := fmt.Sprintf("synthetic_nonce_%016d", i)
		if response.Code != 200 || response.Header().Get("Cache-Control") != "private, no-store" || response.Header().Get(scripts.NonceHeader) != "1" || strings.Count(response.Body.String(), `nonce="`+nonce+`"`) != 4 {
			t.Fatal("nonce document contract", response.Code, response.Header(), response.Body.String())
		}
	}
	if calls != 2 {
		t.Fatal("nonce page reused a public cached render", calls)
	}
	request := httptest.NewRequest("GET", "/", nil)
	request.Header.Set("X-Nextgo-Navigate", "1")
	response := httptest.NewRecorder()
	a.ServeHTTP(response, request)
	if response.Header().Get(scripts.NonceHeader) != "1" || strings.Contains(response.Body.String(), "synthetic_nonce_") {
		t.Fatal("nonce leaked into navigation data", response.Body.String())
	}
}

func TestNonceStreamingShellIncludesEarlyScriptsAndRuntimeNonces(t *testing.T) {
	nonce := "stream_nonce_1234567890"
	a, err := New(Config{EarlyScripts: []scripts.Script{{ID: "early", Src: "/early.js", Asset: true, Strategy: scripts.BeforeInteractive}}, Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) {
		return Page{Stream: true, Body: ui.Defer("test", ui.Text("pending"), func(context.Context) (ui.Node, error) { return ui.Text("loaded"), nil })}, nil
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close(context.Background())
	request := httptest.NewRequest("GET", "/", nil)
	ctx, _ := scripts.WithNonce(request.Context(), nonce)
	request = request.WithContext(ctx)
	response := httptest.NewRecorder()
	a.ServeHTTP(response, request)
	if response.Code != 200 || response.Header().Get(scripts.NonceHeader) != "1" || strings.Count(response.Body.String(), `nonce="`+nonce+`"`) != 4 || strings.Index(response.Body.String(), "/assets/early.js") > strings.Index(response.Body.String(), "wasm_exec.js") {
		t.Fatal("stream nonce shell", response.Code, response.Body.String())
	}
}
