package telemetry

import (
	"context"
	"errors"
	"io"
	"net/http"
	"sync"
)

// Inject supplies only the current version-00 traceparent. Stale tracestate is
// removed when replacing an identity. Baggage is never copied automatically.
func Inject(ctx context.Context, headers http.Header) {
	if parent := Traceparent(ctx); parent != "" {
		headers.Set("Traceparent", parent)
		headers.Del("Tracestate")
	}
}

// Transport adds client spans and traceparent to requests in a traced context.
// It clones requests/headers and ends on body EOF/Close, error, or no-body result.
// Callers must close response bodies, just as with the standard HTTP client.
// It records neither destination URLs nor request/response headers or bodies.
type Transport struct{ Base http.RoundTripper }

func (t Transport) RoundTrip(request *http.Request) (*http.Response, error) {
	base := t.Base
	if base == nil {
		base = http.DefaultTransport
	}
	if request == nil {
		return nil, errors.New("telemetry: nil HTTP request")
	}
	if !Active(request.Context()) {
		return base.RoundTrip(request)
	}
	ctx, span := Start(request.Context(), Info{Name: "nextgo.http", Kind: Client, Method: request.Method})
	clone := request.Clone(ctx)
	clone.Header = request.Header.Clone()
	if clone.Header == nil {
		clone.Header = make(http.Header)
	}
	Inject(ctx, clone.Header)
	completed := false
	defer func() {
		if !completed {
			if failure := recover(); failure != nil {
				span.End(Result{Error: ErrorPanic})
				panic(failure)
			}
			span.End(Result{Error: ErrorAborted})
		}
	}()
	response, err := base.RoundTrip(clone)
	completed = true
	if err != nil {
		span.End(Result{Error: Classify(err)})
		return response, err
	}
	if response == nil {
		span.End(Result{Error: ErrorInternal})
		return nil, errors.New("telemetry: transport returned no response")
	}
	if response.Body == nil || response.Body == http.NoBody {
		span.End(Result{Status: response.StatusCode})
		return response, nil
	}
	response.Body = &body{ReadCloser: response.Body, span: span, status: response.StatusCode}
	return response, nil
}

type body struct {
	io.ReadCloser
	mu      sync.Mutex
	span    *Span
	status  int
	bytes   int64
	failure ErrorKind
}

func (b *body) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.mu.Lock()
	b.bytes += int64(n)
	if err != nil && err != io.EOF {
		b.failure = Classify(err)
	}
	result := Result{Status: b.status, Bytes: b.bytes, Error: b.failure}
	b.mu.Unlock()
	if err != nil {
		b.span.End(result)
	}
	return n, err
}
func (b *body) Close() error {
	err := b.ReadCloser.Close()
	b.mu.Lock()
	if err != nil {
		b.failure = Classify(err)
	}
	result := Result{Status: b.status, Bytes: b.bytes, Error: b.failure}
	b.mu.Unlock()
	b.span.End(result)
	return err
}
