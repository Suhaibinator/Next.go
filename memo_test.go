package nextgo

import (
	"context"
	"errors"
	"fmt"
	"github.com/Suhaibinator/Next.go/ui"
	"io"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMemoConcurrentCallsShareValuesErrorsAndNil(t *testing.T) {
	ctx, cancel := context.WithTimeout(WithRequestMemo(context.Background()), time.Second)
	defer cancel()
	var calls atomic.Int32
	release := make(chan struct{})
	started := make(chan struct{})
	loader := func(context.Context) (int, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return 42, nil
	}
	results := make(chan error, 32)
	for i := 0; i < 32; i++ {
		go func() {
			value, err := Memo(ctx, "value", loader)
			if err == nil && value != 42 {
				err = fmt.Errorf("value=%d", value)
			}
			results <- err
		}()
	}
	<-started
	close(release)
	for i := 0; i < 32; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("loader called %d times", calls.Load())
	}
	failure := errors.New("loader failure")
	for i := 0; i < 2; i++ {
		_, err := Memo(ctx, "failure", func(context.Context) (int, error) { calls.Add(1); return 0, failure })
		if !errors.Is(err, failure) {
			t.Fatal(err)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("error not memoized")
	}
	for i := 0; i < 2; i++ {
		value, err := Memo[any](ctx, "nil", func(context.Context) (any, error) { return nil, nil })
		if err != nil || value != nil {
			t.Fatalf("nil result: %v %v", value, err)
		}
	}
	if _, err := Memo[string](ctx, "value", func(context.Context) (string, error) { return "wrong type", nil }); !errors.Is(err, ErrMemoType) {
		t.Fatal(err)
	}
}

func TestMemoWaiterCancellationPreservesRootDeadlineAndCallerValues(t *testing.T) {
	request, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	root := WithRequestMemo(request)
	type tenantKey struct{}
	waiter, stop := context.WithTimeout(context.WithValue(root, tenantKey{}, "tenant-a"), 20*time.Millisecond)
	defer stop()
	started := make(chan context.Context, 1)
	release := make(chan struct{})
	loader := func(ctx context.Context) (int, error) { started <- ctx; <-release; return 5, nil }
	first := make(chan error, 1)
	go func() { _, err := Memo(waiter, "shared", loader); first <- err }()
	worker := <-started
	wantDeadline, _ := request.Deadline()
	gotDeadline, ok := worker.Deadline()
	if !ok || !wantDeadline.Equal(gotDeadline) || worker.Value(tenantKey{}) != "tenant-a" {
		t.Fatal("lost root deadline or initiating values")
	}
	second := make(chan error, 1)
	go func() {
		v, err := Memo(root, "shared", loader)
		if err == nil && v != 5 {
			err = fmt.Errorf("value=%d", v)
		}
		second <- err
	}()
	if err := <-first; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if worker.Err() != nil {
		t.Fatal("waiter canceled shared loader")
	}
	close(release)
	if err := <-second; err != nil {
		t.Fatal(err)
	}
}

func TestMemoRootCancellationAndCauseReachLoader(t *testing.T) {
	request, cancel := context.WithCancelCause(context.Background())
	defer cancel(context.Canceled)
	root := WithRequestMemo(request)
	started := make(chan struct{})
	cause := make(chan error, 1)
	result := make(chan error, 1)
	go func() {
		_, err := Memo(root, "blocked", func(ctx context.Context) (int, error) {
			close(started)
			<-ctx.Done()
			cause <- context.Cause(ctx)
			return 0, ctx.Err()
		})
		result <- err
	}()
	<-started
	failure := errors.New("request ended")
	cancel(failure)
	select {
	case err := <-cause:
		if !errors.Is(err, failure) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("loader cancellation leaked")
	}
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestMemoCyclesAndAbnormalLoadersDoNotStrandWaiters(t *testing.T) {
	ctx, cancel := context.WithTimeout(WithRequestMemo(context.Background()), time.Second)
	defer cancel()
	_, err := Memo(ctx, "a", func(ctx context.Context) (int, error) {
		return Memo(ctx, "b", func(ctx context.Context) (int, error) {
			return Memo(ctx, "a", func(context.Context) (int, error) { return 0, nil })
		})
	})
	if !errors.Is(err, ErrMemoCycle) {
		t.Fatalf("cycle result %v", err)
	}
	for _, tc := range []struct {
		name string
		load func(context.Context) (int, error)
	}{
		{"panic", func(context.Context) (int, error) { panic("failed") }},
		{"exit", func(context.Context) (int, error) { runtime.Goexit(); return 0, nil }},
	} {
		for i := 0; i < 2; i++ {
			if _, err := Memo(ctx, tc.name, tc.load); err == nil {
				t.Fatalf("%s did not return an error", tc.name)
			}
		}
	}
	// Both root loaders exist before either waits for the other.
	root := WithRequestMemo(ctx)
	a, b := make(chan struct{}), make(chan struct{})
	results := make(chan error, 2)
	go func() {
		_, err := Memo(root, "left", func(ctx context.Context) (int, error) {
			close(a)
			<-b
			return Memo(ctx, "right", func(context.Context) (int, error) { return 0, nil })
		})
		results <- err
	}()
	go func() {
		_, err := Memo(root, "right", func(ctx context.Context) (int, error) {
			close(b)
			<-a
			return Memo(ctx, "left", func(context.Context) (int, error) { return 0, nil })
		})
		results <- err
	}()
	for i := 0; i < 2; i++ {
		if err := <-results; !errors.Is(err, ErrMemoCycle) {
			t.Fatalf("concurrent cycle: %v", err)
		}
	}
}

func TestMemoBoundsAndScopeValidation(t *testing.T) {
	loader := func(context.Context) (int, error) { return 1, nil }
	if _, err := Memo(context.Background(), "value", loader); !errors.Is(err, ErrMemoScope) {
		t.Fatal(err)
	}
	ctx := WithRequestMemo(context.Background())
	for i := 0; i < 128; i++ {
		if _, err := Memo(ctx, fmt.Sprint(i), loader); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Memo(ctx, "extra", loader); !errors.Is(err, ErrMemoCapacity) {
		t.Fatal(err)
	}
	for _, key := range []string{"", strings.Repeat("x", 1025)} {
		if _, err := Memo(WithRequestMemo(context.Background()), key, loader); err == nil {
			t.Fatal("invalid key accepted")
		}
	}
}

func TestMemoRequestIsolationAndSharedLayoutData(t *testing.T) {
	var calls atomic.Int32
	loader := func(context.Context) (int, error) { return int(calls.Add(1)), nil }
	page := func(c *Context) (Page, error) {
		value, err := Memo(c, "value", loader)
		return Page{Body: ui.Text(fmt.Sprint(value))}, err
	}
	layout := func(c *Context, body ui.Node) (ui.Node, error) {
		value, err := Memo(c.Context, "value", loader)
		return ui.Fragment(ui.Text(fmt.Sprint(value)+":"), body), err
	}
	app := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: page, Layouts: []LayoutFunc{layout}}}})
	for i := 1; i <= 2; i++ {
		if body := request(app, "/", false).Body.String(); !strings.Contains(body, fmt.Sprintf(">%d:%d</div>", i, i)) {
			t.Fatal(body)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("requests shared memo state or layouts repeated work")
	}
}

func TestPublicPageCacheDoesNotReuseMiddlewareMemo(t *testing.T) {
	var loads atomic.Int32
	middleware := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, err := Memo(r.Context(), "shared", func(context.Context) (int, error) { return 99, nil }); err != nil {
				t.Error(err)
			}
			next.ServeHTTP(w, r)
		})
	}
	app := testApp(t, Config{Middleware: []func(http.Handler) http.Handler{middleware}, Routes: []Route{{Pattern: "/", Cache: &CachePolicy{TTL: time.Minute}, Page: func(c *Context) (Page, error) {
		value, err := Memo(c, "shared", func(context.Context) (int, error) { loads.Add(1); return 7, nil })
		return Page{Body: ui.Text(fmt.Sprint(value))}, err
	}}}})
	for i := 0; i < 2; i++ {
		body := request(app, "/", true)
		if body.Code != 200 || !strings.Contains(body.Body.String(), `"html":"7"`) {
			t.Fatalf("public cache inherited middleware memo: %d %s", body.Code, body.Body.String())
		}
	}
	if loads.Load() != 1 {
		t.Fatal("public page not cached")
	}
}

// Ensure canceled callers don't keep the request scope alive through callbacks.
func TestMemoHTTPAPIContext(t *testing.T) {
	var mu sync.Mutex
	calls := 0
	app := testApp(t, Config{Routes: []Route{{Pattern: "/api/test", API: func(w http.ResponseWriter, r *http.Request) {
		for i := 0; i < 2; i++ {
			_, err := Memo(r.Context(), "api", func(context.Context) (int, error) { mu.Lock(); calls++; mu.Unlock(); return 1, nil })
			if err != nil {
				t.Error(err)
			}
		}
		_, _ = io.WriteString(w, "ok")
	}}}})
	for i := 0; i < 2; i++ {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest("GET", "/api/test", nil))
		if w.Body.String() != "ok" {
			t.Fatal(w)
		}
	}
	if calls != 2 {
		t.Fatal(calls)
	}
}

func TestCanceledNestedWaitDoesNotLeaveAFalseDependencyCycle(t *testing.T) {
	root, cancel := context.WithTimeout(WithRequestMemo(context.Background()), time.Second)
	defer cancel()
	bStarted, readA, aReady, finishA := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
	bReading := make(chan struct{})
	bResult := make(chan error, 1)
	aResult := make(chan error, 1)
	go func() {
		_, err := Memo(root, "a", func(ctx context.Context) (int, error) {
			child, stop := context.WithCancel(ctx)
			defer stop()
			waiter := make(chan error, 1)
			go func() {
				_, err := Memo(child, "b", func(ctx context.Context) (int, error) {
					close(bStarted)
					<-readA
					close(bReading)
					value, err := Memo(ctx, "a", func(context.Context) (int, error) { return 0, nil })
					bResult <- err
					return value, err
				})
				waiter <- err
			}()
			<-bStarted
			stop()
			if err := <-waiter; !errors.Is(err, context.Canceled) {
				return 0, fmt.Errorf("child cancellation: %v", err)
			}
			close(aReady)
			<-finishA
			return 7, nil
		})
		aResult <- err
	}()
	<-aReady
	close(readA)
	<-bReading
	select {
	case err := <-bResult:
		close(finishA)
		t.Fatalf("canceled A-to-B wait left a false cycle: %v", err)
	case <-time.After(20 * time.Millisecond):
		close(finishA)
	}
	if err := <-aResult; err != nil {
		t.Fatal(err)
	}
	if err := <-bResult; err != nil {
		t.Fatal(err)
	}
}

func TestMemoDetachedWaiterStillEndsWithRequest(t *testing.T) {
	request, cancel := context.WithCancel(context.Background())
	defer cancel()
	root := WithRequestMemo(request)
	started, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	result := make(chan error, 1)
	go func() {
		_, err := Memo(context.WithoutCancel(root), "slow", func(context.Context) (int, error) { close(started); <-release; return 1, nil })
		result <- err
	}()
	<-started
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("detached waiter outlived request cancellation")
	}
}

func TestMemoWorkersCancelWhenApplicationHandlerReturns(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	app := testApp(t, Config{Routes: []Route{{Pattern: "/api/early", API: func(w http.ResponseWriter, r *http.Request) {
		go func() {
			_, _ = Memo(r.Context(), "work", func(ctx context.Context) (int, error) {
				close(started)
				<-ctx.Done()
				close(stopped)
				return 0, ctx.Err()
			})
		}()
		<-started
		w.WriteHeader(204)
	}}}})
	app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/early", nil))
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("request work survived handler return")
	}
}

func TestDeferredNavigationLoadersShareRequestMemo(t *testing.T) {
	var loads atomic.Int32
	app := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: func(c *Context) (Page, error) {
		loader := func(context.Context) (string, error) { loads.Add(1); return "resolved", nil }
		value, err := Memo(c, "value", loader)
		if err != nil {
			return Page{}, err
		}
		return Page{Stream: true, Body: ui.Fragment(ui.Text(value+":"), ui.Defer("deferred", ui.Text("loading"), func(ctx context.Context) (ui.Node, error) {
			value, err := Memo(ctx, "value", loader)
			return ui.Text(value), err
		}))}, nil
	}}}})
	response := request(app, "/", true)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"html":"resolved:resolved"`) || loads.Load() != 1 {
		t.Fatalf("navigation lost memo context: %d %s loads=%d", response.Code, response.Body.String(), loads.Load())
	}
}
