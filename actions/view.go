package actions

import "net/http"

const viewUnknownOutcome = "The action outcome could not be confirmed. Reconcile before retrying."

// ViewSuccess is a successfully encoded action result, including its already
// deployment-resolved redirect. Effects are copies of the handler's selectors;
// the router's existing OnRevalidate callback has already run exactly once.
// A renderer may acknowledge additional fallible/shared expiry before loading a
// current view. It must never rerun the mutation if rendering or transport fails.
type ViewSuccess struct {
	Encoded                         []byte
	Redirect                        string
	RevalidateTags, RevalidatePaths []string
}

// ServeWithView runs the same origin, method, revision, body, handler, encoding
// and invalidation pipeline as ServeHTTP, then delegates a bounded successful
// result to render. Errors always use ordinary typed JSON and never call render,
// including multipart requests whose Accept negotiates only the view protocol.
// The caller's request and headers are not modified. Rendering owns the response
// format, flushing and view errors.
//
// The successful encoded result is limited to 64 KiB. Exceeding that limit can
// follow a committed mutation; existing invalidation effects still run. No
// transport/rendering failure implies rollback or makes retry safe. A callback
// panic before output receives a generic unknown-outcome 500. After output has
// started, it is logged without appending incompatible JSON to the partial view.
func (r *Router) ServeWithView(w http.ResponseWriter, req *http.Request, render func(http.ResponseWriter, *http.Request, ViewSuccess)) {
	viewRequest := req.Clone(req.Context())
	viewRequest.Header = req.Header.Clone()
	if viewRequest.Header == nil {
		viewRequest.Header = make(http.Header)
	}
	viewRequest.Header.Set("Accept", "application/json")
	r.serve(w, viewRequest, render, true)
}

func (r *Router) resultError(w http.ResponseWriter, req *http.Request, err error, view bool) {
	if !view {
		r.handleError(w, req, err)
		return
	}
	r.options.LogError(req, err)
	r.fail(w, req, http.StatusInternalServerError, &Error{Code: "internal_error", Message: viewUnknownOutcome})
}

type viewResponseWriter struct {
	http.ResponseWriter
	started bool
}

func (w *viewResponseWriter) WriteHeader(status int) {
	w.started = true
	w.ResponseWriter.WriteHeader(status)
}
func (w *viewResponseWriter) Write(data []byte) (int, error) {
	w.started = true
	return w.ResponseWriter.Write(data)
}
func (w *viewResponseWriter) Flush() {
	w.started = true
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}
func (w *viewResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
