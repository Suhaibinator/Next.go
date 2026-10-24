//go:build deployment

package deployment

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/cache/shared"
)

const sharedSecret = "nextgo-deployment-shared-cache-secret-32"

var sharedGenerationRE = regexp.MustCompile(`data-shared-generation="">([a-f0-9]{32})</code>`)

var sharedSnapshotID = shared.ID{Namespace: "demo", Schema: "demo-shared-v1", Key: "snapshot"}
var sharedSnapshotPolicy = shared.Policy{FreshFor: 15 * time.Second, ExpireAfter: time.Minute, Tags: []string{"demo-shared"}, Paths: []string{"/shared-cache"}}

// A probe releases a lease it acquires for a stale/missing entry. It does not
// silently run a loader or replace the application generation being inspected.
func sharedStored(ctx context.Context, backend *shared.HTTP) (shared.View, string, error) {
	view, err := backend.BeginStale(ctx, sharedSnapshotID, sharedSnapshotPolicy, time.Second)
	if err != nil {
		return shared.View{}, "", err
	}
	if view.Lease != "" {
		if err := backend.Release(ctx, sharedSnapshotID, view.Lease); err != nil {
			return shared.View{}, "", err
		}
	}
	if !view.Found {
		return view, "", nil
	}
	var snapshot struct{ Generation string }
	if err := json.Unmarshal(view.Value, &snapshot); err != nil {
		return shared.View{}, "", err
	}
	return view, snapshot.Generation, nil
}

func sharedBackend(t *testing.T, endpoint string) (*shared.HTTP, *shared.Store) {
	t.Helper()
	backend, err := shared.NewHTTP(endpoint, sharedSecret, nil)
	if err != nil {
		t.Fatal(err)
	}
	store, err := shared.New(backend, "demo", "demo-shared-v1", shared.Options{})
	if err != nil {
		t.Fatal(err)
	}
	return backend, store
}

func cacheService(t *testing.T) (*httptest.Server, *atomic.Bool) {
	t.Helper()
	memory, err := shared.NewMemory(16, 4<<20)
	if err != nil {
		t.Fatal(err)
	}
	return cacheServiceWithBackend(t, memory)
}
func cacheServiceWithBackend(t *testing.T, backend shared.Backend) (*httptest.Server, *atomic.Bool) {
	t.Helper()
	h, err := shared.NewHandler(shared.HandlerOptions{Backend: backend, Namespace: "demo", Token: sharedSecret})
	if err != nil {
		t.Fatal(err)
	}
	down := &atomic.Bool{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if down.Load() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(503)
			_, _ = io.WriteString(w, `{"Version":3,"Error":"unavailable"}`)
			return
		}
		h.ServeHTTP(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, down
}

func sharedEnvelopeHeader(headers map[string]string, name string) string {
	for key, value := range headers {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	return ""
}

func waitSharedStored(t *testing.T, backend *shared.HTTP, accept func(shared.View, string) bool) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		view, generation, err := sharedStored(ctx, backend)
		if err != nil {
			t.Fatal(err)
		}
		if accept(view, generation) {
			return generation
		}
		select {
		case <-ctx.Done():
			t.Fatal("shared refresh did not settle", view, generation)
		case <-time.After(10 * time.Millisecond):
		}
	}
}
func sharedEnvironment(endpoint string) []string {
	return []string{"NEXTGO_DEMO_SHARED_ENDPOINT=" + endpoint, "NEXTGO_CACHE_TOKEN=" + sharedSecret}
}
func sharedGeneration(t *testing.T, html []byte) string {
	t.Helper()
	match := sharedGenerationRE.FindSubmatch(html)
	if len(match) != 2 || bytes.Contains(html, []byte(sharedSecret)) {
		t.Fatalf("missing public generation or leaked secret: %s", html)
	}
	return string(match[1])
}
func sharedGet(t *testing.T, client *http.Client, p *process, origin string, want int) []byte {
	data, _ := sharedGetPath(t, client, p, origin, "/shared-cache", want)
	return data
}
func sharedGetPath(t *testing.T, client *http.Client, p *process, origin, path string, want int) ([]byte, http.Header) {
	t.Helper()
	for until := time.Now().Add(5 * time.Second); time.Now().Before(until); {
		response, err := client.Get(origin + path)
		if err == nil {
			data := body(t, response)
			if response.StatusCode != want {
				t.Fatalf("shared page status %d, want %d: %s", response.StatusCode, want, data)
			}
			if !strings.Contains(response.Header.Get("Cache-Control"), "no-store") {
				t.Fatal(response.Header)
			}
			return data, response.Header
		}
		select {
		case <-p.done:
			t.Fatal(p.logs.String())
		case <-time.After(25 * time.Millisecond):
		}
	}
	t.Fatal("shared app startup timed out", p.logs.String())
	return nil, nil
}
func TestSharedCacheHTTPReplicasExecutable(t *testing.T) {
	srv, down := cacheService(t)
	env := sharedEnvironment(srv.URL)
	addrA, addrB := address(t), address(t)
	a, b := start(t, env, "-addr", addrA), start(t, env, "-addr", addrB)
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	originA, originB := "http://"+addrA, "http://"+addrB
	first := sharedGeneration(t, sharedGet(t, client, a, originA, 200))
	if got := sharedGeneration(t, sharedGet(t, client, b, originB, 200)); got != first {
		t.Fatal("replicas did not share generation", first, got)
	}
	// No implicit cache fallback: a service outage is a generic server error,
	// even while that process has previously read a fresh generation.
	down.Store(true)
	failed := sharedGet(t, client, a, originA, 500)
	if bytes.Contains(failed, []byte(first)) || bytes.Contains(failed, []byte(sharedSecret)) {
		t.Fatal("outage returned cached/private data")
	}
	down.Store(false)
	if got := sharedGeneration(t, sharedGet(t, client, b, originB, 200)); got != first {
		t.Fatal("outage corrupted last generation", got)
	}
	request, _ := http.NewRequest("POST", originA+"/_nextgo/actions/shared-cache-expire", strings.NewReader(""))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "text/html")
	request.Header.Set("Origin", originA)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = body(t, response)
	if response.StatusCode != 303 || response.Header.Get("Location") != "/shared-cache" {
		t.Fatal(response.StatusCode, response.Header)
	}
	next := sharedGeneration(t, sharedGet(t, client, b, originB, 200))
	if next == first {
		t.Fatal("remote process retained expired generation")
	}
	if got := sharedGeneration(t, sharedGet(t, client, a, originA, 200)); got != next {
		t.Fatal("invalidation not shared", got, next)
	}
	// An application restart must read the existing service generation.
	a.stop(t)
	restarted := start(t, env, "-addr", addrA)
	if got := sharedGeneration(t, sharedGet(t, client, restarted, originA, 200)); got != next {
		t.Fatal("app restart lost service generation", got, next)
	}
	restarted.stop(t)
	b.stop(t)
}

func TestSharedCacheRefreshHTTPExecutable(t *testing.T) {
	srv, _ := cacheService(t)
	backend, store := sharedBackend(t, srv.URL)
	addr := address(t)
	p := start(t, sharedEnvironment(srv.URL), "-addr", addr)
	defer p.stop(t)
	origin := "http://" + addr
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	first := sharedGeneration(t, sharedGet(t, client, p, origin, 200))
	request, _ := http.NewRequest("POST", origin+"/_nextgo/actions/shared-cache-stale", strings.NewReader(""))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	request.Header.Set("Accept", "text/html")
	request.Header.Set("Origin", origin)
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = body(t, response)
	if response.StatusCode != 303 || response.Header.Get("Location") != "/shared-cache?refresh=background" {
		t.Fatal(response.StatusCode, response.Header)
	}
	stale, headers := sharedGetPath(t, client, p, origin, "/shared-cache?refresh=background", 200)
	if sharedGeneration(t, stale) != first || headers.Get("X-Demo-Shared-Cache") != "stale" {
		t.Fatal("stale read did not preserve its selected generation", headers, string(stale))
	}
	second := waitSharedStored(t, backend, func(view shared.View, generation string) bool {
		return view.Found && view.FreshFor > 0 && generation != first
	})
	// Failed background work releases its lease but retains the last good value
	// without renewing its hard deadline. The ordinary reader still blocks.
	if _, err := store.MarkStale(context.Background(), shared.Selector{Kind: "tag", Value: "demo-shared"}, 30*time.Second); err != nil {
		t.Fatal(err)
	}
	failedRefresh, headers := sharedGetPath(t, client, p, origin, "/shared-cache?refresh=background&fail=1", 200)
	if sharedGeneration(t, failedRefresh) != second || headers.Get("X-Demo-Shared-Cache") != "stale" {
		t.Fatal("transient refresh error discarded selected last-good data", headers, string(failedRefresh))
	}
	waitSharedStored(t, backend, func(view shared.View, generation string) bool {
		return view.Found && !view.Busy && view.FreshFor == 0 && generation == second
	})
	thirdBody, headers := sharedGetPath(t, client, p, origin, "/shared-cache", 200)
	third := sharedGeneration(t, thirdBody)
	if third == second || headers.Get("X-Demo-Shared-Cache") != "miss" {
		t.Fatal("blocking stale read did not load fresh data", headers, third, second)
	}
	if _, err := store.MarkStale(context.Background(), shared.Selector{Kind: "tag", Value: "demo-shared"}, time.Millisecond); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Millisecond)
	failure, _ := sharedGetPath(t, client, p, origin, "/shared-cache?refresh=background&fail=1", 500)
	if sharedGenerationRE.Match(failure) || bytes.Contains(failure, []byte(third)) || bytes.Contains(failure, []byte(sharedSecret)) {
		t.Fatal("hard expiry served stale/private data", string(failure))
	}
	recovery, headers := sharedGetPath(t, client, p, origin, "/shared-cache?refresh=background", 200)
	if sharedGeneration(t, recovery) == third || headers.Get("X-Demo-Shared-Cache") != "miss" {
		t.Fatal("failed hard-expiry load did not recover", headers, string(recovery))
	}
}

// Only refresh publication is guarded. Cold/blocking loaders must be allowed
// to commit before producing their ordinary invocation response.
type sharedPublicationBackend struct {
	shared.Backend
	allowed  *atomic.Bool
	rejected func()
}

func (b *sharedPublicationBackend) Commit(ctx context.Context, id shared.ID, token string, value []byte) (shared.View, error) {
	if !b.allowed.Load() {
		b.rejected()
	}
	return b.Backend.Commit(ctx, id, token, value)
}

func TestSharedCacheLambdaExecutable(t *testing.T) {
	const steps = 10
	for _, mode := range []string{"buffered", "streaming"} {
		t.Run(mode, func(t *testing.T) {
			memory, err := shared.NewMemory(16, 4<<20)
			if err != nil {
				t.Fatal(err)
			}
			var publicationAllowed atomic.Bool
			publicationAllowed.Store(true)
			guard := &sharedPublicationBackend{Backend: memory, allowed: &publicationAllowed, rejected: func() { t.Error("shared refresh committed before the Runtime API accepted its response") }}
			srv, _ := cacheServiceWithBackend(t, guard)
			backend, client := sharedBackend(t, srv.URL)
			var issued, reports atomic.Int32
			var stateMu sync.Mutex
			generations := make(map[int]string)
			getGeneration := func(index int) string { stateMu.Lock(); defer stateMu.Unlock(); return generations[index] }
			setGeneration := func(index int, generation string) {
				stateMu.Lock()
				defer stateMu.Unlock()
				generations[index] = generation
			}
			var refreshedBeforeNext string
			done := make(chan struct{})
			runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/next") {
					index := int(issued.Add(1)) - 1
					if index >= steps {
						if index == steps {
							close(done)
						}
						<-r.Context().Done()
						return
					}
					switch index {
					case 2, 6:
						publicationAllowed.Store(false)
						if _, err := client.MarkStale(r.Context(), shared.Selector{Kind: "tag", Value: "demo-shared"}, 30*time.Second); err != nil {
							t.Error(err)
						}
					case 3:
						// A healthy refresh must complete before the worker asks for
						// another invocation, without a test-side retry or delay.
						view, generation, err := sharedStored(r.Context(), backend)
						if err != nil || !view.Found || view.Busy || view.Lease != "" || view.FreshFor <= 0 || generation == getGeneration(0) {
							t.Errorf("refresh not drained before /next: %+v %q %v", view, generation, err)
						}
						stateMu.Lock()
						refreshedBeforeNext = generation
						stateMu.Unlock()
					case 4:
						if _, err := client.Expire(r.Context(), shared.Selector{Kind: "tag", Value: "demo-shared"}); err != nil {
							t.Error(err)
						}
					case 7:
						// The failed scheduled load must release its lease while
						// retaining exactly the preceding last-good generation.
						view, generation, err := sharedStored(r.Context(), backend)
						if err != nil || !view.Found || view.Busy || view.FreshFor != 0 || generation != getGeneration(4) {
							t.Errorf("failed refresh did not retain/release last good: %+v %q %v", view, generation, err)
						}
					case 8:
						if _, err := client.MarkStale(r.Context(), shared.Selector{Kind: "tag", Value: "demo-shared"}, time.Millisecond); err != nil {
							t.Error(err)
						}
						time.Sleep(5 * time.Millisecond) // Cross the explicit backend hard deadline.
					case 9:
						view, _, err := sharedStored(r.Context(), backend)
						if err != nil || view.Found {
							t.Errorf("failed hard-expiry load retained a generation: %+v %v", view, err)
						}
					}
					query := ""
					if index == 2 || index == 6 || index == 8 || index == 9 {
						query = "refresh=background"
					}
					if index == 6 || index == 8 {
						query += "&fail=1"
					}
					w.Header().Set("Lambda-Runtime-Aws-Request-Id", strconv.Itoa(index))
					w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(5*time.Second).UnixMilli(), 10))
					_ = json.NewEncoder(w).Encode(map[string]any{"version": "2.0", "rawPath": "/shared-cache", "rawQueryString": query, "headers": map[string]string{"host": "demo.test"}, "requestContext": map[string]any{"domainName": "demo.test", "http": map[string]string{"method": "GET", "path": "/shared-cache"}}})
					return
				}
				data, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
				if err != nil {
					t.Error(err)
				}
				var envelope httpEnvelope
				var payload []byte
				if mode == "buffered" {
					if err := json.Unmarshal(data, &envelope); err != nil {
						t.Error(err)
					}
					payload = []byte(envelope.Body)
					if envelope.Base64 {
						payload, err = base64.StdEncoding.DecodeString(envelope.Body)
						if err != nil {
							t.Error(err)
						}
					}
				} else {
					split := bytes.Index(data, make([]byte, 8))
					if split < 0 {
						t.Error("missing Lambda stream delimiter")
						w.WriteHeader(500)
						return
					}
					if err := json.Unmarshal(data[:split], &envelope); err != nil {
						t.Error(err)
					}
					payload = data[split+8:]
				}
				index := int(reports.Add(1)) - 1
				if bytes.Contains(payload, []byte(sharedSecret)) {
					t.Error("shared cache credential leaked in Lambda response")
				}
				if !strings.Contains(sharedEnvelopeHeader(envelope.Headers, "Cache-Control"), "no-store") {
					t.Error("shared Lambda response permits HTTP caching", envelope.Headers)
				}
				if index == 8 {
					if envelope.StatusCode != 500 || sharedGenerationRE.Match(payload) || bytes.Contains(payload, []byte(getGeneration(7))) {
						t.Errorf("hard-expiry failure returned stale data: status %d", envelope.StatusCode)
					}
				} else {
					match := sharedGenerationRE.FindSubmatch(payload)
					if envelope.StatusCode != 200 || len(match) != 2 {
						t.Errorf("shared Lambda response %d status %d missing generation", index, envelope.StatusCode)
					} else {
						generation := string(match[1])
						setGeneration(index, generation)
						switch index {
						case 1, 2:
							if generation != getGeneration(0) {
								t.Error("fresh/stale publication changed old generation", index)
							}
						case 3:
							stateMu.Lock()
							expected := refreshedBeforeNext
							stateMu.Unlock()
							if generation != expected || generation == getGeneration(0) {
								t.Error("drained refresh generation missing from next event")
							}
						case 4:
							if generation == getGeneration(3) {
								t.Error("strict expiration reused old generation")
							}
						case 5, 6:
							if generation != getGeneration(4) {
								t.Error("fresh/failed-refresh generation changed", index)
							}
						case 7:
							if generation == getGeneration(4) {
								t.Error("blocking stale read did not replace last good")
							}
						case 9:
							if generation == getGeneration(7) {
								t.Error("hard-expiry recovery reused obsolete generation")
							}
						}
					}
					states := map[int]string{0: "miss", 1: "fresh", 2: "stale", 3: "fresh", 4: "miss", 5: "fresh", 6: "stale", 7: "miss", 9: "miss"}
					if got := sharedEnvelopeHeader(envelope.Headers, "X-Demo-Shared-Cache"); got != states[index] {
						t.Errorf("step %d cache state %q, want %q", index, got, states[index])
					}
				}
				if index == 2 || index == 6 {
					// Inspect while /response is still unacknowledged. The selected
					// old value and queued lease must remain intact at this point.
					view, generation, err := sharedStored(r.Context(), backend)
					oldIndex := 0
					if index == 6 {
						oldIndex = 4
					}
					if err != nil || !view.Found || !view.Busy || view.Lease != "" || generation != getGeneration(oldIndex) {
						t.Errorf("scheduled refresh ran before publication: %+v %q %v", view, generation, err)
					}
					publicationAllowed.Store(true)
				}
				w.WriteHeader(202)
			}))
			defer runtime.Close()
			env := append(sharedEnvironment(srv.URL), "AWS_LAMBDA_RUNTIME_API="+strings.TrimPrefix(runtime.URL, "http://"), "NEXTGO_LAMBDA_RESPONSE_MODE="+mode)
			p := start(t, env)
			select {
			case <-done:
			case <-p.done:
				t.Fatal(p.logs.String())
			case <-time.After(20 * time.Second):
				t.Fatal(fmt.Sprint("shared Lambda timed out: ", p.logs.String()))
			}
			p.stop(t)
			if reports.Load() != steps {
				t.Fatal("missing shared Lambda reports", reports.Load())
			}
			// Service data survives worker shutdown, including its final recovered
			// generation. No worker-local value can supply this backend read.
			view, generation, err := sharedStored(context.Background(), backend)
			if err != nil || !view.Found || view.Busy || view.Lease != "" || generation != getGeneration(9) {
				t.Fatal(view, generation, err)
			}
		})
	}
}
