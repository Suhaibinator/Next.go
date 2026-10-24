package nextgo

import (
	"bytes"
	"context"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/scripts"
	"github.com/Suhaibinator/Next.go/ui"
	"html"
	"io"
	"net/http"
	"strings"
)

type requestURLKey struct{}

func requestURI(r *http.Request) string {
	if value, ok := r.Context().Value(requestURLKey{}).(string); ok {
		return value
	}
	return r.URL.RequestURI()
}

type initialStreamWriter struct {
	http.ResponseWriter
	pending bytes.Buffer
	ready   bool
	err     error
	cancel  context.CancelFunc
}

func (w *initialStreamWriter) Write(data []byte) (int, error) {
	if w.err != nil {
		return 0, w.err
	}
	if !w.ready {
		return w.pending.Write(data)
	}
	return w.ResponseWriter.Write(data)
}

func (w *initialStreamWriter) Flush() {
	if w.err != nil {
		return
	}
	if !w.ready {
		w.pending.WriteString(`</div><span id="nextgo-initial-ready" hidden></span>`)
		w.ready = true
		n, err := w.ResponseWriter.Write(w.pending.Bytes())
		if err == nil && n != w.pending.Len() {
			err = io.ErrShortWrite
		}
		w.err = err
		w.pending.Reset()
		if err != nil {
			if w.cancel != nil {
				w.cancel()
			}
			return
		}
	}
	if flush, ok := w.ResponseWriter.(http.Flusher); ok {
		flush.Flush()
	}
}
func (a *App) respondStream(w http.ResponseWriter, r *http.Request, p Page, head string, document Document) {
	a.draftResponse(w, r)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if flag, _ := r.Context().Value(parallelRequestKey{}).(bool); flag || a.draftState(r) == "1" || len(w.Header().Values("Set-Cookie")) > 0 || strings.Contains(strings.ToLower(w.Header().Get("Cache-Control")), "private") {
		w.Header().Set("Cache-Control", "private, no-store")
	} else {
		w.Header().Set("Cache-Control", "no-store")
	}
	w.Header().Set(navigation.ConfigurationHeader, a.configurationID)
	w.Header().Add("Vary", "X-Nextgo-Navigate, X-Nextgo-Stream, X-Nextgo-Purpose")
	clearNavigationReuse(w.Header())
	nonceResponse(w, r)
	nonce := scripts.Nonce(r.Context())
	nonceAttr := ""
	if nonce != "" {
		nonceAttr = ` nonce="` + html.EscapeString(nonce) + `"`
	}
	var b strings.Builder
	b.WriteString(`<!doctype html><html lang="` + html.EscapeString(document.Language) + `" dir="` + html.EscapeString(document.Direction) + `"><head><meta charset="utf-8"><meta name="viewport" content="` + html.EscapeString(document.Viewport) + `"><title>` + html.EscapeString(p.Title) + `</title><meta name="description" content="` + html.EscapeString(p.Description) + `">`)
	for key, value := range p.Meta {
		b.WriteString(`<meta data-nextgo-meta name="` + html.EscapeString(key) + `" content="` + html.EscapeString(value) + `">`)
	}
	b.WriteString(nonceHead(head, nonce) + `<style` + nonceAttr + `>` + frameworkCSS + `</style><link rel="stylesheet" href="` + html.EscapeString(mustPath(a.paths.AssetURL("/style.css"))) + `">` + a.earlyScripts(r) + `<script` + nonceAttr + ` src="` + html.EscapeString(a.paths.RuntimeURL(a.config.BuildID)) + `wasm_exec.js"></script><script data-nextgo-bootstrap=""` + nonceAttr + ` async src="` + html.EscapeString(a.paths.RuntimeURL(a.config.BuildID)) + `boot.js"></script></head><body data-nextgo-basepath="` + html.EscapeString(a.paths.BasePath) + `" data-nextgo-assets="` + html.EscapeString(a.paths.AssetPrefix) + `" data-nextgo-runtime="` + html.EscapeString(a.paths.RuntimeURL(a.config.BuildID)) + `" data-nextgo-build="` + html.EscapeString(a.config.BuildID) + `"`)
	if a.paths.TrailingSlash {
		b.WriteString(` data-nextgo-trailing-slash="true"`)
	}
	if a.config.Dev {
		b.WriteString(` data-nextgo-dev="true" data-nextgo-dev-generation="` + html.EscapeString(a.config.DevGeneration) + `"`)
	}
	if a.config.PreserveLayouts {
		b.WriteString(` data-nextgo-preserve-layouts="true"`)
	}
	if a.config.ViewTransitions {
		b.WriteString(` data-nextgo-transitions="true"`)
	}
	if state := a.draftState(r); state != "" {
		b.WriteString(` data-nextgo-draft="` + state + `"`)
	}
	b.WriteString(` data-nextgo-configuration="` + a.configurationID + `" data-nextgo-navigation="` + html.EscapeString(a.navigationJSON) + `"`)
	b.WriteString(`><div id="nextgo-status" role="status" aria-live="polite" hidden></div>` + routeAnnouncer + `<div id="nextgo-app" tabindex="-1">`)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	stream := &initialStreamWriter{ResponseWriter: w, cancel: cancel}
	_, _ = io.WriteString(stream, b.String())
	if err := ui.RenderStream(ctx, stream, p.Body); err != nil {
		a.config.Logger.Printf("stream: %v", err)
		if !stream.ready {
			// The shell and tree are still buffered: ordinary error rendering can
			// commit a real 500 without leaking the failed page's initial markup.
			a.respond(w, r, errorPage(), http.StatusInternalServerError)
		}
		return
	}
	if !stream.ready {
		stream.Flush()
	}
	if stream.err != nil {
		a.config.Logger.Printf("stream write: %v", stream.err)
		return
	}
	_, _ = io.WriteString(stream, `<noscript>Deferred content requires WebAssembly. Use a non-streaming page for complete content without scripting.</noscript></body></html>`)
}

func withRequestURI(r *http.Request) (*http.Request, context.CancelFunc) {
	ctx, cancel := context.WithCancel(r.Context())
	return r.Clone(WithRequestMemo(context.WithValue(ctx, requestURLKey{}, r.URL.RequestURI()))), cancel
}
