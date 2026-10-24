package httpx

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCompressionBytesAndHeaders(t *testing.T) {
	payload := strings.Repeat("server-rendered content\n", 1000)
	for _, status := range []int{200, 201, 400, 404, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			h := Compression(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Header().Set("Content-Length", fmt.Sprint(len(payload)))
				w.Header().Set("ETag", `"content-v1"`)
				w.Header().Set("Content-MD5", "obsolete")
				w.Header().Set("Vary", "Origin")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, payload)
			}))
			r := httptest.NewRequest(http.MethodGet, "/", nil)
			r.Header.Set("Accept-Encoding", "gzip")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			result := w.Result()
			if result.StatusCode != status || result.Header.Get("Content-Encoding") != "gzip" {
				t.Fatalf("response %d: %v", result.StatusCode, result.Header)
			}
			if result.Header.Get("Content-Length") != "" || result.Header.Get("Content-MD5") != "" {
				t.Fatal("representation-specific header retained")
			}
			if result.Header.Get("ETag") != `W/"content-v1"` {
				t.Fatal("strong ETag retained across transformation")
			}
			if got := strings.Join(result.Header.Values("Vary"), ","); got != "Origin,Accept-Encoding" {
				t.Fatalf("Vary = %q", got)
			}
			if got := unzip(t, w.Body.Bytes()); got != payload {
				t.Fatal("decompressed bytes differ")
			}
			if w.Body.Len() >= len(payload) {
				t.Fatal("payload did not compress")
			}
		})
	}
}

func TestNegotiation(t *testing.T) {
	for _, test := range []struct {
		value      string
		compressed bool
		status     int
	}{
		{"", false, 200}, {"br", false, 200}, {"gzip", true, 200}, {"GZip", true, 200},
		{"gzip;q=0", false, 200}, {"gzip;q=0.5", true, 200}, {"gzip;q=0.500", true, 200},
		{"gzip;q=1.000", true, 200}, {"gzip;q=0.5, identity;q=0.8", false, 200},
		{"gzip;q=0.8, identity;q=0.5", true, 200}, {"gzip;q=0, *;q=1", false, 200},
		{"*;q=0.4", true, 200}, {"*;q=0", false, 406},
		{"gzip;q=0,identity;q=0", false, 406}, {"gzip;q=0,*;q=0,identity;q=1", false, 200},
		{"gzip;q=1e0", false, 200}, {"gzip;q=1.001", false, 200}, {"gzip;q=-1", false, 200},
		{"gzip;q=0.1234", false, 200}, {"gzip;q=garbage", false, 200},
		{"gzip;q=.5", false, 200}, {"gzip;q", false, 200},
	} {
		t.Run(test.value, func(t *testing.T) {
			called := false
			h := Compression(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { called = true; _, _ = io.WriteString(w, "hello") }))
			r := httptest.NewRequest("GET", "/", nil)
			r.Header.Set("Accept-Encoding", test.value)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if got := w.Header().Get("Content-Encoding") == "gzip"; got != test.compressed {
				t.Fatalf("compressed = %v, want %v", got, test.compressed)
			}
			if w.Code != test.status || called != (test.status != 406) {
				t.Fatalf("status %d, called %v", w.Code, called)
			}
		})
	}
}

func TestExcludedResponses(t *testing.T) {
	for _, test := range []struct {
		name, method, requestHeader, requestValue, header, value string
		status                                                   int
		body                                                     string
	}{
		{name: "head", method: "HEAD", status: 200, body: "body"},
		{name: "range-request", requestHeader: "Range", requestValue: "bytes=0-4", status: 206, body: "bytes"},
		{name: "content-range", header: "Content-Range", value: "bytes 0-4/10", status: 206, body: "bytes"},
		{name: "existing-gzip", header: "Content-Encoding", value: "gzip", status: 200, body: "preencoded"},
		{name: "existing-br", header: "Content-Encoding", value: "br", status: 200, body: "preencoded"},
		{name: "no-transform", header: "Cache-Control", value: "public, No-Transform", status: 200, body: "body"},
		{name: "png", header: "Content-Type", value: "image/png", status: 200, body: "png"},
		{name: "zip", header: "Content-Type", value: "application/zip", status: 200, body: "zip"},
		{name: "audio", header: "Content-Type", value: "audio/mpeg", status: 200, body: "audio"},
		{name: "empty", status: 200}, {name: "no-content", status: 204},
		{name: "reset-content", status: 205}, {name: "not-modified", status: 304},
		{name: "upgrade", requestHeader: "Upgrade", requestValue: "websocket", status: 101},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := Compression(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if test.header != "" {
					w.Header().Set(test.header, test.value)
				}
				w.WriteHeader(test.status)
				if test.body != "" {
					_, _ = io.WriteString(w, test.body)
				}
			}))
			method := test.method
			if method == "" {
				method = "GET"
			}
			r := httptest.NewRequest(method, "/", nil)
			r.Header.Set("Accept-Encoding", "gzip")
			if test.requestHeader != "" {
				r.Header.Set(test.requestHeader, test.requestValue)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			wantEncoding := ""
			if test.header == "Content-Encoding" {
				wantEncoding = test.value
			}
			if got := w.Header().Get("Content-Encoding"); got != wantEncoding {
				t.Fatalf("Content-Encoding = %q", got)
			}
			if w.Body.String() != test.body {
				t.Fatal("body changed")
			}
		})
	}
}

func TestCompressibleTypesAndVaryMerge(t *testing.T) {
	for _, contentType := range []string{"text/event-stream", "application/wasm", "image/svg+xml", "application/json"} {
		t.Run(contentType, func(t *testing.T) {
			h := Compression(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", contentType)
				w.Header().Set("Vary", "Origin, accept-encoding")
				_, _ = io.WriteString(w, "hello")
			}))
			r := httptest.NewRequest("GET", "/", nil)
			r.Header.Set("Accept-Encoding", "gzip")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Header().Get("Content-Encoding") != "gzip" || len(w.Header().Values("Vary")) != 1 {
				t.Fatalf("headers = %v", w.Header())
			}
			if unzip(t, w.Body.Bytes()) != "hello" {
				t.Fatal("body mismatch")
			}
		})
	}
}

func TestStreamingFlushBeforeCompletion(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(Compression(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-release
		_, _ = io.WriteString(w, "data: last\n\n")
	})))
	defer server.Close()
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Accept-Encoding", "gzip")
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	defer client.CloseIdleConnections()
	response, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	reader, err := gzip.NewReader(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	first := make([]byte, len("data: first\n\n"))
	if _, err := io.ReadFull(reader, first); err != nil {
		t.Fatal(err)
	}
	if string(first) != "data: first\n\n" {
		t.Fatal("streamed bytes differ")
	}
}

func TestHeadersFrozenAndTrailers(t *testing.T) {
	h := Compression(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Example", "before")
		w.Header().Set("Trailer", "X-Checksum")
		w.WriteHeader(http.StatusOK)
		w.Header().Set("X-Example", "after")
		_, _ = io.WriteString(w, "data")
		w.Header().Set("X-Checksum", "sum")
	}))
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	result := w.Result()
	if result.Header.Get("X-Example") != "before" {
		t.Fatal("late response header changed committed header")
	}
	if result.Trailer.Get("X-Checksum") != "sum" {
		t.Fatalf("trailers = %v", result.Trailer)
	}
}

func TestNestedMiddlewareDoesNotDoubleEncode(t *testing.T) {
	h := Compression(Compression(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "one layer") })))
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if unzip(t, w.Body.Bytes()) != "one layer" {
		t.Fatal("response encoded twice")
	}
}

type plainWriter struct {
	header http.Header
	body   bytes.Buffer
	status int
}

func (w *plainWriter) Header() http.Header            { return w.header }
func (w *plainWriter) WriteHeader(status int)         { w.status = status }
func (w *plainWriter) Write(body []byte) (int, error) { return w.body.Write(body) }

func TestOptionalFlusherAndResponseController(t *testing.T) {
	h := Compression(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, ok := w.(http.Flusher); ok {
			t.Error("unsupported Flusher advertised")
		}
		if err := http.NewResponseController(w).Flush(); !errors.Is(err, http.ErrNotSupported) {
			t.Errorf("Flush error = %v", err)
		}
		_, _ = io.WriteString(w, "body")
	}))
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w := &plainWriter{header: make(http.Header)}
	h.ServeHTTP(w, r)
	if unzip(t, w.body.Bytes()) != "body" {
		t.Fatal("body mismatch")
	}
}

func TestPanicDoesNotCommitSuccess(t *testing.T) {
	h := Compression(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { panic("failure") }))
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w := &plainWriter{header: make(http.Header)}
	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic swallowed")
			}
		}()
		h.ServeHTTP(w, r)
	}()
	if w.status != 0 || w.body.Len() != 0 {
		t.Fatal("panic committed successful response")
	}
}

type hijackOnlyWriter struct {
	plainWriter
	connection net.Conn
}

func (w *hijackOnlyWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return w.connection, bufio.NewReadWriter(bufio.NewReader(w.connection), bufio.NewWriter(w.connection)), nil
}

func TestHijackingDoesNotCommitHTTPResponse(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	h := Compression(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if _, ok := w.(http.Flusher); ok {
			t.Error("unsupported Flusher advertised")
		}
		conn, _, err := w.(http.Hijacker).Hijack()
		if err != nil || conn != server {
			t.Fatalf("Hijack = %v, %v", conn, err)
		}
		if _, err := w.Write([]byte("bad")); !errors.Is(err, http.ErrHijacked) {
			t.Errorf("write after Hijack error = %v", err)
		}
	}))
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w := &hijackOnlyWriter{plainWriter: plainWriter{header: make(http.Header)}, connection: server}
	h.ServeHTTP(w, r)
	if w.status != 0 || w.body.Len() != 0 {
		t.Fatal("Hijack committed an HTTP response")
	}
}

func TestGzipPoolIsBoundedAndDetachesFinishedDestination(t *testing.T) {
	pool := make(gzipWriterPool, 2)
	var first bytes.Buffer
	writer := pool.get(&first)
	if _, err := io.WriteString(writer, "first response"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	firstBytes := append([]byte(nil), first.Bytes()...)
	pool.put(writer)
	released := <-pool
	// A released writer must point at io.Discard even before its next acquire.
	if _, err := io.WriteString(released, "discarded response"); err != nil {
		t.Fatal(err)
	}
	if err := released.Close(); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first.Bytes(), firstBytes) {
		t.Fatal("released compressor retained prior response destination")
	}
	pool.put(released)
	var second bytes.Buffer
	reused := pool.get(&second)
	if reused != writer {
		t.Fatal("idle compressor was not reused")
	}
	if _, err := io.WriteString(reused, "second response"); err != nil {
		t.Fatal(err)
	}
	if err := reused.Close(); err != nil {
		t.Fatal(err)
	}
	if unzip(t, first.Bytes()) != "first response" || unzip(t, second.Bytes()) != "second response" {
		t.Fatal("response sources mixed across reuse")
	}
	pool.put(reused)
	writers := make([]*gzip.Writer, 8)
	for i := range writers {
		writers[i] = pool.get(io.Discard)
	}
	for _, writer := range writers {
		pool.put(writer)
	}
	if len(pool) != cap(pool) {
		t.Fatalf("idle pool size = %d, capacity %d", len(pool), cap(pool))
	}
}

type failingDestination struct{}

func (failingDestination) Write([]byte) (int, error) { return 0, errors.New("connection failed") }

func TestGzipPoolResetsTransportErrors(t *testing.T) {
	pool := make(gzipWriterPool, 1)
	writer := pool.get(failingDestination{})
	if _, err := io.WriteString(writer, "first response"); err == nil {
		t.Fatal("transport error was lost")
	}
	_ = writer.Close()
	pool.put(writer)
	var output bytes.Buffer
	reused := pool.get(&output)
	if _, err := io.WriteString(reused, "recovered response"); err != nil {
		t.Fatal(err)
	}
	if err := reused.Close(); err != nil {
		t.Fatal(err)
	}
	pool.put(reused)
	if unzip(t, output.Bytes()) != "recovered response" {
		t.Fatal("failed compressor poisoned its next request")
	}
}

func TestConcurrentCompressionDoesNotMixSources(t *testing.T) {
	handler := Compression(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, r.Header.Get("X-Test-Body"))
	}))
	var workers sync.WaitGroup
	for worker := 0; worker < 48; worker++ {
		workers.Add(1)
		go func(worker int) {
			defer workers.Done()
			for request := 0; request < 8; request++ {
				body := strings.Repeat(fmt.Sprintf("private response %d:%d\n", worker, request), 100)
				r := httptest.NewRequest("GET", "/", nil)
				r.Header.Set("Accept-Encoding", "gzip")
				r.Header.Set("X-Test-Body", body)
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				if got := unzip(t, w.Body.Bytes()); got != body {
					t.Errorf("response %d:%d contains another request's data", worker, request)
					return
				}
			}
		}(worker)
	}
	workers.Wait()
	if len(compressors) > maxIdleGzipWriters {
		t.Fatal("idle compressor memory exceeded its bound")
	}
}

func TestPanicAfterWriteReleasesCompressorWithoutFinishingStream(t *testing.T) {
	var captured *responseWriter
	handler := Compression(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		captured = w.(*flushWriter).responseWriter
		_, _ = io.WriteString(w, "partial response")
		panic("handler failed after writing")
	}))
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	func() {
		defer func() {
			if recover() == nil {
				t.Error("handler panic was swallowed")
			}
		}()
		handler.ServeHTTP(w, r)
	}()
	if captured == nil || captured.zip != nil {
		t.Fatal("panicking handler retained its compressor")
	}
	reader, err := gzip.NewReader(bytes.NewReader(w.Body.Bytes()))
	if err == nil {
		_, err = io.ReadAll(reader)
		_ = reader.Close()
	}
	if err == nil {
		t.Fatal("panicking response was finalized as a complete gzip stream")
	}
	next := Compression(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "next request") }))
	output := httptest.NewRecorder()
	next.ServeHTTP(output, r)
	if unzip(t, output.Body.Bytes()) != "next request" {
		t.Fatal("panicking request poisoned later response")
	}
}

func unzip(t *testing.T, body []byte) string {
	t.Helper()
	reader, err := gzip.NewReader(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	plain, err := io.ReadAll(reader)
	if err != nil {
		t.Fatal(err)
	}
	return string(plain)
}
