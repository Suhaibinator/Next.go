package nextgo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/internal/postresponse"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

func TestProfileRevalidationRefreshesSnapshotWithFreshMemo(t *testing.T) {
	for _, mode := range []string{"tag", "path"} {
		t.Run(mode, func(t *testing.T) {
			var revision, reads atomic.Int64
			revision.Store(1)
			read := func(c *Context) (int64, error) {
				return Memo(c.Context, "revision", func(context.Context) (int64, error) { reads.Add(1); return revision.Load(), nil })
			}
			app := testApp(t, Config{CacheProfiles: map[string]cache.Profile{"publish": {Revalidate: time.Second, Expire: 5 * time.Second}}, Routes: []Route{{Pattern: "/posts/:slug", Cache: &CachePolicy{Profile: "hours", Tags: []string{"posts"}}, Page: func(c *Context) (Page, error) {
				value, err := read(c)
				return Page{Body: ui.Text(fmt.Sprint(value))}, err
			}, Metadata: []MetadataLayer{{Load: func(c *Context, _ seo.Metadata) (seo.Metadata, error) {
				value, err := read(c)
				return seo.Metadata{Title: fmt.Sprintf("Revision %d", value)}, err
			}}}}}})
			request := func() (Envelope, *postresponse.Collector) {
				t.Helper()
				ctx, done := postresponse.Collect(context.Background())
				r := httptest.NewRequest("GET", "/posts/a?q=public", nil).WithContext(ctx)
				r.Header.Set("X-Nextgo-Navigate", "1")
				w := httptest.NewRecorder()
				app.ServeHTTP(w, r)
				var e Envelope
				if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || w.Code != 200 {
					t.Fatal(err, w.Body.String())
				}
				return e, done
			}
			first, done := request()
			done.Finish(context.Background(), true)
			revision.Store(2)
			mark := func(name string) error {
				if mode == "tag" {
					return app.RevalidateTagProfile("posts", name)
				}
				return app.RevalidatePathProfile("/posts/", name)
			}
			if err := mark("missing"); err == nil {
				t.Fatal("unknown profile accepted")
			}
			unchanged, done := request()
			done.Finish(context.Background(), true)
			if unchanged.HTML != "1" || reads.Load() != 1 {
				t.Fatal("failed validation changed cache", unchanged, reads.Load())
			}
			if err := mark("publish"); err != nil {
				t.Fatal(err)
			}
			stale, done := request()
			if first.HTML != "1" || stale.HTML != "1" || stale.Title != "Revision 1" || reads.Load() != 1 {
				t.Fatal("stale response failed", stale, reads.Load())
			}
			done.Finish(context.Background(), true)
			fresh, done := request()
			done.Finish(context.Background(), true)
			if fresh.HTML != "2" || fresh.Title != "Revision 2" || reads.Load() != 2 {
				t.Fatal("refresh reused old memo/body/head", fresh, reads.Load())
			}
		})
	}
}
