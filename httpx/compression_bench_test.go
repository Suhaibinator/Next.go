package httpx

import (
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// BenchmarkCompression exercises a representative 100-item navigation JSON
// response, including middleware/header costs, without retaining output bodies.
func BenchmarkCompression(b *testing.B) {
	payload := []byte(`{"html":"` + strings.Repeat(`<article><h2>A useful post</h2><p>Server-rendered HTML, loaded from Go data.</p></article>`, 100) + `","title":"Posts","status":200}`)
	for _, encoding := range []string{"identity", "gzip"} {
		b.Run(encoding, func(b *testing.B) {
			handler := Compression(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
				_, _ = w.Write(payload)
			}))
			request := httptest.NewRequest(http.MethodGet, "/posts", nil)
			request.Header.Set("Accept-Encoding", encoding)
			writer := &discardResponse{header: make(http.Header)}
			b.ReportAllocs()
			b.SetBytes(int64(len(payload)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				clear(writer.header)
				handler.ServeHTTP(writer, request)
			}
		})
	}
}

// The fresh writer baseline stays alongside the pooled implementation to make
// allocation regressions measurable without relying on old benchmark reports.
func BenchmarkFreshGzipWriter(b *testing.B) {
	payload := []byte(strings.Repeat("representative HTML and JSON response data\n", 256))
	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for i := 0; i < b.N; i++ {
		writer := gzip.NewWriter(io.Discard)
		_, _ = writer.Write(payload)
		_ = writer.Close()
	}
}

func BenchmarkPooledGzipWriter(b *testing.B) {
	payload := []byte(strings.Repeat("representative HTML and JSON response data\n", 256))
	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	for i := 0; i < b.N; i++ {
		writer := compressors.get(io.Discard)
		_, _ = writer.Write(payload)
		_ = writer.Close()
		compressors.put(writer)
	}
}

func BenchmarkConcurrentCompression(b *testing.B) {
	payload := []byte(strings.Repeat(`{"title":"A post","body":"representative navigation payload"}`, 100))
	handler := Compression(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(payload)
	}))
	request := httptest.NewRequest(http.MethodGet, "/posts", nil)
	request.Header.Set("Accept-Encoding", "gzip")
	b.ReportAllocs()
	b.SetBytes(int64(len(payload)))
	b.RunParallel(func(pb *testing.PB) {
		writer := &discardResponse{header: make(http.Header)}
		for pb.Next() {
			clear(writer.header)
			handler.ServeHTTP(writer, request)
		}
	})
}

type discardResponse struct{ header http.Header }

func (w *discardResponse) Header() http.Header         { return w.header }
func (w *discardResponse) WriteHeader(int)             {}
func (w *discardResponse) Write(p []byte) (int, error) { return len(p), nil }
