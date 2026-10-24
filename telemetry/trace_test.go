package telemetry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

type records struct {
	mu     sync.Mutex
	values []Record
}

func (r *records) save(value Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.values = append(r.values, value)
	return nil
}
func (r *records) all() []Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Record(nil), r.values...)
}
func testTracer(t *testing.T, values *records, trust bool) *Tracer {
	t.Helper()
	tracer, err := New(Options{Recorder: values.save, TrustIncoming: trust})
	if err != nil {
		t.Fatal(err)
	}
	return tracer
}

func TestTraceparentValidationAndOptInParentSampling(t *testing.T) {
	parent := "00-0123456789abcdef0123456789abcdef-0123456789abcdef-01"
	for _, value := range []string{"", parent + "x", strings.Replace(parent, "00-", "01-", 1), strings.ToUpper(parent), "00-" + strings.Repeat("0", 32) + "-0123456789abcdef-01", "00-0123456789abcdef0123456789abcdef-0000000000000000-01", parent[:53] + "xz"} {
		if _, ok := ParseTraceparent(value); ok {
			t.Fatal("invalid parent accepted", value)
		}
	}
	for _, trust := range []bool{false, true} {
		var values records
		tracer := testTracer(t, &values, trust)
		r := httptest.NewRequest("GET", "http://example.test/path?secret=value", nil)
		r.Header.Set("Traceparent", parent)
		tracer.ServeHTTP(httptest.NewRecorder(), r, func(w http.ResponseWriter, r *http.Request) {
			SetRoute(r.Context(), "/path")
			SetLocale(r.Context(), "fr")
			ctx, span := Start(r.Context(), Info{Name: "app.repository"})
			if Traceparent(ctx) == "" {
				t.Fatal("missing child identity")
			}
			span.End(Result{})
		})
		got := values.all()
		if len(got) != 2 || got[0].ParentID != got[1].SpanID || got[0].TraceID != got[1].TraceID || got[1].Route != "/path" || got[1].Locale != "fr" {
			t.Fatal(got)
		}
		if (got[1].TraceID == "0123456789abcdef0123456789abcdef") != trust {
			t.Fatal("unexpected remote trust", trust, got)
		}
		if trust && got[1].ParentID != "0123456789abcdef" {
			t.Fatal(got)
		}
	}
	var values records
	tracer := testTracer(t, &values, true)
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Traceparent", parent[:53]+"00")
	tracer.ServeHTTP(httptest.NewRecorder(), r, func(_ http.ResponseWriter, r *http.Request) {
		if Context(r.Context()).Sampled {
			t.Error("unsampled remote parent changed")
		}
		_, span := Start(r.Context(), Info{Name: "child"})
		span.End(Result{})
	})
	if len(values.all()) != 0 {
		t.Fatal("unsampled trace was recorded")
	}
	r.Header.Add("Traceparent", parent)
	tracer.ServeHTTP(httptest.NewRecorder(), r, func(_ http.ResponseWriter, r *http.Request) {
		if Context(r.Context()).TraceID == "0123456789abcdef0123456789abcdef" {
			t.Error("duplicate header trusted")
		}
	})
}

func TestRecorderFailureIsolationIdempotencyAndConcurrentJSON(t *testing.T) {
	for _, recorder := range []func(Record) error{func(Record) error { return errors.New("private sink failure") }, func(Record) error { panic("private sink panic") }} {
		tracer, _ := New(Options{Recorder: recorder})
		_, span := tracer.Start(context.Background(), Info{Name: "test"})
		span.End(Result{})
		span.End(Result{})
		if tracer.Stats().Failures != 1 || tracer.Stats().Recorded != 0 {
			t.Fatal(tracer.Stats())
		}
	}
	var output bytes.Buffer
	tracer, _ := New(Options{Recorder: JSONRecorder(&output)})
	var pending sync.WaitGroup
	for i := 0; i < 100; i++ {
		pending.Add(1)
		go func() {
			defer pending.Done()
			_, span := tracer.Start(context.Background(), Info{Name: strings.Repeat("é", 100), Method: "private-token"})
			span.Cache("private-value")
			span.End(Result{})
		}()
	}
	pending.Wait()
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 100 {
		t.Fatal(len(lines))
	}
	for _, line := range lines {
		var r Record
		if json.Unmarshal([]byte(line), &r) != nil || len(r.Name) > 128 || r.Method != "OTHER" || r.Cache != "" {
			t.Fatal(line)
		}
	}
	if strings.Contains(output.String(), "private") {
		t.Fatal("unbounded method/cache values leaked")
	}
	if _, err := New(Options{}); err == nil {
		t.Fatal("missing recorder accepted")
	}
}

func TestRunPreservesErrorsPanicGoexitAndCancellation(t *testing.T) {
	var values records
	tracer := testTracer(t, &values, false)
	ctx, root := tracer.Start(context.Background(), Info{Name: "root"})
	secret := errors.New("private failure text")
	if err := Run(ctx, "fail", func(context.Context) error { return secret }); err != secret {
		t.Fatal(err)
	}
	func() {
		defer func() {
			if recover() != "private panic" {
				t.Error("panic was swallowed")
			}
		}()
		_ = Run(ctx, "panic", func(context.Context) error { panic("private panic") })
	}()
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = Run(ctx, "exit", func(context.Context) error { runtime.Goexit(); return nil })
	}()
	<-done
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_ = Run(canceled, "cancel", func(context.Context) error { return context.Canceled })
	root.End(Result{})
	got := values.all()
	if len(got) != 5 || got[0].Error != ErrorInternal || got[1].Error != ErrorPanic || got[2].Error != ErrorAborted || got[3].Error != ErrorCanceled {
		t.Fatal(got)
	}
	data, _ := json.Marshal(got)
	if bytes.Contains(data, []byte("private")) {
		t.Fatal(string(data))
	}
}

func TestLifecycleHooksAreExplicitIdempotentAndDoNotExposePanicPayloads(t *testing.T) {
	var flushes, shutdowns atomic.Int32
	tracer, err := New(Options{Recorder: func(Record) error { return nil }, Flush: func(context.Context) error { flushes.Add(1); return nil }, Shutdown: func(context.Context) error { shutdowns.Add(1); return nil }})
	if err != nil {
		t.Fatal(err)
	}
	_ = tracer.Flush(context.Background())
	_ = tracer.Shutdown(context.Background())
	_ = tracer.Shutdown(context.Background())
	if flushes.Load() != 1 || shutdowns.Load() != 1 {
		t.Fatal(flushes.Load(), shutdowns.Load())
	}
	broken, _ := New(Options{Recorder: func(Record) error { return nil }, Flush: func(context.Context) error { panic("private panic payload") }})
	if err := broken.Flush(context.Background()); err != ErrLifecycle || strings.Contains(err.Error(), "private") {
		t.Fatal(err)
	}
}
