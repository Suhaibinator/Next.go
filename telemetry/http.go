package telemetry

import (
	"bufio"
	"context"
	"io"
	"net"
	"net/http"
)

// ServeHTTP traces one application request. Nested locale mounts using this
// tracer share its root. Request bytes mean accepted response-writer bytes;
// this does not certify delivery or platform publication.
func (t *Tracer) ServeHTTP(w http.ResponseWriter, r *http.Request, next func(http.ResponseWriter, *http.Request)) {
	if t == nil {
		next(w, r)
		return
	}
	if state, _ := r.Context().Value(requestKey{}).(*requestState); state != nil && state.tracer == t {
		next(w, r)
		return
	}
	ctx := context.WithValue(r.Context(), tracerKey{}, t)
	if t.options.TrustIncoming && Context(ctx).TraceID == "" {
		values := r.Header.Values("Traceparent")
		if len(values) == 1 {
			if parent, ok := ParseTraceparent(values[0]); ok {
				ctx = context.WithValue(ctx, spanKey{}, spanState{t, parent})
			}
		}
	}
	ctx = context.WithValue(ctx, requestKey{}, &requestState{tracer: t})
	handle(w, r.WithContext(ctx), Info{Name: "nextgo.request", Kind: Server, Method: r.Method}, next)
}

// Handle adds a child handler span while preserving optional HTTP interfaces.
func Handle(w http.ResponseWriter, r *http.Request, info Info, next func(http.ResponseWriter, *http.Request)) {
	if !Active(r.Context()) {
		next(w, r)
		return
	}
	if info.Method == "" {
		info.Method = r.Method
	}
	handle(w, r, info, next)
}
func handle(w http.ResponseWriter, r *http.Request, info Info, next func(http.ResponseWriter, *http.Request)) {
	ctx, span := Start(r.Context(), info)
	response := &response{underlying: w}
	completed := false
	defer func() {
		result := Result{Status: response.status, Bytes: response.bytes, Hijacked: response.hijacked}
		if !completed {
			result.Error = ErrorAborted
			if result.Status == 0 {
				result.Status = 500
			}
		}
		if result.Status == 0 && !result.Hijacked {
			result.Status = 200
		}
		if failure := recover(); failure != nil {
			result.Error = ErrorPanic
			span.End(result)
			panic(failure)
		}
		if result.Error == ErrorNone {
			result.Error = Classify(ctx.Err())
		}
		if response.writeError != ErrorNone && result.Error == ErrorNone {
			result.Error = response.writeError
		}
		span.End(result)
	}()
	next(capabilities(response), r.WithContext(ctx))
	completed = true
}

type response struct {
	underlying http.ResponseWriter
	status     int
	bytes      int64
	hijacked   bool
	writeError ErrorKind
}

func (w *response) Header() http.Header         { return w.underlying.Header() }
func (w *response) Unwrap() http.ResponseWriter { return w.underlying }
func (w *response) WriteHeader(status int) {
	w.underlying.WriteHeader(status)
	if w.status == 0 && (status >= 200 || status == 101) {
		w.status = status
	}
}
func (w *response) Write(body []byte) (int, error) {
	n, err := w.underlying.Write(body)
	if w.status == 0 {
		w.status = 200
	}
	w.bytes += int64(n)
	if err != nil {
		w.writeError = Classify(err)
	}
	return n, err
}

type flushFunc func()

func (f flushFunc) Flush() { f() }

type hijackFunc func() (net.Conn, *bufio.ReadWriter, error)

func (f hijackFunc) Hijack() (net.Conn, *bufio.ReadWriter, error) { return f() }

type pushFunc func(string, *http.PushOptions) error

func (f pushFunc) Push(target string, options *http.PushOptions) error { return f(target, options) }

type readFromFunc func(io.Reader) (int64, error)

func (f readFromFunc) ReadFrom(r io.Reader) (int64, error) { return f(r) }

// Compose only interfaces actually supported by the underlying writer. Unwrap
// keeps ResponseController deadlines/full-duplex access available too.
func capabilities(w *response) http.ResponseWriter {
	bits := 0
	var flush http.Flusher
	var hijack http.Hijacker
	var push http.Pusher
	var readFrom io.ReaderFrom
	if f, ok := w.underlying.(http.Flusher); ok {
		bits |= 1
		flush = flushFunc(func() {
			f.Flush()
			if w.status == 0 {
				w.status = 200
			}
		})
	}
	if h, ok := w.underlying.(http.Hijacker); ok {
		bits |= 2
		hijack = hijackFunc(func() (net.Conn, *bufio.ReadWriter, error) {
			c, r, err := h.Hijack()
			if err == nil {
				w.hijacked = true
			}
			return c, r, err
		})
	}
	if p, ok := w.underlying.(http.Pusher); ok {
		bits |= 4
		push = pushFunc(p.Push)
	}
	if f, ok := w.underlying.(io.ReaderFrom); ok {
		bits |= 8
		readFrom = readFromFunc(func(r io.Reader) (int64, error) {
			n, err := f.ReadFrom(r)
			if w.status == 0 {
				w.status = 200
			}
			w.bytes += n
			if err != nil {
				w.writeError = Classify(err)
			}
			return n, err
		})
	}
	switch bits {
	case 1:
		return &struct {
			*response
			http.Flusher
		}{w, flush}
	case 2:
		return &struct {
			*response
			http.Hijacker
		}{w, hijack}
	case 3:
		return &struct {
			*response
			http.Flusher
			http.Hijacker
		}{w, flush, hijack}
	case 4:
		return &struct {
			*response
			http.Pusher
		}{w, push}
	case 5:
		return &struct {
			*response
			http.Flusher
			http.Pusher
		}{w, flush, push}
	case 6:
		return &struct {
			*response
			http.Hijacker
			http.Pusher
		}{w, hijack, push}
	case 7:
		return &struct {
			*response
			http.Flusher
			http.Hijacker
			http.Pusher
		}{w, flush, hijack, push}
	case 8:
		return &struct {
			*response
			io.ReaderFrom
		}{w, readFrom}
	case 9:
		return &struct {
			*response
			http.Flusher
			io.ReaderFrom
		}{w, flush, readFrom}
	case 10:
		return &struct {
			*response
			http.Hijacker
			io.ReaderFrom
		}{w, hijack, readFrom}
	case 11:
		return &struct {
			*response
			http.Flusher
			http.Hijacker
			io.ReaderFrom
		}{w, flush, hijack, readFrom}
	case 12:
		return &struct {
			*response
			http.Pusher
			io.ReaderFrom
		}{w, push, readFrom}
	case 13:
		return &struct {
			*response
			http.Flusher
			http.Pusher
			io.ReaderFrom
		}{w, flush, push, readFrom}
	case 14:
		return &struct {
			*response
			http.Hijacker
			http.Pusher
			io.ReaderFrom
		}{w, hijack, push, readFrom}
	case 15:
		return &struct {
			*response
			http.Flusher
			http.Hijacker
			http.Pusher
			io.ReaderFrom
		}{w, flush, hijack, push, readFrom}
	default:
		return w
	}
}
