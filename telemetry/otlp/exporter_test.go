package otlp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/telemetry"
)

func record(i int) telemetry.Record {
	return telemetry.Record{Name: "nextgo.request", Kind: telemetry.Server, TraceID: "0123456789abcdef0123456789abcdef", SpanID: strconv.FormatUint(uint64(i)+0x1000000000000000, 16), Started: time.Unix(1700000000, 123), Duration: 17 * time.Millisecond, Route: "/posts/:slug", Method: "GET", Locale: "fr", Status: 200, Bytes: 123}
}
func exporter(t *testing.T, options Options) *Exporter {
	t.Helper()
	if options.ServiceName == "" {
		options.ServiceName = "test-service"
	}
	if options.Interval == 0 {
		options.Interval = time.Minute
	}
	e, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = e.Shutdown(ctx)
	})
	return e
}
func flush(t *testing.T, e *Exporter) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return e.Flush(ctx)
}

func TestWireUsesOTLPJSONIDsEnumsIntegersAndResourceOwnership(t *testing.T) {
	var captured map[string]any
	var mu sync.Mutex
	headers := http.Header{"Authorization": []string{"private-original"}}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/traces" || r.Method != "POST" || r.Header.Get("Content-Type") != "application/json" || r.Header.Get("Authorization") != "private-original" {
			t.Error(r.Method, r.URL, r.Header)
		}
		mu.Lock()
		defer mu.Unlock()
		if err := json.NewDecoder(r.Body).Decode(&captured); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{}")
	}))
	defer server.Close()
	e := exporter(t, Options{Endpoint: server.URL, Headers: headers, ServiceVersion: "v-test"})
	headers.Set("Authorization", "private-mutated")
	value := record(1)
	value.ParentID = "fedcba9876543210"
	value.Error = telemetry.ErrorInternal
	if err := e.Record(value); err != nil {
		t.Fatal(err)
	}
	if err := flush(t, e); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	resources := captured["resourceSpans"].([]any)
	resource := resources[0].(map[string]any)
	attrs := resource["resource"].(map[string]any)["attributes"].([]any)
	if attrs[0].(map[string]any)["value"].(map[string]any)["stringValue"] != "test-service" {
		t.Fatal(attrs)
	}
	scopes := resource["scopeSpans"].([]any)
	span := scopes[0].(map[string]any)["spans"].([]any)[0].(map[string]any)
	if span["traceId"] != value.TraceID || span["parentSpanId"] != value.ParentID || span["kind"] != float64(2) || span["startTimeUnixNano"] != strconv.FormatInt(value.Started.UnixNano(), 10) || span["endTimeUnixNano"] != strconv.FormatInt(value.Started.Add(value.Duration).UnixNano(), 10) || span["status"].(map[string]any)["code"] != float64(2) {
		t.Fatal(span)
	}
	bytes, _ := json.Marshal(captured)
	if strings.Contains(string(bytes), "private") {
		t.Fatal(string(bytes))
	}
	if stats := e.Stats(); stats.Accepted != 1 || stats.Exported != 1 || stats.Dropped != 0 || stats.Queued != 0 {
		t.Fatal(stats)
	}
}

func TestQueueAdmissionIsNonblockingAndShutdownCancelsExport(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		once.Do(func() { close(started) })
		select {
		case <-release:
		case <-r.Context().Done():
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{}")
	}))
	defer server.Close()
	defer close(release)
	e := exporter(t, Options{Endpoint: server.URL, QueueSize: 2, BatchSize: 1, MaxQueueBytes: 4096, Timeout: time.Second})
	if err := e.Record(record(1)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("export did not start")
	}
	for _, i := range []int{2, 3} {
		if err := e.Record(record(i)); err != nil {
			t.Fatal(err)
		}
	}
	if err := e.Record(record(4)); err != ErrQueueFull {
		t.Fatal(err)
	}
	stats := e.Stats()
	if stats.Queued != 2 || stats.InFlight != 1 || stats.QueueBytes > 4096 {
		t.Fatal(stats)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := e.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	select {
	case <-e.done:
	case <-time.After(time.Second):
		t.Fatal("expired shutdown left worker running")
	}
	if err := e.Record(record(5)); err != ErrClosed {
		t.Fatal(err)
	}
	stats = e.Stats()
	if stats.Queued != 0 || stats.InFlight != 0 || stats.QueueBytes != 0 || stats.Dropped != 4 {
		t.Fatal(stats)
	}
}

func TestRetryPartialRejectionAndPermanentFailures(t *testing.T) {
	for _, test := range []struct {
		name     string
		status   int
		body     string
		retry    bool
		exported uint64
	}{{"transient", 503, "{}", true, 2}, {"permanent", 400, "private-rejection", false, 0}, {"partial", 200, `{"partialSuccess":{"rejectedSpans":"1","errorMessage":"private-rejection"}}`, false, 1}, {"number-partial", 200, `{"partialSuccess":{"rejectedSpans":1}}`, false, 1}, {"warning", 200, `{"partialSuccess":{"rejectedSpans":"0","errorMessage":"private-warning"}}`, false, 2}, {"malformed", 200, "private-malformed", false, 0}, {"bad-count", 200, `{"partialSuccess":{"rejectedSpans":"3"}}`, false, 0}} {
		t.Run(test.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if test.retry && n > 1 {
					_, _ = io.WriteString(w, "{}")
					return
				}
				w.WriteHeader(test.status)
				_, _ = io.WriteString(w, test.body)
			}))
			defer server.Close()
			e := exporter(t, Options{Endpoint: server.URL, BatchSize: 10, Timeout: time.Second})
			_ = e.Record(record(1))
			_ = e.Record(record(2))
			err := flush(t, e)
			if test.exported == 2 {
				if err != nil {
					t.Fatal(err)
				}
			} else if err != ErrExport {
				t.Fatal(err)
			}
			wantCalls := int32(1)
			if test.retry {
				wantCalls = 2
			}
			if calls.Load() != wantCalls {
				t.Fatal(calls.Load())
			}
			stats := e.Stats()
			if stats.Exported != test.exported || stats.Dropped != 2-test.exported {
				t.Fatal(stats)
			}
			if err != nil && strings.Contains(err.Error(), "private") {
				t.Fatal(err)
			}
		})
	}
}

func TestRetryAfterAndFlushDeadlinesBoundTheWholeBatch(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(429)
	}))
	defer server.Close()
	e := exporter(t, Options{Endpoint: server.URL, BatchSize: 10, Timeout: time.Second})
	_ = e.Record(record(1))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := e.Flush(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("retried before Retry-After", calls.Load())
	}
}

func TestRedirectsResponsesAndInvalidInputsFailWithoutLeakingSecrets(t *testing.T) {
	var redirected atomic.Int32
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { redirected.Add(1) }))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", destination.URL)
		w.WriteHeader(307)
	}))
	defer server.Close()
	e := exporter(t, Options{Endpoint: server.URL, Headers: http.Header{"Authorization": []string{"private-secret"}}})
	_ = e.Record(record(1))
	if err := flush(t, e); err != ErrExport {
		t.Fatal(err)
	}
	if redirected.Load() != 0 {
		t.Fatal("redirected collector credentials")
	}
	for _, endpoint := range []string{"", "file:///tmp/file", "https://user:private-secret@example.test", "https://example.test?secret=private", "http://example.test:65536"} {
		if _, err := New(Options{Endpoint: endpoint, ServiceName: "test"}); err == nil || strings.Contains(err.Error(), "private") {
			t.Fatal(endpoint, err)
		}
	}
	invalid := record(3)
	invalid.Name = strings.Repeat("a", 129)
	if err := e.Record(invalid); err != ErrRecord {
		t.Fatal(err)
	}
	invalid = record(3)
	invalid.Started = time.Time{}
	if err := e.Record(invalid); err != ErrRecord {
		t.Fatal(err)
	}
}

func TestByteCapacityAndRecoveryAfterFailedBatch(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{}")
	}))
	defer server.Close()
	e := exporter(t, Options{Endpoint: server.URL, QueueSize: 10, BatchSize: 10, MaxQueueBytes: 1024})
	value := record(1)
	value.Route = strings.Repeat("x", 256)
	if err := e.Record(value); err != nil {
		t.Fatal(err)
	}
	if err := e.Record(value); err != ErrQueueFull {
		t.Fatal("byte budget did not reject second record", err, e.Stats())
	}
	if err := flush(t, e); err != ErrExport {
		t.Fatal(err)
	}
	if err := e.Record(record(2)); err != nil {
		t.Fatal("failed batch retained queue capacity", err)
	}
	if err := flush(t, e); err != nil {
		t.Fatal("exporter did not recover", err)
	}
	if stats := e.Stats(); stats.Accepted != 2 || stats.Exported != 1 || stats.Dropped != 2 || stats.FailedBatches != 1 || stats.QueueBytes != 0 {
		t.Fatal(stats)
	}
}

func TestConcurrentFlushAdmissionIsBounded(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-release
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{}")
	}))
	defer server.Close()
	defer close(release)
	e := exporter(t, Options{Endpoint: server.URL, QueueSize: 2, BatchSize: 1})
	if err := e.Record(record(1)); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("export did not start")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	results := make(chan error, 17)
	for range 17 {
		go func() { results <- e.Flush(ctx) }()
	}
	select {
	case err := <-results:
		if err != ErrFlushBusy || len(e.flush) != 16 {
			t.Fatal("flush queue limit", err, len(e.flush))
		}
	case <-time.After(time.Second):
		t.Fatal("flush callers exceeded capacity without rejection")
	}
	cancel()
	for range 16 {
		select {
		case err := <-results:
			if !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
		case <-time.After(time.Second):
			t.Fatal("canceled flush did not return")
		}
	}
}

func TestLaterShutdownDeadlineDoesNotCancelFirstDrain(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	var transportCanceled atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		select {
		case <-release:
		case <-r.Context().Done():
			transportCanceled.Store(true)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, "{}")
	}))
	defer server.Close()
	var releaseOnce sync.Once
	finish := func() { releaseOnce.Do(func() { close(release) }) }
	defer finish()
	e := exporter(t, Options{Endpoint: server.URL})
	if err := e.Record(record(1)); err != nil {
		t.Fatal(err)
	}
	first, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- e.Shutdown(first) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not start draining")
	}
	second, stop := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer stop()
	if err := e.Shutdown(second); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if e.root.Err() != nil {
		t.Fatal("later caller canceled the first caller's drain")
	}
	finish()
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("first drain did not complete")
	}
	if stats := e.Stats(); stats.Exported != 1 || stats.Dropped != 0 || transportCanceled.Load() {
		t.Fatal(stats, transportCanceled.Load())
	}
}
