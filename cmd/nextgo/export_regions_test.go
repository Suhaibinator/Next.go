package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestExportRequestRegionsRejectsBeforeReadingBodyAndPreservesOutput(t *testing.T) {
	assets, base := exportFixture(t)
	out := filepath.Join(t.TempDir(), "published")
	if err := exportApplication(context.Background(), base, assets, out, "test", []string{"/"}); err != nil {
		t.Fatal(err)
	}
	previous, err := os.ReadFile(filepath.Join(out, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range []string{"X-Nextgo-Request-Regions", "X-Nextgo-Partial"} {
		t.Run(marker, func(t *testing.T) {
			closed := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set(marker, "1")
				w.Header().Set("Content-Type", "text/html")
				w.WriteHeader(200)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				close(closed)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			err := exportApplication(ctx, server.URL, assets, out, "test", []string{"/"})
			if err == nil || !strings.Contains(err.Error(), "request-time regions require") || ctx.Err() != nil {
				t.Fatal(err, ctx.Err())
			}
			select {
			case <-closed:
			case <-ctx.Done():
				t.Fatal("exporter retained the request-time stream")
			}
			after, err := os.ReadFile(filepath.Join(out, "index.html"))
			if err != nil || string(after) != string(previous) {
				t.Fatal("failed publication replaced previous output", err)
			}
		})
	}
}
