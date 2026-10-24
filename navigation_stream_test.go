package nextgo

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/ui"
)

func TestNavigationStreamCommitsShellBeforeBoundaries(t *testing.T) {
	release := make(chan struct{})
	app, err := New(Config{BuildID: "test-build", Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) {
		return Page{Title: "Stream page", Stream: true, Body: ui.Fragment(ui.Text("shell"), ui.Defer("content", ui.Text("pending"), func(ctx context.Context) (ui.Node, error) {
			select {
			case <-release:
				return ui.Text("finished"), nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}))}, nil
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(app)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/?q=value", nil)
	request.Header.Set("X-Nextgo-Navigate", "1")
	request.Header.Set(navigation.StreamHeader, "1")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.Header.Get("Content-Type") != navigation.StreamMediaType+"; charset=utf-8" || response.Header.Get("Cache-Control") != "private, no-store" {
		t.Fatal(response.Header)
	}
	reader := bufio.NewReader(response.Body)
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	frame, err := navigation.DecodeStreamFrame(line)
	if err != nil || frame.Type != "shell" {
		t.Fatal(frame, err)
	}
	var envelope Envelope
	if err := json.Unmarshal(frame.Page, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.URL != "/?q=value" || envelope.Title != "Stream page" || !strings.Contains(envelope.HTML, "pending") || strings.Contains(envelope.HTML, "finished") {
		t.Fatal(envelope)
	}
	close(release)
	line, err = reader.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	frame, err = navigation.DecodeStreamFrame(line)
	if err != nil || frame.Type != "chunk" || frame.ID != "content" || frame.HTML != "finished" {
		t.Fatal(frame, err)
	}
	line, err = reader.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	frame, err = navigation.DecodeStreamFrame(line)
	if err != nil || frame.Type != "done" {
		t.Fatal(frame, err)
	}
}
func TestNavigationStreamFailureAndLegacyJSON(t *testing.T) {
	app, err := New(Config{Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) {
		return Page{Stream: true, Body: ui.Defer("content", ui.Text("pending"), func(context.Context) (ui.Node, error) { return nil, errors.New("private-secret-source") })}, nil
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Nextgo-Navigate", "1")
	r.Header.Set(navigation.StreamHeader, "1")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	if w.Code != 200 || strings.Contains(w.Body.String(), "private-secret-source") || !strings.Contains(w.Body.String(), `"failed":true`) {
		t.Fatal(w.Code, w.Body.String())
	}
	r.Header.Del(navigation.StreamHeader)
	w = httptest.NewRecorder()
	app.ServeHTTP(w, r)
	var page Envelope
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil || w.Code != 500 || page.Version != NavigationProtocolVersion {
		t.Fatal(err, w.Code)
	}
}
func TestNavigationStreamCancelsDeferredLoaderOnDisconnect(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	app, err := New(Config{Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) {
		return Page{Stream: true, Body: ui.Defer("waiting", nil, func(ctx context.Context) (ui.Node, error) {
			close(started)
			<-ctx.Done()
			close(stopped)
			return nil, ctx.Err()
		})}, nil
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(app)
	defer server.Close()
	ctx, cancel := context.WithCancel(context.Background())
	r, _ := http.NewRequestWithContext(ctx, "GET", server.URL, nil)
	r.Header.Set("X-Nextgo-Navigate", "1")
	r.Header.Set(navigation.StreamHeader, "1")
	response, err := http.DefaultClient.Do(r)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = bufio.NewReader(response.Body).ReadBytes('\n')
	<-started
	cancel()
	_, _ = io.Copy(io.Discard, response.Body)
	response.Body.Close()
	select {
	case <-stopped:
	case <-time.After(3 * time.Second):
		t.Fatal("deferred loader survived transport cancellation")
	}
}

func TestStreamPrefetchDoesNotStartDeferredWork(t *testing.T) {
	started := false
	app, err := New(Config{Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) {
		return Page{Stream: true, Body: ui.Defer("content", nil, func(context.Context) (ui.Node, error) { started = true; return ui.Text("private work"), nil })}, nil
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Nextgo-Navigate", "1")
	r.Header.Set(navigation.PurposeHeader, "prefetch")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	if w.Code != 204 || started || w.Body.Len() != 0 || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal(w.Code, started, w.Body.String())
	}
}
