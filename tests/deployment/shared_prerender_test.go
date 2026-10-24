//go:build deployment

package deployment

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/cache/shared"
	"github.com/Suhaibinator/Next.go/examples/demo/components/counter"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/serverless"
	"github.com/Suhaibinator/Next.go/ui"
)

const seedFixtureCode = "shared-prerender-executable-v1"
const seedFixtureTag = "shared-prerender-executable"

// This fixture runs as a separate copy of the compiled deployment executable.
// It needs neither a Go compiler nor application sources in the scratch image.
func TestSharedPrerenderProcessHelper(t *testing.T) {
	if os.Getenv("NEXTGO_TEST_SEED_HELPER") != "true" {
		return
	}
	fresh, err := time.ParseDuration(os.Getenv("NEXTGO_TEST_SEED_FRESH"))
	if err != nil {
		t.Fatal(err)
	}
	config, err := seedFixtureConfig(os.Getenv("NEXTGO_TEST_SEED_ENDPOINT"), os.Getenv("NEXTGO_TEST_SEED_GENERATION"), os.Getenv("NEXTGO_TEST_SEED_FAIL") == "true", fresh)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(os.Getenv("NEXTGO_TEST_SEED_ARTIFACT"))
	if err != nil {
		t.Fatal(err)
	}
	config.BuildID = nextgo.PrerenderBuildID(seedFixtureCode, data)
	config.PrerenderFS = os.DirFS(filepath.Dir(os.Getenv("NEXTGO_TEST_SEED_ARTIFACT")))
	app, err := nextgo.New(config)
	if err != nil {
		t.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	closeApp := func() {
		drain, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := app.Close(drain); err != nil {
			t.Error(err)
		}
	}
	if os.Getenv("AWS_LAMBDA_RUNTIME_API") != "" {
		defer closeApp()
		if err := serverless.Run(ctx, app); err != nil && ctx.Err() == nil {
			t.Fatal(err)
		}
		return
	}
	server := &http.Server{Addr: os.Getenv("NEXTGO_TEST_SEED_ADDR"), Handler: app, ReadHeaderTimeout: 2 * time.Second}
	drained := make(chan struct{})
	go func() {
		<-ctx.Done()
		drain, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(drain)
		closeApp()
		close(drained)
	}()
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		t.Fatal(err)
	}
	<-drained
}

func seedFixtureConfig(endpoint, generation string, fail bool, fresh time.Duration) (nextgo.Config, error) {
	backend, err := shared.NewHTTP(endpoint, durableCacheSecret, nil)
	if err != nil {
		return nextgo.Config{}, err
	}
	store, err := shared.New(backend, "demo", "seed-executable-v1", shared.Options{Timeout: 10 * time.Second})
	if err != nil {
		return nextgo.Config{}, err
	}
	return nextgo.Config{BuildID: seedFixtureCode, SharedCache: store, Logger: log.New(io.Discard, "", 0), Routes: []nextgo.Route{
		{Pattern: "/api/seed-health", API: func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"ok":true}`)
		}},
		{Pattern: "/seed/:slug", Prerender: &nextgo.PrerenderPolicy{HostIndependent: true, Shared: true, Revalidate: fresh, Expire: max(time.Minute, 2*fresh), Tags: []string{seedFixtureTag}}, StaticParams: func(context.Context) ([]nextgo.Params, error) {
			return []nextgo.Params{{"slug": "alpha"}, {"slug": "beta"}}, nil
		},
			Page: func(c *nextgo.Context) (nextgo.Page, error) {
				effective, unavailable := generation, fail
				if path := os.Getenv("NEXTGO_TEST_SEED_SOURCE_FILE"); path != "" {
					data, err := os.ReadFile(path)
					var source seedSource
					if err != nil || json.Unmarshal(data, &source) != nil {
						return nextgo.Page{}, errors.New("seed fixture source unreadable")
					}
					effective, unavailable = source.Generation, source.Fail
				}
				if unavailable {
					return nextgo.Page{}, errors.New("seed fixture source unavailable")
				}
				title := "Seed " + c.Params["slug"] + " " + effective
				return nextgo.Page{Title: title, Description: "Generation " + effective, Meta: map[string]string{"seed-generation": effective}, Metadata: seo.Metadata{Language: "en", Direction: "ltr", OpenGraph: seo.OpenGraph{Title: title, Type: "article"}}, Body: ui.El("article", nil, ui.El("h1", nil, ui.Text(c.Params["slug"])), ui.El("code", ui.Attrs{"data-seed-generation": ""}, ui.Text(effective)), counter.View(counter.Props{Label: "Seed counter", Initial: 3}))}, nil
			}, Layouts: []nextgo.LayoutFunc{func(_ *nextgo.Context, n ui.Node) (ui.Node, error) {
				return ui.El("main", ui.Attrs{"data-seed-layout": ""}, n), nil
			}},
		},
	}}, nil
}

func buildSeedFixture(t *testing.T, endpoint, generation string, fresh time.Duration) string {
	t.Helper()
	config, err := seedFixtureConfig(endpoint, generation, false, fresh)
	if err != nil {
		t.Fatal(err)
	}
	config.PrerenderBuildMode = true
	app, err := nextgo.New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := app.Close(ctx); err != nil {
			t.Error(err)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	data, err := app.BuildPrerender(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte(durableCacheSecret)) {
		t.Fatal("artifact contains authority authentication credential")
	}
	dir := durableDirectory(t)
	path := filepath.Join(dir, "prerender.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func seedChild(t *testing.T, endpoint, artifact, generation string, fail bool, fresh time.Duration, env ...string) *process {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary, "-test.run=^TestSharedPrerenderProcessHelper$", "-test.timeout=60s")
	cmd.Dir = "/"
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if !strings.HasPrefix(key, "NEXTGO_TEST_SEED_") && !strings.HasPrefix(key, "AWS_LAMBDA_") && !strings.HasPrefix(key, "NEXTGO_LAMBDA_") {
			cmd.Env = append(cmd.Env, entry)
		}
	}
	cmd.Env = append(cmd.Env, "NEXTGO_TEST_SEED_HELPER=true", "NEXTGO_TEST_SEED_ENDPOINT="+endpoint, "NEXTGO_TEST_SEED_ARTIFACT="+artifact, "NEXTGO_TEST_SEED_GENERATION="+generation, "NEXTGO_TEST_SEED_FAIL="+strconv.FormatBool(fail), "NEXTGO_TEST_SEED_FRESH="+fresh.String())
	cmd.Env = append(cmd.Env, env...)
	p := &process{cmd: cmd, done: make(chan struct{}), logs: &capture{}}
	cmd.Stdout = p.logs
	cmd.Stderr = p.logs
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	go func() { p.err = cmd.Wait(); close(p.done) }()
	t.Cleanup(func() {
		select {
		case <-p.done:
			return
		default:
		}
		_ = p.cmd.Process.Kill()
		<-p.done
	})
	return p
}
func closeSeedChild(p *process) {
	select {
	case <-p.done:
		return
	default:
	}
	_ = p.cmd.Process.Kill()
	<-p.done
}

func seedHTTPChild(t *testing.T, endpoint, artifact, generation string, fail bool, fresh time.Duration) (*process, string) {
	t.Helper()
	addr := address(t)
	p := seedChild(t, endpoint, artifact, generation, fail, fresh, "NEXTGO_TEST_SEED_ADDR="+addr)
	client := &http.Client{Timeout: time.Second}
	defer client.CloseIdleConnections()
	until := time.Now().Add(10 * time.Second)
	for time.Now().Before(until) {
		response, err := client.Get("http://" + addr + "/api/seed-health")
		if err == nil {
			data := body(t, response)
			if response.StatusCode == 200 && strings.Contains(string(data), `"ok":true`) {
				return p, "http://" + addr
			}
		}
		select {
		case <-p.done:
			t.Fatalf("seed application startup: %v\n%s", p.err, p.logs.String())
		case <-time.After(20 * time.Millisecond):
		}
	}
	t.Fatal("seed application readiness timeout", p.logs.String())
	return nil, ""
}
func readSeedHTTP(t *testing.T, origin, path string, want int) ([]byte, http.Header) {
	t.Helper()
	client := &http.Client{Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	request, err := http.NewRequest("GET", origin+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "seed.example.test"
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	data := body(t, response)
	if response.StatusCode != want {
		t.Fatalf("seed response %d want%d: %s", response.StatusCode, want, data)
	}
	if bytes.Contains(data, []byte(durableCacheSecret)) {
		t.Fatal("seed response leaked authority credential")
	}
	return data, response.Header.Clone()
}
func mustSeedPage(t *testing.T, data []byte, slug, generation string) {
	t.Helper()
	for _, part := range []string{"<title>Seed " + slug + " " + generation + "</title>", `data-seed-generation="">` + generation + "</code>", `name="seed-generation"`, `content="` + generation + `"`, `data-seed-layout=""`, `aria-label="Seed counter"`, `data-count=""`, `disabled=""`} {
		if !bytes.Contains(data, []byte(part)) {
			t.Fatalf("seed page missing %q: %s", part, data)
		}
	}
}

func TestSharedPrerenderHTTPExecutable(t *testing.T) {
	dir, addr := durableDirectory(t), address(t)
	authority, backend := openDurableCacheAt(t, dir, addr)
	endpoint := "http://" + addr
	artifact := buildSeedFixture(t, endpoint, "build-one", time.Hour)
	worker, origin := seedHTTPChild(t, endpoint, artifact, "runtime-hidden", true, time.Hour)
	first, headers := readSeedHTTP(t, origin, "/seed/alpha", 200)
	mustSeedPage(t, first, "alpha", "build-one")
	if headers.Get("X-Nextgo-Prerender") != "build" || headers.Get("X-Nextgo-Cache") != "fresh" {
		t.Fatal(headers)
	}
	worker.stop(t)
	killDurableCache(t, authority)
	authority, backend = openDurableCacheAt(t, dir, addr)
	worker, origin = seedHTTPChild(t, endpoint, artifact, "runtime-hidden", true, time.Hour)
	second, _ := readSeedHTTP(t, origin, "/seed/alpha", 200)
	if !bytes.Equal(first, second) {
		t.Fatal("cache/app restart changed seeded frozen document")
	}
	worker.stop(t)
	if n, err := backend.Expire(context.Background(), "demo", shared.Selector{Kind: "tag", Value: seedFixtureTag}); err != nil || n != 2 {
		t.Fatal("seed invalidation", n, err)
	}
	killDurableCache(t, authority)
	authority, _ = openDurableCacheAt(t, dir, addr)
	worker, origin = seedHTTPChild(t, endpoint, artifact, "runtime-hidden", true, time.Hour)
	failed, _ := readSeedHTTP(t, origin, "/seed/alpha", 500)
	if bytes.Contains(failed, []byte("build-one")) {
		t.Fatal("consumed artifact resurrected invalidated seed")
	}
	worker.stop(t)
	// A later online build receives a new capability and can seed the same
	// namespace again after an acknowledged invalidation and authority restart.
	newer := buildSeedFixture(t, endpoint, "build-two", time.Hour)
	worker, origin = seedHTTPChild(t, endpoint, newer, "runtime-hidden", true, time.Hour)
	data, headers := readSeedHTTP(t, origin, "/seed/alpha", 200)
	mustSeedPage(t, data, "alpha", "build-two")
	if headers.Get("X-Nextgo-Prerender") != "build" {
		t.Fatal(headers)
	}
	worker.stop(t)
	backend = durableHTTP(t, addr)
	if n, err := backend.Expire(context.Background(), "demo", shared.Selector{Kind: "path", Value: "/seed/alpha"}); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	worker, origin = seedHTTPChild(t, endpoint, newer, "runtime-three", false, time.Hour)
	data, headers = readSeedHTTP(t, origin, "/seed/alpha", 200)
	mustSeedPage(t, data, "alpha", "runtime-three")
	if headers.Get("X-Nextgo-Prerender") != "runtime" {
		t.Fatal(headers)
	}
	data, _ = readSeedHTTP(t, origin, "/seed/beta", 200)
	mustSeedPage(t, data, "beta", "build-two")
	worker.stop(t)
	authority.stop(t)
}

func TestSharedPrerenderPendingBuildRevokedExecutable(t *testing.T) {
	for _, test := range []struct {
		name     string
		selector shared.Selector
		stale    bool
	}{{"tag", shared.Selector{Kind: "tag", Value: seedFixtureTag}, false}, {"path", shared.Selector{Kind: "path", Value: "/seed/alpha"}, false}, {"prefix", shared.Selector{Kind: "prefix", Value: "/seed"}, false}, {"key", shared.Selector{Kind: "key", Value: "not-yet-imported"}, false}, {"all", shared.Selector{Kind: "all"}, false}, {"stale", shared.Selector{Kind: "tag", Value: seedFixtureTag}, true}} {
		t.Run(test.name, func(t *testing.T) {
			dir, addr := durableDirectory(t), address(t)
			authority, backend := openDurableCacheAt(t, dir, addr)
			endpoint := "http://" + addr
			artifact := buildSeedFixture(t, endpoint, "must-never-import", time.Hour)
			var n int
			var err error
			if test.stale {
				n, err = backend.MarkStale(context.Background(), "demo", test.selector, shared.Never)
			} else {
				n, err = backend.Expire(context.Background(), "demo", test.selector)
			}
			if err != nil || n != 0 {
				t.Fatal("empty invalidation must acknowledge without a matching value", n, err)
			}
			killDurableCache(t, authority)
			authority, _ = openDurableCacheAt(t, dir, addr)
			worker, origin := seedHTTPChild(t, endpoint, artifact, "source-offline", true, time.Hour)
			data, _ := readSeedHTTP(t, origin, "/seed/alpha", 500)
			if bytes.Contains(data, []byte("must-never-import")) {
				t.Fatal("empty invalidation lost build capability revocation")
			}
			worker.stop(t)
			authority.stop(t)
		})
	}
}

type seedLambdaEvent struct {
	path, generation string
	status           int
}

func seedLambdaEvents(t *testing.T, mode, endpoint, artifact string, events []seedLambdaEvent, before func(int)) {
	t.Helper()
	var issued, reports atomic.Int32
	done := make(chan struct{})
	runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/next") {
			index := int(issued.Add(1)) - 1
			if index >= len(events) {
				if index == len(events) {
					close(done)
				}
				<-r.Context().Done()
				return
			}
			if before != nil {
				before(index)
			}
			w.Header().Set("Lambda-Runtime-Aws-Request-Id", strconv.Itoa(index))
			w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(5*time.Second).UnixMilli(), 10))
			_ = json.NewEncoder(w).Encode(map[string]any{"version": "2.0", "rawPath": events[index].path, "headers": map[string]string{"host": "seed.example.test"}, "requestContext": map[string]any{"domainName": "seed.example.test", "http": map[string]string{"method": "GET", "path": events[index].path}}})
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
				t.Error("missing seed Lambda stream metadata")
				w.WriteHeader(500)
				return
			}
			if err := json.Unmarshal(data[:split], &envelope); err != nil {
				t.Error(err)
			}
			payload = data[split+8:]
		}
		index := int(reports.Add(1)) - 1
		if index >= len(events) {
			t.Error("unexpected seed Lambda response")
			w.WriteHeader(500)
			return
		}
		event := events[index]
		if envelope.StatusCode != event.status {
			t.Errorf("seed Lambda %s event%d status%d want%d: %s", mode, index, envelope.StatusCode, event.status, payload)
		}
		if bytes.Contains(payload, []byte(durableCacheSecret)) {
			t.Error("seed Lambda leaked authority authentication")
		}
		if event.status == 200 {
			slug := strings.TrimPrefix(event.path, "/seed/")
			for _, part := range []string{"<title>Seed " + slug + " " + event.generation + "</title>", `data-seed-generation="">` + event.generation + "</code>", `aria-label="Seed counter"`} {
				if !bytes.Contains(payload, []byte(part)) {
					t.Errorf("seed Lambda event%d missing%s: %s", index, part, payload)
				}
			}
			if sharedEnvelopeHeader(envelope.Headers, "X-Nextgo-Prerender") != "build" || sharedEnvelopeHeader(envelope.Headers, "X-Nextgo-Cache") != "fresh" {
				t.Errorf("seed Lambda provenance/cache: %v", envelope.Headers)
			}
		} else if bytes.Contains(payload, []byte("build-lambda")) || !bytes.Contains(payload, []byte("Something went wrong")) {
			t.Error("seed Lambda revived invalidated content or lost generic error")
		}
		w.WriteHeader(202)
	}))
	defer runtime.Close()
	worker := seedChild(t, endpoint, artifact, "source-offline", true, time.Hour, "AWS_LAMBDA_RUNTIME_API="+strings.TrimPrefix(runtime.URL, "http://"), "NEXTGO_LAMBDA_RESPONSE_MODE="+mode)
	defer closeSeedChild(worker) // release an outstanding /next before runtime.Close on a test failure
	select {
	case <-done:
	case <-worker.done:
		t.Fatalf("seed Lambda worker stopped: %v\n%s", worker.err, worker.logs.String())
	case <-time.After(15 * time.Second):
		t.Fatal("seed Lambda flow timed out", worker.logs.String())
	}
	worker.stop(t)
	if reports.Load() != int32(len(events)) {
		t.Fatal("missing seed Lambda reports", reports.Load(), len(events))
	}
}
func TestSharedPrerenderLambdaExecutable(t *testing.T) {
	for _, mode := range []string{"buffered", "streaming"} {
		t.Run(mode, func(t *testing.T) {
			dir, addr := durableDirectory(t), address(t)
			authority, backend := openDurableCacheAt(t, dir, addr)
			endpoint := "http://" + addr
			artifact := buildSeedFixture(t, endpoint, "build-lambda", time.Hour)
			seedLambdaEvents(t, mode, endpoint, artifact, []seedLambdaEvent{{"/seed/alpha", "build-lambda", 200}, {"/seed/beta", "build-lambda", 200}, {"/seed/alpha", "", 500}}, func(index int) {
				if index == 2 {
					if n, err := backend.Expire(context.Background(), "demo", shared.Selector{Kind: "path", Value: "/seed/alpha"}); err != nil || n != 1 {
						t.Error("seed Lambda invalidation", n, err)
					}
				}
			})
			killDurableCache(t, authority)
			authority, _ = openDurableCacheAt(t, dir, addr)
			seedLambdaEvents(t, mode, endpoint, artifact, []seedLambdaEvent{{"/seed/beta", "build-lambda", 200}, {"/seed/alpha", "", 500}}, nil)
			authority.stop(t)
		})
	}
}

type seedSource struct {
	Generation string
	Fail       bool
}

func writeSeedSource(t *testing.T, path, generation string, fail bool) {
	t.Helper()
	data, err := json.Marshal(seedSource{Generation: generation, Fail: fail})
	if err != nil {
		t.Fatal(err)
	}
	staging := path + ".next"
	if err := os.WriteFile(staging, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(staging, path); err != nil {
		t.Fatal(err)
	}
}

type seedCommittedPage struct {
	Format      int               `json:"format"`
	Binding     string            `json:"binding"`
	HTML        string            `json:"html"`
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Meta        map[string]string `json:"meta"`
	Document    nextgo.Document   `json:"document"`
	Head        []seo.Element     `json:"head"`
	GeneratedAt time.Time         `json:"generatedAt"`
	Prerendered bool              `json:"prerendered"`
}
type seedAcceptedCommit struct {
	ID   shared.ID
	Page seedCommittedPage
}

type seedRefreshFence struct {
	started chan string
	permit  chan struct{}
	next    chan struct{}
	began   atomic.Bool
	closed  atomic.Bool
}

func (f *seedRefreshFence) release() {
	if f.closed.CompareAndSwap(false, true) {
		close(f.permit)
	}
}

// The observer forwards to the actual file-backed authority. It observes the
// accepted publication itself; it never supplies a loader or repairs a read.
func seedRecordingProxy(t *testing.T, target string, allowed *atomic.Bool, fence *seedRefreshFence) (*httptest.Server, <-chan seedAcceptedCommit, <-chan shared.ID) {
	t.Helper()
	committed := make(chan seedAcceptedCommit, 8)
	released := make(chan shared.ID, 8)
	transport := &http.Transport{DisableKeepAlives: true}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Timeout: 5 * time.Second, Transport: transport}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, err := io.ReadAll(io.LimitReader(r.Body, (2<<20)+1))
		if err != nil {
			t.Error(err)
			w.WriteHeader(503)
			return
		}
		var operation struct {
			Operation string
			ID        shared.ID
			Value     []byte
		}
		if err := json.Unmarshal(data, &operation); err != nil {
			t.Error(err)
			w.WriteHeader(503)
			return
		}
		if (operation.Operation == "commit" || operation.Operation == "release" || operation.Operation == "discard") && !allowed.Load() {
			t.Errorf("shared prerender %s began before Runtime API accepted its response", operation.Operation)
		}
		if (operation.Operation == "commit" || operation.Operation == "release" || operation.Operation == "discard") && fence.began.CompareAndSwap(false, true) {
			fence.started <- operation.Operation
			select {
			case <-fence.permit:
			case <-r.Context().Done():
				w.WriteHeader(503)
				return
			}
		}
		request, err := http.NewRequestWithContext(r.Context(), "POST", target, bytes.NewReader(data))
		if err != nil {
			t.Error(err)
			w.WriteHeader(503)
			return
		}
		request.Header = r.Header.Clone()
		response, err := client.Do(request)
		if err != nil {
			t.Error("authority forwarding failed")
			w.WriteHeader(503)
			return
		}
		payload, err := io.ReadAll(io.LimitReader(response.Body, (2<<20)+1))
		response.Body.Close()
		if err != nil {
			t.Error(err)
			w.WriteHeader(503)
			return
		}
		if response.StatusCode == 200 {
			if operation.Operation == "commit" {
				var result struct{ View *shared.View }
				if err := json.Unmarshal(payload, &result); err != nil {
					t.Error(err)
				}
				if result.View != nil && result.View.Found {
					var page seedCommittedPage
					if err := json.Unmarshal(operation.Value, &page); err != nil {
						t.Error(err)
					}
					select {
					case committed <- seedAcceptedCommit{ID: operation.ID, Page: page}:
					default:
						t.Error("too many seed fixture publications")
					}
				}
			} else if operation.Operation == "release" || operation.Operation == "discard" {
				select {
				case released <- operation.ID:
				default:
					t.Error("too many seed fixture retirements")
				}
			}
		}
		for key, values := range response.Header {
			w.Header()[key] = append([]string(nil), values...)
		}
		w.WriteHeader(response.StatusCode)
		_, _ = w.Write(payload)
	}))
	t.Cleanup(server.Close)
	return server, committed, released
}
func verifySeedCommit(t *testing.T, commit seedAcceptedCommit, generation string) {
	t.Helper()
	page := commit.Page
	if page.Format != 1 || page.Prerendered || page.Binding != commit.ID.Key || page.GeneratedAt.IsZero() || page.Meta["seed-generation"] != generation || page.Title != "Seed alpha "+generation || page.Description != "Generation "+generation || page.Document.Language != "en" || page.Document.Direction != "ltr" || !strings.Contains(page.HTML, `data-seed-generation="">`+generation+"</code>") || !strings.Contains(page.HTML, `aria-label="Seed counter"`) {
		t.Errorf("accepted refresh did not publish one complete runtime snapshot: %+v", page)
	}
	found := false
	for _, element := range page.Head {
		if element.Tag == "meta" && element.Attrs["property"] == "og:title" && element.Attrs["content"] == page.Title {
			found = true
		}
	}
	if !found {
		t.Error("accepted runtime snapshot lost structured OpenGraph head")
	}
}
func TestSharedPrerenderLambdaRefreshOrderingExecutable(t *testing.T) {
	for _, mode := range []string{"buffered", "streaming"} {
		for _, fail := range []bool{false, true} {
			name := "healthy"
			if fail {
				name = "failure-hard-expiry-recovery"
			}
			t.Run(mode+"/"+name, func(t *testing.T) {
				const fresh = time.Second
				dir, addr := durableDirectory(t), address(t)
				authority, backend := openDurableCacheAt(t, dir, addr)
				defer authority.stop(t)
				var allowed atomic.Bool
				allowed.Store(true)
				fence := &seedRefreshFence{started: make(chan string, 1), permit: make(chan struct{}), next: make(chan struct{}, 1)}
				defer fence.release()
				proxy, committed, released := seedRecordingProxy(t, "http://"+addr, &allowed, fence)
				artifact := buildSeedFixture(t, proxy.URL, "build-stale", fresh)
				source := filepath.Join(filepath.Dir(artifact), "source.json")
				writeSeedSource(t, source, "runtime-next", fail)
				// Ticket age, not app startup time, controls seed freshness. No authority
				// lookup is used here; doing so could acquire a lease and hide a broken job.
				time.Sleep(fresh + 100*time.Millisecond)
				allowed.Store(false)
				events := 2
				if fail {
					events = 3
				}
				var issued, reports atomic.Int32
				done := make(chan struct{})
				runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasSuffix(r.URL.Path, "/next") {
						index := int(issued.Add(1)) - 1
						if index >= events {
							if index == events {
								close(done)
							}
							<-r.Context().Done()
							return
						}
						if index == 1 {
							select {
							case fence.next <- struct{}{}:
							default:
							}
							if fail {
								select {
								case <-released:
								default:
									t.Error("Lambda requested another invocation before failed seeded refresh retired its lease")
								}
								n, err := backend.MarkStale(r.Context(), "demo", shared.Selector{Kind: "path", Value: "/seed/alpha"}, time.Millisecond)
								if err != nil || n != 1 {
									t.Error("clip failed seed generation hard deadline", n, err)
								}
								time.Sleep(3 * time.Millisecond)
							} else {
								select {
								case result := <-committed:
									verifySeedCommit(t, result, "runtime-next")
								default:
									t.Error("Lambda requested next invocation before accepted whole-snapshot refresh publication")
								}
							}
						}
						if index == 2 {
							select {
							case <-released:
							default:
								t.Error("failed hard-expiry load did not retire its ownership before the next event")
							}
							writeSeedSource(t, source, "runtime-recovered", false)
						}
						w.Header().Set("Lambda-Runtime-Aws-Request-Id", strconv.Itoa(index))
						w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(5*time.Second).UnixMilli(), 10))
						_ = json.NewEncoder(w).Encode(map[string]any{"version": "2.0", "rawPath": "/seed/alpha", "headers": map[string]string{"host": "seed.example.test"}, "requestContext": map[string]any{"domainName": "seed.example.test", "http": map[string]string{"method": "GET", "path": "/seed/alpha"}}})
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
							t.Error("missing seed refresh stream delimiter")
							w.WriteHeader(500)
							return
						}
						if err := json.Unmarshal(data[:split], &envelope); err != nil {
							t.Error(err)
						}
						payload = data[split+8:]
					}
					index := int(reports.Add(1)) - 1
					if index >= events {
						t.Error("unexpected seed refresh Lambda response")
						w.WriteHeader(500)
						return
					}
					expectedStatus, expectedGeneration, expectedSource, expectedCache := 200, "build-stale", "build", "stale"
					if index == 1 {
						if fail {
							expectedStatus = 500
							expectedGeneration = ""
							expectedSource = ""
							expectedCache = ""
						} else {
							expectedGeneration = "runtime-next"
							expectedSource = "runtime"
							expectedCache = "fresh"
						}
					}
					if index == 2 {
						expectedGeneration = "runtime-recovered"
						expectedSource = "runtime"
						expectedCache = "miss"
					}
					if envelope.StatusCode != expectedStatus || sharedEnvelopeHeader(envelope.Headers, "X-Nextgo-Prerender") != expectedSource || sharedEnvelopeHeader(envelope.Headers, "X-Nextgo-Cache") != expectedCache {
						t.Errorf("seed refresh event%d status/provenance/cache got%d/%s/%s want%d/%s/%s: %s", index, envelope.StatusCode, sharedEnvelopeHeader(envelope.Headers, "X-Nextgo-Prerender"), sharedEnvelopeHeader(envelope.Headers, "X-Nextgo-Cache"), expectedStatus, expectedSource, expectedCache, payload)
					}
					if expectedStatus == 200 {
						for _, part := range []string{"<title>Seed alpha " + expectedGeneration + "</title>", `data-seed-generation="">` + expectedGeneration + "</code>", `aria-label="Seed counter"`} {
							if !bytes.Contains(payload, []byte(part)) {
								t.Errorf("seed refresh event%d missing%s", index, part)
							}
						}
					} else if bytes.Contains(payload, []byte("build-stale")) || !bytes.Contains(payload, []byte("Something went wrong")) {
						t.Error("hard-expiry failure served the old seed or lost generic error")
					}
					// Permit work only at the acceptance boundary. The next-event checks above
					// inspect the recorded publication/retirement before delivering a read.
					allowed.Store(true)
					w.WriteHeader(202)
				}))
				defer runtime.Close()
				worker := seedChild(t, proxy.URL, artifact, "runtime-next", false, fresh, "NEXTGO_TEST_SEED_SOURCE_FILE="+source, "AWS_LAMBDA_RUNTIME_API="+strings.TrimPrefix(runtime.URL, "http://"), "NEXTGO_LAMBDA_RESPONSE_MODE="+mode)
				defer closeSeedChild(worker) // close the long poll before deferred runtime cleanup
				// Keep confirmed live completion work blocked before it reaches the
				// authority. A worker that polls /next without draining cannot pass
				// merely because its callback happened to finish quickly.
				select {
				case <-fence.started:
				case <-worker.done:
					t.Fatalf("seed completion never began: %v\n%s", worker.err, worker.logs.String())
				case <-time.After(10 * time.Second):
					t.Fatal("seed completion never reached publication/retirement fence", worker.logs.String())
				}
				select {
				case <-fence.next:
					t.Error("Lambda requested /next while confirmed completion work remained blocked")
				case <-time.After(100 * time.Millisecond):
				}
				fence.release()
				select {
				case <-done:
				case <-worker.done:
					t.Fatalf("seed refresh worker stopped: %v\n%s", worker.err, worker.logs.String())
				case <-time.After(20 * time.Second):
					t.Fatal("seed refresh runtime flow timed out", worker.logs.String())
				}
				worker.stop(t)
				if reports.Load() != int32(events) {
					t.Fatal("missing seed refresh reports", reports.Load(), events)
				}
				if fail {
					select {
					case result := <-committed:
						verifySeedCommit(t, result, "runtime-recovered")
					default:
						t.Error("recovered source never published a complete new generation")
					}
				}
			})
		}
	}
}
