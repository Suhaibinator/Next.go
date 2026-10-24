package serverless

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/ui"
)

func TestRuntimeCacheRefreshPublishesStaleThenDrainsBeforeNextEvent(t *testing.T) {
	for _, mode := range []string{"age", "mark", "never"} {
		for _, streaming := range []bool{false, true} {
			t.Run(mode+"/"+strconv.FormatBool(streaming), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				var clock, revision, loads, nexts atomic.Int64
				clock.Store(time.Now().UnixNano())
				revision.Store(1)
				reportSeen, accept, started, release, nextAgain := make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{}), make(chan struct{})
				acceptOnce := sync.OnceFunc(func() { close(accept) })
				defer acceptOnce()
				releaseOnce := sync.OnceFunc(func() { close(release) })
				defer releaseOnce()
				expire := time.Minute
				if mode == "never" {
					expire = cache.Never
				}
				app, err := nextgo.New(nextgo.Config{Cache: cache.New(4, cache.WithClock(func() time.Time { return time.Unix(0, clock.Load()) })), Routes: []nextgo.Route{{Pattern: "/", Cache: &nextgo.CachePolicy{Lifetime: &cache.Lifetime{Revalidate: 10 * time.Second, Expire: expire}, Tags: []string{"snapshot"}}, Page: func(c *nextgo.Context) (nextgo.Page, error) {
					value := revision.Load()
					if loads.Add(1) == 2 {
						close(started)
						select {
						case <-release:
						case <-c.Context.Done():
							return nextgo.Page{}, c.Context.Err()
						}
					}
					return nextgo.Page{Title: fmt.Sprintf("Revision %d", value), Body: ui.Text(fmt.Sprintf("snapshot %d", value))}, nil
				}}}})
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					drain, stop := context.WithTimeout(context.Background(), time.Second)
					defer stop()
					_ = app.Close(drain)
				}()
				warm := httptest.NewRecorder()
				app.ServeHTTP(warm, httptest.NewRequest("GET", "https://site.example/", nil))
				if warm.Code != 200 || loads.Load() != 1 {
					t.Fatal("warm snapshot not cached", warm.Code, loads.Load())
				}
				revision.Store(2)
				if mode == "age" {
					clock.Add(int64(10 * time.Second))
				} else {
					profile := "seconds"
					if mode == "never" {
						profile = "default"
					}
					if err := app.RevalidateTagProfile("snapshot", profile); err != nil {
						t.Fatal(err)
					}
				}
				runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if strings.HasSuffix(r.URL.Path, "/next") {
						index := nexts.Add(1)
						if index > 1 {
							if index == 2 {
								close(nextAgain)
							} else {
								w.WriteHeader(503)
								return
							}
						}
						id := "stale"
						if index == 2 {
							id = "fresh"
						}
						w.Header().Set("Lambda-Runtime-Aws-Request-Id", id)
						w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(time.Minute).UnixMilli(), 10))
						_ = json.NewEncoder(w).Encode(Request{Version: "2.0", RawPath: "/", Headers: map[string]string{"host": "site.example"}, RequestContext: RequestContext{HTTP: HTTPContext{Method: "GET", Protocol: "HTTP/1.1"}}})
						return
					}
					payload, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
					}
					want := "snapshot 2"
					if strings.Contains(r.URL.Path, "/stale/") {
						want = "snapshot 1"
					}
					if !strings.HasSuffix(r.URL.Path, "/response") || !strings.Contains(string(payload), want) || !strings.Contains(string(payload), "no-store") {
						t.Errorf("incorrect lifetime response %s: %s", r.URL.Path, payload)
					}
					if want == "snapshot 1" {
						close(reportSeen)
						select {
						case <-accept:
						case <-r.Context().Done():
							return
						}
					}
					w.WriteHeader(202)
				}))
				defer func() { acceptOnce(); releaseOnce(); cancel(); runtime.Close() }()
				done := make(chan error, 1)
				go func() {
					done <- runWithOptions(ctx, app, runtime.URL, runtime.Client(), runtimeOptions{streaming: streaming})
				}()
				select {
				case <-reportSeen:
				case <-ctx.Done():
					t.Fatal("stale response not published")
				}
				select {
				case <-started:
					t.Fatal("refresh started before publication accepted")
				case <-time.After(25 * time.Millisecond):
				}
				acceptOnce()
				select {
				case <-started:
				case <-ctx.Done():
					t.Fatal("refresh never started")
				}
				select {
				case <-nextAgain:
					t.Fatal("runtime advanced before refresh completed")
				case <-time.After(25 * time.Millisecond):
				}
				releaseOnce()
				select {
				case err := <-done:
					if err == nil {
						t.Fatal("expected final Runtime API error")
					}
				case <-ctx.Done():
					t.Fatal("refresh not drained")
				}
				if loads.Load() != 2 || nexts.Load() != 3 {
					t.Fatal("fresh invocation missed refreshed snapshot", loads.Load(), nexts.Load())
				}
			})
		}
	}
}

func TestBodyBearingLambdaGETBypassesPublicSnapshots(t *testing.T) {
	store := cache.New(4)
	app, err := nextgo.New(nextgo.Config{Cache: store, Routes: []nextgo.Route{{Pattern: "/", Cache: &nextgo.CachePolicy{Lifetime: &cache.Lifetime{Revalidate: time.Second, Expire: time.Minute}}, Page: func(c *nextgo.Context) (nextgo.Page, error) {
		body, err := io.ReadAll(c.Request.Body)
		return nextgo.Page{Body: ui.Text(string(body))}, err
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close(context.Background())
	for _, value := range []string{"first body", "second body"} {
		response, err := Adapt(context.Background(), app, Request{Version: "2.0", RawPath: "/", Body: value, Headers: map[string]string{"host": "site.example"}, RequestContext: RequestContext{HTTP: HTTPContext{Method: "GET", Protocol: "HTTP/1.1"}}})
		if err != nil || response.StatusCode != 200 || !strings.Contains(response.Body, value) || response.Headers["cache-control"] != "no-store" {
			t.Fatal("body-dependent Lambda page shared", response, err)
		}
	}
	if store.Len() != 0 {
		t.Fatal("GET body entered snapshot cache")
	}
}
