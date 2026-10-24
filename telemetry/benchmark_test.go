package telemetry

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func BenchmarkHTTPTracing(b *testing.B) {
	for _, mode := range []string{"disabled", "record", "unsampled"} {
		b.Run(mode, func(b *testing.B) {
			var tracer *Tracer
			if mode != "disabled" {
				tracer, _ = New(Options{Recorder: JSONRecorder(io.Discard), TrustIncoming: mode == "unsampled"})
			}
			request := httptest.NewRequest("GET", "http://example.test/posts/a", nil)
			if mode == "unsampled" {
				request.Header.Set("Traceparent", "00-0123456789abcdef0123456789abcdef-0123456789abcdef-00")
			}
			next := func(w http.ResponseWriter, r *http.Request) {
				SetRoute(r.Context(), "/posts/:slug")
				_, _ = w.Write([]byte("meaningful HTML"))
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tracer.ServeHTTP(httptest.NewRecorder(), request, next)
			}
		})
	}
}
