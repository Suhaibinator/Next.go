package telemetry

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type plainWriter struct {
	header http.Header
	code   int
	output bytes.Buffer
}

type canceledWriter struct{ *plainWriter }

func (w canceledWriter) Write([]byte) (int, error) { return 0, context.Canceled }

func (w *plainWriter) Header() http.Header {
	if w.header == nil {
		w.header = make(http.Header)
	}
	return w.header
}
func (w *plainWriter) WriteHeader(code int) {
	if w.code == 0 && code >= 200 {
		w.code = code
	}
}
func (w *plainWriter) Write(p []byte) (int, error) {
	if w.code == 0 {
		w.code = 200
	}
	return w.output.Write(p)
}

type fullWriter struct {
	*plainWriter
	flushes, pushes, reads, deadlines int
	conn, peer                        net.Conn
}

func (w *fullWriter) Flush()                               { w.flushes++ }
func (w *fullWriter) Push(string, *http.PushOptions) error { w.pushes++; return nil }
func (w *fullWriter) ReadFrom(r io.Reader) (int64, error) {
	w.reads++
	return io.Copy(w.plainWriter, r)
}
func (w *fullWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w.conn, w.peer = net.Pipe()
	return w.conn, bufio.NewReadWriter(bufio.NewReader(w.conn), bufio.NewWriter(w.conn)), nil
}
func (w *fullWriter) SetWriteDeadline(time.Time) error { w.deadlines++; return nil }

func TestHTTPObservationPreservesCapabilitiesStreamingAndController(t *testing.T) {
	full := &fullWriter{plainWriter: &plainWriter{}}
	for _, underlying := range []http.ResponseWriter{&plainWriter{}, httptest.NewRecorder(), full, struct {
		http.ResponseWriter
		io.ReaderFrom
	}{&plainWriter{}, full}} {
		var values records
		tracer := testTracer(t, &values, false)
		tracer.ServeHTTP(underlying, httptest.NewRequest("GET", "/", nil), func(w http.ResponseWriter, r *http.Request) {
			_, wantF := underlying.(http.Flusher)
			_, gotF := w.(http.Flusher)
			_, wantH := underlying.(http.Hijacker)
			_, gotH := w.(http.Hijacker)
			_, wantP := underlying.(http.Pusher)
			_, gotP := w.(http.Pusher)
			_, wantR := underlying.(io.ReaderFrom)
			_, gotR := w.(io.ReaderFrom)
			if wantF != gotF || wantH != gotH || wantP != gotP || wantR != gotR {
				t.Fatal("changed optional writer interfaces")
			}
			w.Header().Set("X-Observed", "yes")
			if _, recorder := underlying.(*httptest.ResponseRecorder); !recorder {
				w.WriteHeader(103)
			}
			if gotF {
				w.(http.Flusher).Flush()
			}
			if gotP {
				_ = w.(http.Pusher).Push("/public.css", nil)
			}
			if gotR {
				_, _ = w.(io.ReaderFrom).ReadFrom(strings.NewReader("hello"))
			} else {
				_, _ = w.Write([]byte("hello"))
			}
			if underlying == full {
				if err := http.NewResponseController(w).SetWriteDeadline(time.Now()); err != nil {
					t.Fatal(err)
				}
			}
		})
		got := values.all()
		if len(got) != 1 || got[0].Status != 200 || got[0].Bytes != 5 || underlying.Header().Get("X-Observed") != "yes" {
			t.Fatal(got)
		}
	}
	if full.flushes != 1 || full.pushes != 1 || full.reads != 2 || full.deadlines != 1 {
		t.Fatal(full)
	}
	var values records
	tracer := testTracer(t, &values, false)
	tracer.ServeHTTP(full, httptest.NewRequest("GET", "/", nil), func(w http.ResponseWriter, _ *http.Request) {
		connection, _, err := w.(http.Hijacker).Hijack()
		if err != nil {
			t.Fatal(err)
		}
		_ = connection.Close()
		_ = full.peer.Close()
	})
	if got := values.all(); len(got) != 1 || !got[0].Hijacked {
		t.Fatal(got)
	}
}

func TestRequestPanicAndCancellationRecordGenericFailures(t *testing.T) {
	var values records
	tracer := testTracer(t, &values, false)
	func() {
		defer func() {
			if recover() != "private panic" {
				t.Error("panic semantics changed")
			}
		}()
		tracer.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil), func(http.ResponseWriter, *http.Request) { panic("private panic") })
	}()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tracer.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil).WithContext(ctx), func(http.ResponseWriter, *http.Request) {})
	got := values.all()
	if len(got) != 2 || got[0].Error != ErrorPanic || got[0].Status != 500 || got[1].Error != ErrorCanceled {
		t.Fatal(got)
	}
	tracer.ServeHTTP(canceledWriter{&plainWriter{}}, httptest.NewRequest("GET", "/", nil), func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("body")) })
	if got := values.all(); got[2].Error != ErrorCanceled || got[2].Bytes != 0 {
		t.Fatal(got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestTransportClonesHeadersPropagatesIdentityAndEndsOnBodyCompletion(t *testing.T) {
	var values records
	tracer := testTracer(t, &values, false)
	ctx, root := tracer.Start(context.Background(), Info{Name: "root"})
	request, _ := http.NewRequestWithContext(ctx, "GET", "https://user:private-password@backend.invalid/path?secret=value", nil)
	request.Header.Set("Traceparent", "untrusted-original")
	request.Header.Set("Tracestate", "private-state")
	request.Header.Set("Authorization", "private-token")
	transport := Transport{Base: roundTripFunc(func(clone *http.Request) (*http.Response, error) {
		if clone == request || clone.Header.Get("Traceparent") == "untrusted-original" || clone.Header.Get("Tracestate") != "" || clone.Header.Get("Authorization") != "private-token" {
			t.Fatal("request clone/propagation failed")
		}
		parent, ok := ParseTraceparent(clone.Header.Get("Traceparent"))
		if !ok || parent.TraceID != Context(ctx).TraceID || parent.SpanID == Context(ctx).SpanID {
			t.Fatal(parent)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader("private-body"))}, nil
	})}
	response, err := transport.RoundTrip(request)
	if err != nil {
		t.Fatal(err)
	}
	if len(values.all()) != 0 {
		t.Fatal("client span ended before body completion")
	}
	_, _ = io.ReadAll(response.Body)
	_ = response.Body.Close()
	_ = response.Body.Close()
	root.End(Result{})
	got := values.all()
	if len(got) != 2 || got[0].Kind != Client || got[0].Bytes != 12 || got[0].ParentID != got[1].SpanID {
		t.Fatal(got)
	}
	if request.Header.Get("Traceparent") != "untrusted-original" || request.Header.Get("Tracestate") != "private-state" {
		t.Fatal("caller headers mutated")
	}
	for _, r := range got {
		if strings.Contains(r.Name+r.Route+r.Method, "private") {
			t.Fatal(r)
		}
	}
	_, err = (Transport{Base: roundTripFunc(func(*http.Request) (*http.Response, error) { return nil, errors.New("private transport failure") })}).RoundTrip(request)
	if err == nil || values.all()[2].Error != ErrorInternal {
		t.Fatal(err, values.all())
	}
}
