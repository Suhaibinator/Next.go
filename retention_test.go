package nextgo

import (
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/ui"
)

func retainedTestPage(partition string) Page {
	return Page{Body: ui.El("main", nil, ui.El("p", ui.Attrs{"data-quote": `<&"`}, ui.Text("Useful SSR < & >"))), Title: "Retained", Retention: &RouteRetention{Partition: partition}}
}

func TestRetentionResponseStampBindsNativeHTMLAndNavigationBytes(t *testing.T) {
	page := retainedTestPage(`public-<&"-v1`)
	a := testApp(t, Config{RetainRoutes: true, Routes: []Route{{Pattern: "/a", Page: func(*Context) (Page, error) { return page, nil }}}})
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "/a", nil))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "Useful SSR") || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
	match := regexp.MustCompile(`data-nextgo-retention="([^"]+)"`).FindStringSubmatch(w.Body.String())
	if len(match) != 2 || strings.Contains(match[1], `<`) || strings.Contains(match[1], `"version"`) {
		t.Fatal("descriptor was missing or escaped unsafely", match)
	}
	initial, err := navigation.ParseRetentionDescriptor(html.UnescapeString(match[1]))
	if err != nil || initial == nil {
		t.Fatal("initial trusted document metadata was invalid", err)
	}
	want, err := ui.HTML(page.Body)
	if err != nil || initial.Validate(want) != nil {
		t.Fatal("server stamp did not hash the exact rendered body", err)
	}
	navResponse, envelope := runtimeEnvelope(t, a, "/a", nil)
	if navResponse.Code != http.StatusOK || envelope.Retention == nil || envelope.Retention.Validate(envelope.HTML) != nil || *envelope.Retention != *initial || envelope.HTML != want {
		t.Fatal("HTML and navigation retention stamps disagree", envelope)
	}
	encoded, _ := json.Marshal(envelope)
	if strings.Contains(string(encoded), "retention-html") || strings.Contains(w.Body.String(), "data-nextgo-retention-html") {
		t.Fatal("server duplicated private markup into a bootstrap snapshot")
	}
}

func TestRetentionRequiresBothOptInsAndFreshPerRequestPolicy(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		for _, opted := range []bool{false, true} {
			var calls atomic.Int32
			partition := "demo-one"
			a := testApp(t, Config{RetainRoutes: enabled, Routes: []Route{{Pattern: "/a", Page: func(*Context) (Page, error) {
				calls.Add(1)
				page := retainedTestPage(partition)
				if !opted {
					page.Retention = nil
				}
				return page, nil
			}}}})
			_, first := runtimeEnvelope(t, a, "/a", nil)
			partition = "demo-two"
			_, second := runtimeEnvelope(t, a, "/a", nil)
			if calls.Load() != 2 || (first.Retention != nil) != (enabled && opted) || (second.Retention != nil) != (enabled && opted) {
				t.Fatal("response policy bypassed admission or fresh rendering", enabled, opted, calls.Load())
			}
			if enabled && opted && (first.Retention.Partition != "demo-one" || second.Retention.Partition != "demo-two" || first.Retention.Fingerprint != second.Retention.Fingerprint) {
				t.Fatal("a prior privacy partition leaked into a fresh response")
			}
		}
	}
}

func TestRetentionDoesNotEnterPublicPageSnapshots(t *testing.T) {
	var calls atomic.Int32
	a := testApp(t, Config{RetainRoutes: true, Routes: []Route{{Pattern: "/cached", Cache: &CachePolicy{TTL: time.Minute}, Page: func(*Context) (Page, error) {
		calls.Add(1)
		return retainedTestPage("request-owned-partition"), nil
	}}}})
	for i := 0; i < 2; i++ {
		_, envelope := runtimeEnvelope(t, a, "/cached", nil)
		if envelope.Status != http.StatusOK || envelope.Retention != nil || !strings.Contains(envelope.HTML, "Useful SSR") {
			t.Fatal("public cache retained a per-request UI policy", envelope)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("retention changed normal cache behavior", calls.Load())
	}
}

func TestRetentionDenialsErrorsAndUnsupportedModesStayCold(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cfg    func(*Config)
		render func() (Page, error)
		status int
	}{
		{"denied", nil, func() (Page, error) { return retainedTestPage("demo"), ErrForbidden }, http.StatusForbidden},
		{"invalid-policy", nil, func() (Page, error) { return retainedTestPage("with spaces"), nil }, http.StatusInternalServerError},
		{"layout-preserve", func(c *Config) { c.PreserveLayouts = true }, func() (Page, error) { return retainedTestPage("demo"), nil }, http.StatusOK},
		{"development", func(c *Config) { c.Dev = true }, func() (Page, error) { return retainedTestPage("demo"), nil }, http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := Config{RetainRoutes: true, Routes: []Route{{Pattern: "/a", Page: func(*Context) (Page, error) { return tc.render() }}}}
			if tc.cfg != nil {
				tc.cfg(&cfg)
			}
			a := testApp(t, cfg)
			w, envelope := runtimeEnvelope(t, a, "/a", nil)
			if w.Code != tc.status || envelope.Retention != nil {
				t.Fatal(w.Code, envelope)
			}
		})
	}
}

func TestRetentionDescriptorRejectsUnsupportedPhasesAndStreams(t *testing.T) {
	for _, phase := range []Phase{PhaseDevelopment, PhasePrerender, PhaseStaticExport} {
		a := &App{config: Config{RetainRoutes: true, Phase: phase}}
		if descriptor, err := a.retentionDescriptor(retainedTestPage("demo"), http.StatusOK, "body"); err != nil || descriptor != nil {
			t.Fatal("unsupported phase admitted ownership", phase, descriptor, err)
		}
	}
	a := &App{config: Config{RetainRoutes: true, Phase: PhaseProduction}}
	page := retainedTestPage("demo")
	page.Stream = true
	if descriptor, err := a.retentionDescriptor(page, http.StatusOK, "body"); err != nil || descriptor != nil {
		t.Fatal("streaming response admitted buffered ownership", descriptor, err)
	}
	page.Stream = false
	if descriptor, err := a.retentionDescriptor(page, http.StatusOK, strings.Repeat("a", navigation.MaxRetentionHTML+1)); err != nil || descriptor != nil {
		t.Fatal("oversized response did not retain its fresh-render path", descriptor, err)
	}
}
