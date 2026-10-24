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
				if fail {
					return nextgo.Page{}, errors.New("seed fixture source unavailable")
				}
				title := "Seed " + c.Params["slug"] + " " + generation
				return nextgo.Page{Title: title, Description: "Generation " + generation, Meta: map[string]string{"seed-generation": generation}, Metadata: seo.Metadata{Language: "en", Direction: "ltr", OpenGraph: seo.OpenGraph{Title: title, Type: "article"}}, Body: ui.El("article", nil, ui.El("h1", nil, ui.Text(c.Params["slug"])), ui.El("code", ui.Attrs{"data-seed-generation": ""}, ui.Text(generation)), counter.View(counter.Props{Label: "Seed counter", Initial: 3}))}, nil
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
