package nextgo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/actions"
	"github.com/Suhaibinator/Next.go/internal/postresponse"
	"github.com/Suhaibinator/Next.go/telemetry"
	"github.com/Suhaibinator/Next.go/ui"
)

type traceRecords struct {
	mu     sync.Mutex
	values []telemetry.Record
}

func (r *traceRecords) record(value telemetry.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values = append(r.values, value)
	return nil
}
func (r *traceRecords) all() []telemetry.Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]telemetry.Record(nil), r.values...)
}
func tracedConfig(t *testing.T, values *traceRecords) Config {
	t.Helper()
	tracer, err := telemetry.New(telemetry.Options{Recorder: values.record})
	if err != nil {
		t.Fatal(err)
	}
	return Config{Tracing: tracer}
}

func TestTracingCoversMiddlewareLoaderRenderAndAfterWithoutSecrets(t *testing.T) {
	var records traceRecords
	c := tracedConfig(t, &records)
	c.Middleware = []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_ = telemetry.Run(r.Context(), "app.middleware", func(ctx context.Context) error { next.ServeHTTP(w, r.WithContext(ctx)); return nil })
		})
	}}
	c.Routes = []Route{{Pattern: "/posts/:id", Page: func(c *Context) (Page, error) {
		if telemetry.Context(c).SpanID != telemetry.Context(c.Request.Context()).SpanID {
			t.Error("loader request context lost its span")
		}
		if err := After(c, func(context.Context) error { return errors.New("private callback failure") }); err != nil {
			t.Error(err)
		}
		_ = telemetry.Run(c, "app.repository", func(context.Context) error { return nil })
		return Page{Body: ui.Text(c.Params["id"])}, nil
	}}}
	app := testApp(t, c)
	r := httptest.NewRequest("GET", "http://example.test/posts/private-slug?secret=private-query", nil)
	r.Header.Set("Authorization", "private-token")
	r.Header.Set("Cookie", "session=private-cookie")
	r.Header.Set("Traceparent", "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := app.Close(ctx); err != nil {
		t.Fatal(err)
	}
	got := records.all()
	names := map[string]telemetry.Record{}
	byID := map[string]telemetry.Record{}
	for _, record := range got {
		names[record.Name] = record
		byID[record.SpanID] = record
		if record.Route != "/posts/:id" || record.TraceID == "0123456789abcdef0123456789abcdef" {
			t.Fatal(record)
		}
	}
	for _, name := range []string{"nextgo.request", "app.middleware", "nextgo.load", "app.repository", "nextgo.render", "nextgo.after"} {
		if names[name].SpanID == "" {
			t.Fatal("missing span", name, got)
		}
	}
	root := names["nextgo.request"]
	if root.Status != 200 || root.Bytes != int64(w.Body.Len()) || root.ParentID != "" || names["nextgo.after"].Error != telemetry.ErrorInternal {
		t.Fatal(got)
	}
	for _, record := range got {
		if record.TraceID != root.TraceID {
			t.Fatal("trace split", record)
		}
		if record.ParentID != "" {
			if _, ok := byID[record.ParentID]; !ok {
				t.Fatal("missing parent", record)
			}
		}
	}
	data, _ := json.Marshal(got)
	if strings.Contains(string(data), "private") {
		t.Fatal("request/error secrets entered records", string(data))
	}
}

func TestTracingCacheHitsErrorsAPIActionsAndLocaleMounts(t *testing.T) {
	var records traceRecords
	c := tracedConfig(t, &records)
	locales := localeConfig(true)
	locales.Detect = false
	c.Locales = locales
	c.Routes = []Route{
		{Pattern: "/cached", Cache: &CachePolicy{TTL: time.Minute}, Page: func(*Context) (Page, error) { return Page{Body: ui.Text("public")}, nil }},
		{Pattern: "/fail", Page: func(*Context) (Page, error) { return Page{}, errors.New("private loader failure") }},
		{Pattern: "/api/:id", API: func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(403) }},
	}
	actionsRouter := actions.New(actions.Options{})
	actionsRouter.Handle("save", func(*actions.Context) (actions.Result, error) {
		return actions.Result{}, errors.New("private action failure")
	})
	c.Actions = actionsRouter
	app := testApp(t, c)
	for _, path := range []string{"/fr/cached", "/fr/cached", "/ar/fail", "/en/api/private-id"} {
		app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "http://example.test"+path, nil))
	}
	r := httptest.NewRequest("POST", "http://example.test/fr/_nextgo/actions/save", strings.NewReader("{}"))
	r.Header.Set("Origin", "http://example.test")
	r.Header.Set("Content-Type", "application/json")
	app.ServeHTTP(httptest.NewRecorder(), r)
	var rootCount int
	var cached []string
	seenAPI, seenAction := false, false
	for _, record := range records.all() {
		if record.Name == "nextgo.request" {
			rootCount++
			if record.Locale == "" {
				t.Fatal("missing locale", record)
			}
		}
		if record.Name == "nextgo.load" && record.Route == "/cached" {
			cached = append(cached, record.Cache)
		}
		if record.Name == "nextgo.api" {
			seenAPI = record.Status == 403 && record.Route == "/api/:id"
		}
		if record.Name == "nextgo.action" {
			seenAction = record.Error == telemetry.ErrorInternal && record.Route == "/_nextgo/actions/:action"
		}
	}
	if rootCount != 5 || strings.Join(cached, ",") != "miss,fresh" || !seenAPI || !seenAction {
		t.Fatal(rootCount, cached, seenAPI, seenAction, records.all())
	}
	data, _ := json.Marshal(records.all())
	if strings.Contains(string(data), "private") {
		t.Fatal(string(data))
	}
}

func TestTracingCompletionHonorsPlatformAcceptance(t *testing.T) {
	for _, accepted := range []bool{false, true} {
		var records traceRecords
		c := tracedConfig(t, &records)
		c.Routes = []Route{{Pattern: "/", Page: func(c *Context) (Page, error) {
			return Page{Body: ui.Text("ready")}, After(c, func(context.Context) error { return nil })
		}}}
		app := testApp(t, c)
		ctx, collector := postresponse.Collect(context.Background())
		app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "http://example.test/", nil).WithContext(ctx))
		for _, record := range records.all() {
			if record.Name == "nextgo.after" {
				t.Fatal("completion ran before platform acceptance")
			}
		}
		collector.Finish(context.Background(), accepted)
		seen := false
		for _, record := range records.all() {
			seen = seen || record.Name == "nextgo.after"
		}
		if seen != accepted {
			t.Fatal(accepted, records.all())
		}
	}
}
