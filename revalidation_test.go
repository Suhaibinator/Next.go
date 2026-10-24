package nextgo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/actions"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

func TestRevalidationRefreshesAlreadyWaitingPublicResponses(t *testing.T) {
	for _, mode := range []string{"tag", "path", "action-tag", "action-path"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			var revision, loads, memoReads atomic.Int64
			revision.Store(1)
			started, release := make(chan struct{}), make(chan struct{})
			releaseOld := sync.OnceFunc(func() { close(release) })
			defer releaseOld()
			read := func(c *Context) (int64, error) {
				return Memo(c.Context, "revision", func(context.Context) (int64, error) {
					memoReads.Add(1)
					return revision.Load(), nil
				})
			}
			var app *App
			router := actions.New(actions.Options{OnRevalidate: func(tags, paths []string) {
				for _, tag := range tags {
					app.RevalidateTag(tag)
				}
				for _, path := range paths {
					app.RevalidatePath(path)
				}
			}})
			router.Handle("publish", func(*actions.Context) (actions.Result, error) {
				revision.Store(2) // Commit data before expiring cached reads.
				if mode == "action-tag" {
					return actions.Result{RevalidateTags: []string{"posts"}}, nil
				}
				return actions.Result{RevalidatePaths: []string{"/posts"}}, nil
			})
			app = testApp(t, Config{Actions: router, Routes: []Route{{Pattern: "/posts/:slug", Cache: &CachePolicy{TTL: time.Minute, Tags: []string{"posts"}}, Page: func(c *Context) (Page, error) {
				value, err := read(c)
				if err != nil {
					return Page{}, err
				}
				if loads.Add(1) == 1 {
					close(started)
					<-release
				}
				return Page{Body: ui.Text(fmt.Sprint(value))}, nil
			}, Metadata: []MetadataLayer{{Load: func(c *Context, _ seo.Metadata) (seo.Metadata, error) {
				value, err := read(c)
				return seo.Metadata{Title: fmt.Sprintf("Revision %d", value)}, err
			}}}}}})
			results := make(chan *httptest.ResponseRecorder, 1)
			go func() {
				w := httptest.NewRecorder()
				r := httptest.NewRequest("GET", "/posts/a?filter=recent", nil).WithContext(ctx)
				r.Header.Set("X-Nextgo-Navigate", "1")
				app.ServeHTTP(w, r)
				results <- w
			}()
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal("initial public load not started")
			}
			if strings.HasPrefix(mode, "action-") {
				r := httptest.NewRequest("POST", "http://example.com/_nextgo/actions/publish", strings.NewReader("{}"))
				r.Header.Set("Content-Type", "application/json")
				r.Header.Set("Origin", "http://example.com")
				w := httptest.NewRecorder()
				app.ServeHTTP(w, r)
				if w.Code != 200 {
					t.Fatal("mutation failed", w.Code, w.Body.String())
				}
			} else {
				revision.Store(2)
				if mode == "tag" {
					app.RevalidateTag("posts")
				} else {
					app.RevalidatePath("/posts")
				}
			}
			select {
			case w := <-results:
				var data Envelope
				if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil || w.Code != 200 || data.HTML != "2" || data.Title != "Revision 2" {
					t.Fatal("pending response returned obsolete body/head", w.Code, data, err)
				}
			case <-ctx.Done():
				t.Fatal("revalidation waited on obsolete renderer")
			}
			releaseOld()
			_, data := runtimeEnvelope(t, app, "/posts/a?filter=recent", nil)
			if data.HTML != "2" || data.Title != "Revision 2" || loads.Load() != 2 || memoReads.Load() != 2 {
				t.Fatal("replacement did not preserve fresh memo/head snapshot", data, loads.Load(), memoReads.Load())
			}
		})
	}
}

func TestRevalidationGivesEachRetryOwnedParametersAndQueries(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	finishOld := sync.OnceFunc(func() { close(release) })
	defer finishOld()
	var calls atomic.Int64
	app := testApp(t, Config{Routes: []Route{{Pattern: "/:slug", Cache: &CachePolicy{TTL: time.Minute, Tags: []string{"page"}}, Page: func(c *Context) (Page, error) {
		value := c.Params["slug"] + "|" + c.Query.Get("q")
		c.Params["slug"] = "mutated"
		c.Query.Set("q", "mutated")
		if calls.Add(1) == 1 {
			close(started)
			<-release
		}
		return Page{Body: ui.Text(value)}, nil
	}}}})
	done := make(chan Envelope, 1)
	go func() { _, data := runtimeEnvelope(t, app, "/original?q=public", nil); done <- data }()
	<-started
	app.RevalidateTag("page")
	select {
	case data := <-done:
		if data.HTML != "original|public" {
			t.Fatal("retry reused mutable maps", data)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("retry stranded")
	}
}
