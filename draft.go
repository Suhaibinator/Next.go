package nextgo

import (
	"bufio"
	"fmt"
	"net"
	"net/http"

	"github.com/Suhaibinator/Next.go/draft"
	"github.com/Suhaibinator/Next.go/internal/navigation"
)

func (a *App) serveDraftProbe(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("X-Robots-Tag", "noindex")
	clearNavigationReuse(w.Header())
	if a.config.Draft == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if a.redirectCanonical(w, r) {
		return
	}
	a.draftResponse(w, r)
	w.Header().Set(navigation.ProtocolHeader, fmt.Sprint(navigation.ProtocolVersion))
	w.Header().Set(navigation.ConfigurationHeader, a.configurationID)
	w.Header().Set(navigation.BuildHeader, a.config.BuildID)
	w.WriteHeader(http.StatusNoContent)
}

func (a *App) draftState(r *http.Request) string {
	if a.config.Draft == nil {
		return ""
	}
	if draft.Enabled(r.Context()) {
		return "1"
	}
	return "0"
}

// Enforce at the final write, including configured API/action handlers and
// redirects that choose their own response headers. Preserve optional transport
// interfaces rather than making a non-streaming writer appear flushable.
type draftWriter struct {
	noindex bool
	http.ResponseWriter
	wrote bool
}

func (w *draftWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
func (w *draftWriter) WriteHeader(status int) {
	if w.wrote {
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	if w.noindex {
		w.Header().Set("X-Robots-Tag", "noindex")
	}
	clearNavigationReuse(w.Header())
	if status >= 200 || status == 101 {
		w.wrote = true
	}
	w.ResponseWriter.WriteHeader(status)
}
func (w *draftWriter) Write(body []byte) (int, error) {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(body)
}

type draftFlushWriter struct{ *draftWriter }

func (w draftFlushWriter) Flush() {
	if !w.wrote {
		w.WriteHeader(200)
	}
	w.ResponseWriter.(http.Flusher).Flush()
}

type draftHijackWriter struct{ *draftWriter }

func (w draftHijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.ResponseWriter.(http.Hijacker).Hijack()
}

type draftFlushHijackWriter struct{ *draftWriter }

func (w draftFlushHijackWriter) Flush() { draftFlushWriter{w.draftWriter}.Flush() }
func (w draftFlushHijackWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.ResponseWriter.(http.Hijacker).Hijack()
}
func privateDraftWriter(w http.ResponseWriter) http.ResponseWriter {
	return privateResponseWriter(w, true)
}
func privateResponseWriter(w http.ResponseWriter, noindex bool) http.ResponseWriter {
	wrapped := &draftWriter{ResponseWriter: w, noindex: noindex}
	_, flush := w.(http.Flusher)
	_, hijack := w.(http.Hijacker)
	if flush && hijack {
		return draftFlushHijackWriter{wrapped}
	}
	if flush {
		return draftFlushWriter{wrapped}
	}
	if hijack {
		return draftHijackWriter{wrapped}
	}
	return wrapped
}

func (a *App) draftResponse(w http.ResponseWriter, r *http.Request) {
	w.Header().Del(navigation.DraftHeader)
	if a.config.Draft == nil {
		return
	}
	w.Header().Set(navigation.DraftHeader, a.draftState(r))
	// HttpOnly sessions can change in another tab. Every soft navigation must
	// reach the server to detect that change before retaining private layout UI.
	clearNavigationReuse(w.Header())
	if draft.Enabled(r.Context()) {
		w.Header().Set("Cache-Control", "private, no-store")
		w.Header().Set("X-Robots-Tag", "noindex")
	}
}
