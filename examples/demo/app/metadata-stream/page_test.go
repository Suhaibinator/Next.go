//go:build !js

package metadatastream

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/assets"
	"github.com/Suhaibinator/Next.go/seo"
)

func clearSettings(t *testing.T) {
	t.Helper()
	for _, name := range []string{"NEXTGO_DEMO_METADATA_SOURCE", "NEXTGO_DEMO_METADATA_SOURCE_TIMEOUT", "NEXTGO_DEMO_METADATA_FIXTURE", "NEXTGO_DEMO_METADATA_TEST_MODE"} {
		t.Setenv(name, "")
	}
}

func TestPageSeparatesSemanticFallbackFromFrozenCompanions(t *testing.T) {
	clearSettings(t)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != "GET" || r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "" {
			t.Error("metadata source inherited request credentials", r.Method, r.Header)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(defaultValue())
	}))
	defer server.Close()
	t.Setenv("NEXTGO_DEMO_METADATA_SOURCE", server.URL+"/value")
	ctx := &nextgo.Context{Context: nextgo.WithRequestMemo(context.Background()), Request: httptest.NewRequest("GET", "https://example.test/metadata-stream", nil)}
	page, err := Page(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !page.Stream || page.DeferredMetadata == nil || page.Title != "" || page.Description != "" || page.Metadata.Title != fallbackTitle || page.Metadata.Description != fallbackDescription || !page.Metadata.AbsoluteTitle || len(page.Resources) != 1 || len(page.JSONLD) != 1 {
		t.Fatal("fallback lost explicit typed ownership", page)
	}
	var html strings.Builder
	if err := page.Body.Render(&html); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`data-metadata-stream=""`, `data-metadata-input=""`, `autocomplete="off"`, `data-count="">3`, `data-metadata-refresh-link=""`, `href="/metadata-stream?cycle=next"`} {
		if !strings.Contains(html.String(), want) {
			t.Fatal("meaningful body missing", want, html.String())
		}
	}
	if strings.Contains(html.String(), server.URL) {
		t.Fatal("native source endpoint leaked to page body/props")
	}
	resources, err := assets.Elements(page.Resources...)
	if err != nil || len(resources) != 1 || resources[0].Attrs["href"] != resourcePath {
		t.Fatal(resources, err)
	}
	final, err := page.DeferredMetadata(ctx, page.Metadata)
	if err != nil || final.Title != defaultValue().Title || final.Description != defaultValue().Description || final.Canonical != defaultValue().Canonical || final.OpenGraph.Title != defaultValue().SocialTitle || final.Twitter.Title != defaultValue().SocialTitle || final.Viewport != nil {
		t.Fatal(final, err)
	}
	if _, err := seo.Elements(final); err != nil {
		t.Fatal(err)
	}
	settings, err := environmentSettings()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loadValue(ctx.Context, settings); err != nil || requests.Load() != 1 {
		t.Fatal("request Memo did not share public source work", requests.Load(), err)
	}
	if _, err := assets.Elements(page.Resources...); err == nil {
		t.Fatal("sample failed to detect a second resource callback")
	}
}

func TestSourceSettingsAreNativeBoundedAndFixtureModesAreExplicit(t *testing.T) {
	clearSettings(t)
	for _, bad := range []string{"https://127.0.0.1/value", "http://example.test/value", "http://localhost/value", "http://user@127.0.0.1/value", "http://127.0.0.1/value?target=other", "http://127.0.0.1/value#fragment", "http://127.0.0.1:00080/value", "http://127.0.0.1:65536/value"} {
		t.Setenv("NEXTGO_DEMO_METADATA_SOURCE", bad)
		if _, err := environmentSettings(); err == nil {
			t.Fatal("accepted nonliteral/unbounded source", bad)
		}
	}
	t.Setenv("NEXTGO_DEMO_METADATA_SOURCE", "http://127.0.0.1:4162/value")
	for _, bad := range []string{"invalid", "0s", "500us", "31s"} {
		t.Setenv("NEXTGO_DEMO_METADATA_SOURCE_TIMEOUT", bad)
		if _, err := environmentSettings(); err == nil {
			t.Fatal("accepted unbounded source timeout", bad)
		}
	}
	t.Setenv("NEXTGO_DEMO_METADATA_SOURCE_TIMEOUT", "30s")
	t.Setenv("NEXTGO_DEMO_METADATA_TEST_MODE", "readonly")
	if _, err := environmentSettings(); err == nil {
		t.Fatal("fixture mode became available without operator enablement")
	}
	t.Setenv("NEXTGO_DEMO_METADATA_FIXTURE", "true")
	for _, mode := range []string{"fail", "invalid", "readonly"} {
		t.Setenv("NEXTGO_DEMO_METADATA_TEST_MODE", mode)
		if settings, err := environmentSettings(); err != nil || settings.testMode != mode || settings.timeout != 30*time.Second {
			t.Fatal(settings, err)
		}
	}
}

func TestSourceRejectsAmbiguousOversizedAndUnsafeData(t *testing.T) {
	encoded, err := json.Marshal(defaultValue())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, body, media string
		status            int
	}{
		{"valid", string(encoded), "application/json", 200},
		{"duplicate", `{"version":1,"version":1}`, "application/json", 200},
		{"unknown", strings.TrimSuffix(string(encoded), "}") + `,"unexpected":true}`, "application/json", 200},
		{"oversized", strings.Repeat(" ", maxSourceBytes+1), "application/json", 200},
		{"unsafe canonical", strings.Replace(string(encoded), defaultValue().Canonical, "javascript:invalid", 1), "application/json", 200},
		{"invalid UTF8", string([]byte{'"', 0xff, '"'}), "application/json", 200},
		{"redirect", string(encoded), "application/json", 302},
		{"wrong media", string(encoded), "text/html", 200},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", test.media)
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			_, err := fetchValue(context.Background(), settings{source: server.URL, timeout: time.Second})
			if (err == nil) != (test.name == "valid") {
				t.Fatal("bounded source admission mismatch", err)
			}
		})
	}
}

func TestRequestCancellationReachesTheActualSource(t *testing.T) {
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(entered)
		select {
		case <-request.Context().Done():
			close(canceled)
		case <-release:
		}
	}))
	t.Cleanup(func() { close(release); server.Close() })
	request, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		_, err := loadValue(nextgo.WithRequestMemo(request), settings{source: server.URL, timeout: time.Second})
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("source request did not start")
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("request cancellation did not release the waiter")
	}
	select {
	case <-canceled:
	case <-time.After(2 * time.Second):
		t.Fatal("actual HTTP source did not observe cancellation")
	}
}
