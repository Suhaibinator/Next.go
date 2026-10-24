package nextgo_test

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

// BenchmarkHTTP measures the handler without network/socket latency. It counts
// response writes instead of retaining the body, so a recorder's buffer growth
// does not dominate rendering allocations. Loader/database latency is excluded.
func BenchmarkHTTP(b *testing.B) {
	for _, scenario := range []struct {
		name       string
		items      int
		navigation bool
		cache      bool
		gzip       bool
	}{
		{name: "TinySSR", items: 1},
		{name: "HundredItemSSR", items: 100},
		{name: "HundredItemEnvelope", items: 100, navigation: true},
		{name: "WarmCachedEnvelope", items: 100, navigation: true, cache: true},
		{name: "GzipEnvelope", items: 100, navigation: true, gzip: true},
	} {
		b.Run(scenario.name, func(b *testing.B) {
			route := nextgo.Route{Pattern: "/", Page: func(*nextgo.Context) (nextgo.Page, error) {
				children := make([]ui.Node, 0, scenario.items)
				for index := 0; index < scenario.items; index++ {
					children = append(children, ui.El("li", nil,
						ui.El("a", ui.Attrs{"href": "/posts/" + strconv.Itoa(index)}, ui.Text("A representative server-rendered post "+strconv.Itoa(index))),
						ui.El("p", nil, ui.Text("Escaped content & metadata rendered in Go."))))
				}
				return nextgo.Page{Title: "Reference benchmark", Description: "Server rendering and navigation envelope benchmark.", Body: ui.El("main", nil, ui.El("ul", nil, children...))}, nil
			}}
			if scenario.cache {
				route.Cache = &nextgo.CachePolicy{TTL: time.Minute}
			}
			app, err := nextgo.New(nextgo.Config{BuildID: "benchmark", Routes: []nextgo.Route{route}, Logger: log.New(io.Discard, "", 0)})
			if err != nil {
				b.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			if scenario.navigation {
				request.Header.Set("X-Nextgo-Navigate", "1")
			}
			if scenario.gzip {
				request.Header.Set("Accept-Encoding", "gzip")
			}
			warm := httptest.NewRecorder()
			app.ServeHTTP(warm, request)
			if warm.Code != http.StatusOK || warm.Body.Len() == 0 {
				b.Fatalf("warmup status=%d bytes=%d", warm.Code, warm.Body.Len())
			}
			if scenario.gzip && warm.Header().Get("Content-Encoding") != "gzip" {
				b.Fatal("gzip benchmark did not negotiate compression")
			}
			b.SetBytes(int64(warm.Body.Len()))
			b.ReportAllocs()
			b.ResetTimer()
			for iteration := 0; iteration < b.N; iteration++ {
				writer := &benchmarkWriter{header: make(http.Header)}
				app.ServeHTTP(writer, request)
				if writer.status != http.StatusOK || writer.bytes == 0 {
					b.Fatalf("status=%d bytes=%d", writer.status, writer.bytes)
				}
			}
		})
	}
}

type benchmarkWriter struct {
	header http.Header
	status int
	bytes  int
}

func (w *benchmarkWriter) Header() http.Header { return w.header }
func (w *benchmarkWriter) WriteHeader(status int) {
	if w.status == 0 {
		w.status = status
	}
}
func (w *benchmarkWriter) Write(body []byte) (int, error) {
	if w.status == 0 {
		w.WriteHeader(http.StatusOK)
	}
	w.bytes += len(body)
	return len(body), nil
}
