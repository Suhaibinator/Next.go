//go:build !js

// Package site contains public demonstration pages shared as Go source by two
// independently compiled applications. It does not select a zone from a header.
package site

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/actions"
	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

type stateKey struct{}
type state struct {
	revision atomic.Int64
	cache    *cache.Store
}

func requestState(ctx context.Context) *state { s, _ := ctx.Value(stateKey{}).(*state); return s }

func Configure(c *nextgo.Config, app string) {
	c.PreserveLayouts = true
	s := &state{cache: cache.New(32, cache.WithMaxBytes(1<<20, nil))}
	c.Cache = s.cache
	c.Middleware = append(c.Middleware, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), stateKey{}, s)))
		})
	})
	router := actions.New(actions.Options{MaxBodyBytes: 2048, OnRevalidate: func(tags, paths []string) {
		for _, tag := range tags {
			s.cache.ExpireTag(tag)
		}
	}})
	router.Handle("preview", func(ctx *actions.Context) (actions.Result, error) {
		values, err := ctx.ParseForm()
		if err != nil {
			return actions.Result{}, err
		}
		if len(values) != 1 || len(values["note"]) != 1 || len(values.Get("note")) > 80 {
			return actions.Result{}, &actions.ValidationError{Message: "Use one public note of at most 80 bytes."}
		}
		info := nextgo.CurrentZone(ctx)
		revision := s.revision.Add(1)
		return actions.Result{Data: map[string]any{"app": app, "zone": info.ID, "revision": revision, "note": values.Get("note")}, Redirect: "/?preview=1", RevalidateTags: []string{"shared-tag"}}, nil
	})
	c.Actions = router
	if app == "marketing" {
		info := c.Zone()
		origin := "https://example.test"
		if info.Host != "" {
			origin = "http://" + info.Host
		}
		c.MetadataFiles.Sitemap = func(ctx context.Context) ([]seo.SitemapEntry, error) {
			var entries []seo.SitemapEntry
			for _, path := range []string{"/", "/details", "/cached", "/posts/welcome"} {
				mounted, err := nextgo.URL(ctx, path)
				if err != nil {
					return nil, err
				}
				entries = append(entries, seo.SitemapEntry{URL: origin + mounted})
			}
			return entries, ctx.Err()
		}
	}
}

func Layout(c *nextgo.Context, body ui.Node, app string, counter ui.Node) (ui.Node, error) {
	other, label := "/blog", "Visit the blog zone"
	if app == "blog" {
		other, label = "/", "Visit the marketing zone"
	}
	return ui.Fragment(ui.El("header", ui.Attrs{"data-zone-layout": app}, ui.El("p", ui.Attrs{"class": "eyebrow"}, ui.Text("NEXT.GO / "+strings.ToUpper(app))), ui.El("nav", ui.Attrs{"aria-label": app + " navigation"}, ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/")}, ui.Text(app+" home")), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/details")}, ui.Text("Same-zone details")), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/cached")}, ui.Text("Zone cache")), ui.El("a", ui.Attrs{"href": other, "data-zone-cross-link": ""}, ui.Text(label))), counter), ui.El("main", ui.Attrs{"id": "main", "tabindex": "-1"}, body), ui.El("footer", nil, ui.Text("Independent Go application. Public, process-local demonstration state."))), nil
}

func Page(c *nextgo.Context, app string) (nextgo.Page, error) {
	info := nextgo.CurrentZone(c)
	return nextgo.Page{Title: app + " — Next.go zones", Body: ui.El("article", ui.Attrs{"data-zone-page": app}, ui.El("h1", nil, ui.Text("The "+app+" application")), ui.El("p", ui.Attrs{"class": "lead"}, ui.Text("This application owns its pages, public assets, Go counter and native actions. A different application owns the other path on this host.")), ui.El("p", ui.Attrs{"data-zone-context": ""}, ui.Text(fmt.Sprintf("zone=%s tenant=%s host=%s base=%s", info.ID, info.Tenant, info.Host, info.BasePath))), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/posts/welcome")}, ui.Text("Read a dynamic route")), ui.El("form", ui.Attrs{"method": "post", "action": nextgo.MustURL(c, "/_nextgo/actions/preview"), "data-zone-action": ""}, ui.El("label", nil, ui.Text("Public note "), ui.El("input", ui.Attrs{"name": "note", "value": "Hello " + app, "maxlength": "80"})), ui.El("button", ui.Attrs{"type": "submit"}, ui.Text("Preview "+app+" action"))), ui.El("p", ui.Attrs{"data-zone-receipt": ""}, ui.Text("Preview flag: "+c.Query.Get("preview"))), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/api/status"), "data-nextgo-reload": "true"}, ui.Text("Inspect zone API")))}, nil
}

func Details(c *nextgo.Context, app string) (nextgo.Page, error) {
	return nextgo.Page{Title: app + " details — Next.go zones", Body: ui.El("article", ui.Attrs{"data-zone-details": app}, ui.El("h1", nil, ui.Text(app+" details")), ui.El("p", nil, ui.Text("Same-zone navigation retains this application's root layout and Go counter.")), ui.El("a", ui.Attrs{"href": nextgo.MustURL(c, "/")}, ui.Text("Return to "+app)))}, nil
}
func Post(c *nextgo.Context, app string) (nextgo.Page, error) {
	slug := c.Params["slug"]
	if slug != "welcome" && slug != "ownership" {
		return nextgo.Page{}, nextgo.ErrNotFound
	}
	return nextgo.Page{Title: app + " post — " + slug, Body: ui.El("article", ui.Attrs{"data-zone-post": app, "data-zone-slug": slug}, ui.El("h1", nil, ui.Text(app+": "+slug)), ui.El("p", nil, ui.Text("The same logical dynamic route belongs to a different application at each mount.")))}, nil
}
func Cached(c *nextgo.Context, app string) (nextgo.Page, error) {
	s := requestState(c)
	if s == nil {
		return nextgo.Page{}, fmt.Errorf("missing sample state")
	}
	info := nextgo.CurrentZone(c)
	value, err := cache.GetOrLoad(c, s.cache, "shared-key", cache.Options{TTL: time.Minute, Tags: []string{"shared-tag"}}, func(context.Context) (string, error) { return fmt.Sprintf("%s:%d", info.ID, s.revision.Load()), nil })
	if err != nil {
		return nextgo.Page{}, err
	}
	return nextgo.Page{Title: app + " cache", Body: ui.El("article", nil, ui.El("h1", nil, ui.Text(app+" public cache")), ui.El("output", ui.Attrs{"data-zone-cache": app}, ui.Text(value)), ui.El("p", nil, ui.Text("Both applications use the same logical key and tag. The local stores belong to different configured application instances. Previewing this application's action invalidates only its own tag.")))}, nil
}
func Status(w http.ResponseWriter, r *http.Request, app string) {
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(405)
		return
	}
	info := nextgo.CurrentZone(r.Context())
	revision := int64(0)
	if s := requestState(r.Context()); s != nil {
		revision = s.revision.Load()
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == "GET" {
		_ = json.NewEncoder(w).Encode(map[string]any{"app": app, "zone": info.ID, "tenant": info.Tenant, "host": info.Host, "basePath": info.BasePath, "revision": revision})
	}
}
