package nextgo

import (
	"context"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/scripts"
	"github.com/Suhaibinator/Next.go/ui"
)

// This verifies the serialization/flush contract. Actual execution before a
// private region completes is exercised by the browser bootstrap regression.
func assertBootstrapOrder(t *testing.T, markup, runtime, nonce string) {
	t.Helper()
	ready := strings.Index(markup, `<span id="nextgo-initial-ready" hidden></span>`)
	sdk := strings.Index(markup, `src="`+runtime+`wasm_exec.js"`)
	boot := strings.Index(markup, `src="`+runtime+`boot.js"`)
	headEnd := strings.Index(markup, "</head>")
	if ready < 0 || sdk < ready || boot < sdk || headEnd < 0 || headEnd > ready {
		t.Fatalf("runtime does not follow complete shell in parser order: %s", markup)
	}
	if strings.Count(markup, `src="`+runtime+`wasm_exec.js"`) != 1 || strings.Count(markup, `src="`+runtime+`boot.js"`) != 1 || strings.Count(markup, `data-nextgo-bootstrap=""`) != 1 {
		t.Fatal("runtime scripts repeated or omitted", markup)
	}
	for _, tag := range regexp.MustCompile(`<script\b[^>]*>`).FindAllString(markup, -1) {
		if !strings.Contains(tag, "wasm_exec.js") && !strings.Contains(tag, "boot.js") {
			continue
		}
		if regexp.MustCompile(`\s(?:async|defer)(?:\s|=|>)`).MatchString(tag) || strings.Contains(tag, `type="module"`) {
			t.Fatal("runtime script is not parser-ordered", tag)
		}
		if nonce != "" && !strings.Contains(tag, `nonce="`+nonce+`"`) {
			t.Fatal("runtime lost request nonce", tag)
		}
	}
	if !strings.HasSuffix(strings.TrimSpace(markup[:ready]), `</div>`) {
		t.Fatal("ready marker precedes completed app root")
	}
	early := strings.Index(markup, "early-contract.js")
	if early < 0 || early > headEnd || early > sdk {
		t.Fatal("early managed dependency lost head/parser ordering", markup)
	}
}

func TestBootstrapBufferedDocumentsPreserveParserOrderAndNonce(t *testing.T) {
	for _, nonce := range []string{"", "bootstrap_nonce_1234567890"} {
		t.Run(nonce, func(t *testing.T) {
			a := testApp(t, Config{BuildID: "bootstrap-contract", BasePath: "/docs", AssetPrefix: "https://cdn.example.test/static", EarlyScripts: []scripts.Script{{ID: "early", Src: "/early-contract.js", Asset: true, Strategy: scripts.BeforeInteractive}}, Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) {
				return Page{Title: "Initial title", Body: ui.El("main", nil, ui.Text("meaningful initial HTML"))}, nil
			}}}})
			defer a.Close(context.Background())
			request := httptest.NewRequest("GET", "/docs", nil)
			if nonce != "" {
				ctx, err := scripts.WithNonce(request.Context(), nonce)
				if err != nil {
					t.Fatal(err)
				}
				request = request.WithContext(ctx)
			}
			w := httptest.NewRecorder()
			a.ServeHTTP(w, request)
			if w.Code != 200 || !strings.Contains(w.Body.String(), "meaningful initial HTML") {
				t.Fatal(w.Code, w.Body.String())
			}
			assertBootstrapOrder(t, w.Body.String(), "https://cdn.example.test/static/_nextgo/bootstrap-contract/", nonce)
			head := httptest.NewRecorder()
			a.ServeHTTP(head, httptest.NewRequest("HEAD", "/docs", nil))
			if head.Code != 200 || head.Body.Len() != 0 {
				t.Fatal("HEAD emitted document/bootstrap bytes", head.Code, head.Body.String())
			}
		})
	}
}

func TestBootstrapFirstStreamFlushContainsRuntimeBeforePrivateLoaderCompletes(t *testing.T) {
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	a := testApp(t, Config{BuildID: "bootstrap-stream", BasePath: "/docs", AssetPrefix: "https://cdn.example.test/static", EarlyScripts: []scripts.Script{{ID: "early", Src: "/early-contract.js", Asset: true, Strategy: scripts.BeforeInteractive}}, Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) {
		return Page{Stream: true, Body: ui.Fragment(ui.El("header", nil, ui.Text("completed public shell")), ui.Defer("private-contract", ui.Text("private fallback"), func(ctx context.Context) (ui.Node, error) {
			select {
			case <-release:
				return ui.Text("private final result"), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}), ui.El("footer", nil, ui.Text("shell footer")))}, nil
	}}}})
	defer func() { unblock(); a.Close(context.Background()) }()
	request := httptest.NewRequest("GET", "/docs", nil)
	nonce := "bootstrap_nonce_1234567890"
	ctx, err := scripts.WithNonce(request.Context(), nonce)
	if err != nil {
		t.Fatal(err)
	}
	request = request.WithContext(ctx)
	w := &runtimeStreamRecorder{ResponseRecorder: httptest.NewRecorder(), flushes: make(chan string, 8)}
	done := make(chan struct{})
	go func() { defer close(done); a.ServeHTTP(w, request) }()
	initial := runtimeStreamFlush(t, w)
	assertBootstrapOrder(t, initial, "https://cdn.example.test/static/_nextgo/bootstrap-stream/", nonce)
	if !strings.Contains(initial, "completed public shell") || !strings.Contains(initial, "private fallback") || !strings.Contains(initial, "shell footer") || strings.Contains(initial, `data-nextgo-chunk=`) || strings.Contains(initial, "private final result") {
		t.Fatal("initial bootstrap flush includes incomplete shell or private results", initial)
	}
	select {
	case <-done:
		t.Fatal("private region completed without release")
	default:
	}
	unblock()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("stream did not complete after private loader release")
	}
	final := w.Body.String()
	assertBootstrapOrder(t, final, "https://cdn.example.test/static/_nextgo/bootstrap-stream/", nonce)
	if strings.Index(final, `src="https://cdn.example.test/static/_nextgo/bootstrap-stream/boot.js"`) > strings.Index(final, `<template data-nextgo-chunk="private-contract">`) {
		t.Fatal("runtime was emitted after private stream chunk", final)
	}
}
