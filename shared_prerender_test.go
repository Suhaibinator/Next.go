package nextgo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Suhaibinator/Next.go/assets"
	"github.com/Suhaibinator/Next.go/cache/shared"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

type sharedPrerenderProbe struct {
	*shared.Memory
	reservations, sessions atomic.Int32
	age                    atomic.Int64
}

func (p *sharedPrerenderProbe) ReserveSeeds(ctx context.Context, ns string) (shared.SeedTicket, error) {
	p.reservations.Add(1)
	return p.Memory.ReserveSeeds(ctx, ns)
}
func (p *sharedPrerenderProbe) BeginSeeds(ctx context.Context, ns, token string) (shared.SeedSession, error) {
	p.sessions.Add(1)
	return p.Memory.BeginSeeds(ctx, ns, token)
}
func (p *sharedPrerenderProbe) BeginStale(ctx context.Context, id shared.ID, policy shared.Policy, lease time.Duration) (shared.View, error) {
	v, err := p.Memory.BeginStale(ctx, id, policy, lease)
	if v.Found {
		p.age.Store(int64(v.Age))
	}
	return v, err
}
func sharedPrerenderMemory(t *testing.T) (*sharedPrerenderProbe, *shared.Store) {
	t.Helper()
	m, err := shared.NewMemory(64, 16<<20)
	if err != nil {
		t.Fatal(err)
	}
	p := &sharedPrerenderProbe{Memory: m}
	return p, sharedPageStore(t, p, "prerender-integration", "snapshot-v1")
}
func sharedPrerenderRoute(page PageFunc) Route {
	return Route{Pattern: "/", Page: page, Prerender: &PrerenderPolicy{Shared: true, HostIndependent: true, Revalidate: time.Hour, Expire: 24 * time.Hour, Tags: []string{"articles"}}}
}
func buildSharedPrerenderTest(t *testing.T, c Config) (Config, []byte) {
	t.Helper()
	c.BuildID = "source-code"
	c.PrerenderBuildMode = true
	app := testApp(t, c)
	defer app.Close(context.Background())
	data, err := app.BuildPrerender(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	c.PrerenderBuildMode = false
	c.BuildID = PrerenderBuildID(c.BuildID, data)
	c.PrerenderFS = fstest.MapFS{"prerender.json": &fstest.MapFile{Data: data}}
	return c, data
}

func sharedPrerenderRewriteArtifact(t *testing.T, c Config, data []byte, mutate func(*prerenderManifest)) Config {
	t.Helper()
	var manifest prerenderManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	mutate(&manifest)
	revised, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	c.BuildID = PrerenderBuildID(manifest.Code, revised)
	c.PrerenderFS = fstest.MapFS{"prerender.json": &fstest.MapFile{Data: revised}}
	return c
}
func TestSharedPrerenderBuildReusesCompleteFrozenPageWithoutRuntimeSource(t *testing.T) {
	_, store := sharedPrerenderMemory(t)
	var broken atomic.Bool
	var calls atomic.Int32
	structured, err := seo.JSONLD(map[string]string{"name": "build </script><img src=x>"})
	if err != nil {
		t.Fatal(err)
	}
	route := sharedPrerenderRoute(func(*Context) (Page, error) {
		calls.Add(1)
		if broken.Load() {
			return Page{}, errors.New("runtime source offline")
		}
		return Page{Title: "Built <title>", Description: "built description", Meta: map[string]string{"robots": "index"}, Metadata: seo.Metadata{Language: "ar", Direction: "rtl", Canonical: "https://example.test/built"}, Resources: []assets.Resource{assets.Stylesheet("/assets/built.css")}, JSONLD: []seo.StructuredData{structured}, Body: ui.Text("meaningful <body>")}, nil
	})
	route.Layouts = []LayoutFunc{func(_ *Context, n ui.Node) (ui.Node, error) { return ui.El("article", nil, n), nil }}
	route.LayoutIDs = []string{"article-v1"}
	c, _ := buildSharedPrerenderTest(t, Config{SharedCache: store, Routes: []Route{route}})
	broken.Store(true)
	app := sharedPageApp(t, c)
	full := sharedPageRead(t, app, "http://one.example/", false, nil)
	nav := sharedPageRead(t, app, "http://other.example/", true, nil)
	e := sharedPageEnvelope(t, nav)
	if full.Code != 200 || nav.Code != 200 || e.HTML != "<article>meaningful &lt;body&gt;</article>" || e.Title != "Built <title>" || e.Description != "built description" || e.Meta["robots"] != "index" || e.Document.Language != "ar" || e.Document.Direction != "rtl" || nav.Header().Get("X-Nextgo-Prerender") != "build" || calls.Load() != 1 {
		t.Fatal(full.Code, nav.Code, e, nav.Header(), calls.Load())
	}
	var canonical, stylesheet, jsonld bool
	for _, h := range e.Head {
		canonical = canonical || h.Attrs["rel"] == "canonical" && h.Attrs["href"] == "https://example.test/built"
		stylesheet = stylesheet || h.Attrs["rel"] == "stylesheet"
		jsonld = jsonld || h.Tag == "script" && h.Attrs["type"] == "application/ld+json"
	}
	if !canonical || !stylesheet || !jsonld || strings.Contains(full.Body.String(), "</script><img src=x>") || !strings.Contains(full.Body.String(), "Built &lt;title&gt;") {
		t.Fatal("head was lost or unsafe", e.Head)
	}
	if app.config.Cache.Len() != 0 {
		t.Fatal("shared build snapshot entered a local L1")
	}
}

func TestSharedPrerenderUsesAuthorityAgeDespiteBuilderTimestamp(t *testing.T) {
	for _, generated := range []time.Time{time.Now().Add(-48 * time.Hour), time.Now().Add(48 * time.Hour)} {
		t.Run(generated.Format(time.RFC3339), func(t *testing.T) {
			probe, store := sharedPrerenderMemory(t)
			c, data := buildSharedPrerenderTest(t, Config{SharedCache: store, Routes: []Route{sharedPrerenderRoute(static("built"))}})
			c = sharedPrerenderRewriteArtifact(t, c, data, func(m *prerenderManifest) { m.Pages[0].GeneratedAt = generated })
			time.Sleep(20 * time.Millisecond)
			app := sharedPageApp(t, c)
			w := sharedPageRead(t, app, "/", true, nil)
			if w.Code != 200 || sharedPageEnvelope(t, w).HTML != "built" || w.Header().Get("X-Nextgo-Cache") != "fresh" || probe.age.Load() < int64(20*time.Millisecond) {
				t.Fatal(w.Code, w.Header(), probe.age.Load())
			}
		})
	}
}

func TestSharedPrerenderInvalidationDuringBuildRevokesSeedBeforeFirstLookup(t *testing.T) {
	selectors := []shared.Selector{{Kind: "all"}, {Kind: "key", Value: "missing-key"}, {Kind: "tag", Value: "missing-tag"}, {Kind: "tag", Value: "articles"}, {Kind: "path", Value: "/posts/go"}, {Kind: "prefix", Value: "/posts"}}
	for _, stage := range []string{"params", "render"} {
		for _, mark := range []bool{false, true} {
			for _, selector := range selectors {
				t.Run(fmt.Sprintf("%s/mark=%t/%s=%s", stage, mark, selector.Kind, selector.Value), func(t *testing.T) {
					_, store := sharedPrerenderMemory(t)
					var invalidated atomic.Bool
					var runtime atomic.Bool
					invalidate := func() {
						if invalidated.CompareAndSwap(false, true) {
							var n int
							var err error
							if mark {
								n, err = store.MarkStale(context.Background(), selector, time.Minute)
							} else {
								n, err = store.Expire(context.Background(), selector)
							}
							if err != nil || n != 0 {
								t.Fatal("expected zero-match build invalidation", n, err)
							}
						}
					}
					r := sharedPrerenderRoute(func(*Context) (Page, error) {
						if !runtime.Load() && stage == "render" {
							invalidate()
						}
						if runtime.Load() {
							return Page{Body: ui.Text("runtime")}, nil
						}
						return Page{Body: ui.Text("build")}, nil
					})
					r.Pattern = "/posts/:slug"
					r.StaticParams = func(context.Context) ([]Params, error) {
						if stage == "params" && !runtime.Load() {
							invalidate()
						}
						return []Params{{"slug": "go"}}, nil
					}
					c, _ := buildSharedPrerenderTest(t, Config{SharedCache: store, Routes: []Route{r}})
					runtime.Store(true)
					app := sharedPageApp(t, c)
					w := sharedPageRead(t, app, "/posts/go", true, nil)
					if !invalidated.Load() || w.Code != 200 || sharedPageEnvelope(t, w).HTML != "runtime" || w.Header().Get("X-Nextgo-Prerender") != "runtime" {
						t.Fatal(w.Code, w.Header(), w.Body.String())
					}
				})
			}
		}
	}
}

func TestSharedPrerenderRepeatedOnlineBuildSeedsAfterInvalidation(t *testing.T) {
	probe, store := sharedPrerenderMemory(t)
	var revision atomic.Int32
	revision.Store(1)
	route := sharedPrerenderRoute(func(*Context) (Page, error) { return Page{Body: ui.Text(fmt.Sprint(revision.Load()))}, nil })
	first, _ := buildSharedPrerenderTest(t, Config{SharedCache: store, Routes: []Route{route}})
	app := sharedPageApp(t, first)
	if w := sharedPageRead(t, app, "/", true, nil); sharedPageEnvelope(t, w).HTML != "1" {
		t.Fatal(w.Body.String())
	}
	if err := app.ExpireCache(context.Background(), shared.Selector{Kind: "all"}); err != nil {
		t.Fatal(err)
	}
	revision.Store(2)
	second, _ := buildSharedPrerenderTest(t, Config{SharedCache: store, Routes: []Route{route}})
	revision.Store(3)
	next := sharedPageApp(t, second)
	w := sharedPageRead(t, next, "/", true, nil)
	if w.Code != 200 || sharedPageEnvelope(t, w).HTML != "2" || w.Header().Get("X-Nextgo-Prerender") != "build" || probe.reservations.Load() != 2 {
		t.Fatal(w.Code, w.Header(), w.Body.String(), probe.reservations.Load())
	}
	old := sharedPageApp(t, first)
	w = sharedPageRead(t, old, "/", true, nil)
	if sharedPageEnvelope(t, w).HTML != "3" || w.Header().Get("X-Nextgo-Prerender") != "runtime" {
		t.Fatal("old artifact refilled invalidated hole", w.Header(), w.Body.String())
	}
}

func TestSharedPrerenderCanonicalPathsLocalesAndMixedLocalRoutes(t *testing.T) {
	probe, store := sharedPrerenderMemory(t)
	var calls atomic.Int32
	var broken atomic.Bool
	page := func(c *Context) (Page, error) {
		calls.Add(1)
		if broken.Load() {
			return Page{}, errors.New("source unavailable")
		}
		return Page{Body: ui.Text(CurrentLocale(c) + ":" + c.Params["slug"])}, nil
	}
	r := sharedPrerenderRoute(page)
	r.Pattern = "/posts/:slug"
	r.StaticParams = func(context.Context) ([]Params, error) { return []Params{{"slug": "café"}}, nil }
	r.Prerender.Fallback = FallbackNotFound
	local := Route{Pattern: "/local", Prerender: &PrerenderPolicy{HostIndependent: true, Revalidate: time.Hour}, Page: static("local seed")}
	c, _ := buildSharedPrerenderTest(t, Config{SharedCache: store, Locales: &LocaleConfig{Codes: []string{"en", "fr"}, Default: "en"}, Routes: []Route{r, local}})
	broken.Store(true)
	app := sharedPageApp(t, c)
	for _, path := range []string{"/posts/caf%C3%A9", "/posts/caf%c3%a9", "/fr/posts/caf%C3%A9"} {
		w := sharedPageRead(t, app, "http://alternate.example"+path, true, nil)
		e := sharedPageEnvelope(t, w)
		want := "en:café"
		if strings.HasPrefix(path, "/fr/") {
			want = "fr:café"
		}
		if w.Code != 200 || e.HTML != want || w.Header().Get("X-Nextgo-Prerender") != "build" {
			t.Fatal(path, w.Code, w.Header(), e)
		}
	}
	for _, path := range []string{"/local", "/fr/local"} {
		w := sharedPageRead(t, app, path, true, nil)
		if w.Code != 200 || sharedPageEnvelope(t, w).HTML != "local seed" {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	if probe.reservations.Load() != 1 || probe.sessions.Load() != 1 || calls.Load() != 2 {
		t.Fatal("locale aliases multiplied reservation or runtime work", probe.reservations.Load(), probe.sessions.Load(), calls.Load())
	}
	// The default-locale alias has a different URL configuration. Its links
	// require a runtime render rather than reuse of the unprefixed snapshot.
	broken.Store(false)
	alias := sharedPageRead(t, app, "/en/posts/caf%c3%a9", true, nil)
	if alias.Code != 200 || sharedPageEnvelope(t, alias).HTML != "en:café" || alias.Header().Get("X-Nextgo-Prerender") != "runtime" || calls.Load() != 3 {
		t.Fatal("alias did not preserve its configuration", alias.Code, alias.Header(), calls.Load())
	}
	w := sharedPageRead(t, app, "/posts/missing", true, nil)
	if w.Code != 404 {
		t.Fatal("fallback membership lost", w.Code)
	}
}

func TestSharedPrerenderPrivateQueryAndRuntimeConfigurationBypass(t *testing.T) {
	_, store := sharedPrerenderMemory(t)
	var calls atomic.Int32
	r := sharedPrerenderRoute(func(c *Context) (Page, error) {
		n := calls.Add(1)
		return Page{Body: ui.Text(fmt.Sprintf("%d:%s:%s", n, c.Query.Get("q"), MustURL(c, "/")))}, nil
	})
	c, _ := buildSharedPrerenderTest(t, Config{SharedCache: store, Routes: []Route{r}})
	app := sharedPageApp(t, c)
	for _, tc := range []struct {
		target  string
		headers http.Header
	}{{"/?q=private", nil}, {"/?", nil}, {"/", http.Header{"Cookie": {"session=x"}}}, {"/", http.Header{"Authorization": {"Bearer x"}}}} {
		w := sharedPageRead(t, app, tc.target, true, func(req *http.Request) {
			for k, v := range tc.headers {
				req.Header[k] = v
			}
		})
		if w.Code != 200 || w.Header().Get("X-Nextgo-Prerender") != "" || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") || strings.HasPrefix(sharedPageEnvelope(t, w).HTML, "1:") {
			t.Fatal(tc.target, w.Code, w.Header(), w.Body.String())
		}
	}
	w := sharedPageRead(t, app, "http://other.example/", true, nil)
	if sharedPageEnvelope(t, w).HTML != "1::/" || calls.Load() != 5 {
		t.Fatal(w.Body.String(), calls.Load())
	}
	changed := c
	changed.BasePath = "/mounted"
	other := sharedPageApp(t, changed)
	w = sharedPageRead(t, other, "/mounted", true, nil)
	if w.Code != 200 || w.Header().Get("X-Nextgo-Prerender") != "runtime" || !strings.Contains(sharedPageEnvelope(t, w).HTML, ":/mounted") {
		t.Fatal("changed runtime configuration reused build", w.Code, w.Header(), w.Body.String())
	}
}

func TestSharedPrerenderDurableRestartConsumesArtifactOnce(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "authority")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	open := func() *shared.File {
		f, err := shared.OpenFile(dir, shared.FileOptions{})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = f.Close() })
		return f
	}
	file := open()
	store := sharedPageStore(t, file, "durable-prerender", "snapshot-v1")
	var runtime atomic.Bool
	r := sharedPrerenderRoute(func(*Context) (Page, error) {
		if runtime.Load() {
			return Page{Body: ui.Text("runtime")}, nil
		}
		return Page{Body: ui.Text("build")}, nil
	})
	c, _ := buildSharedPrerenderTest(t, Config{SharedCache: store, Routes: []Route{r}})
	if err := file.Close(); err != nil {
		t.Fatal(err)
	} // Pending reservation survives authority restart.
	file = open()
	c.SharedCache = sharedPageStore(t, file, "durable-prerender", "snapshot-v1")
	runtime.Store(true)
	app := sharedPageApp(t, c)
	w := sharedPageRead(t, app, "/", true, nil)
	if sharedPageEnvelope(t, w).HTML != "build" {
		t.Fatal(w.Body.String())
	}
	if err := app.ExpireCache(context.Background(), shared.Selector{Kind: "all"}); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	file = open()
	c.SharedCache = sharedPageStore(t, file, "durable-prerender", "snapshot-v1")
	next := sharedPageApp(t, c)
	w = sharedPageRead(t, next, "/", true, nil)
	if w.Code != 200 || sharedPageEnvelope(t, w).HTML != "runtime" || w.Header().Get("X-Nextgo-Prerender") != "runtime" {
		t.Fatal("restart reimported consumed artifact", w.Code, w.Header(), w.Body.String())
	}
}

func TestSharedPrerenderMalformedReservationFailsBeforeConsumption(t *testing.T) {
	for _, mode := range []string{"token", "scope", "duplicate"} {
		t.Run(mode, func(t *testing.T) {
			_, store := sharedPrerenderMemory(t)
			c, data := buildSharedPrerenderTest(t, Config{SharedCache: store, Routes: []Route{sharedPrerenderRoute(static("build"))}})
			bad := sharedPrerenderRewriteArtifact(t, c, data, func(m *prerenderManifest) {
				switch mode {
				case "token":
					m.SharedSeeds[0].Ticket = "bad"
				case "scope":
					m.SharedSeeds[0].Scope = "bad"
				case "duplicate":
					m.SharedSeeds = append(m.SharedSeeds, m.SharedSeeds[0])
				}
			})
			if app, err := New(bad); err == nil {
				_ = app.Close(context.Background())
				t.Fatal("malformed reservation accepted")
			}
			// Validation failure must leave the authentic artifact's one-use ticket intact.
			app := sharedPageApp(t, c)
			w := sharedPageRead(t, app, "/", true, nil)
			if w.Code != 200 || w.Header().Get("X-Nextgo-Prerender") != "build" {
				t.Fatal(w.Code, w.Header())
			}
		})
	}
}

func TestSharedPrerenderSnapshotMatchesCredentialBypass(t *testing.T) {
	_, store := sharedPrerenderMemory(t)
	r := sharedPrerenderRoute(func(*Context) (Page, error) {
		return Page{Title: "Article", Description: "legacy description", Body: ui.Text("stable")}, nil
	})
	r.Metadata = []MetadataLayer{{Load: func(*Context, seo.Metadata) (seo.Metadata, error) {
		return seo.Metadata{TitleTemplate: "%s | Site", Description: "inherited", Canonical: "https://example.test/stable", Language: "fr"}, nil
	}}}
	c, _ := buildSharedPrerenderTest(t, Config{SharedCache: store, Routes: []Route{r}})
	app := sharedPageApp(t, c)
	seeded := sharedPageEnvelope(t, sharedPageRead(t, app, "/", true, nil))
	direct := sharedPageEnvelope(t, sharedPageRead(t, app, "/", true, func(req *http.Request) { req.Header.Set("Authorization", "Bearer test") }))
	if seeded.Title != direct.Title || seeded.Description != direct.Description || seeded.HTML != direct.HTML || !reflect.DeepEqual(seeded.Head, direct.Head) || !reflect.DeepEqual(seeded.Document, direct.Document) {
		t.Fatal("seed and ordinary render differ", seeded, direct)
	}
}

func TestSharedPrerenderOversizeSeedsRemainRunnableAndConsumed(t *testing.T) {
	_, store := sharedPrerenderMemory(t)
	var big atomic.Bool
	big.Store(true)
	var loads atomic.Int32
	r := sharedPrerenderRoute(func(*Context) (Page, error) {
		loads.Add(1)
		if big.Load() {
			return Page{Body: ui.Text(strings.Repeat("x", shared.MaxValueBytes+1))}, nil
		}
		return Page{Body: ui.Text("runtime")}, nil
	})
	c, _ := buildSharedPrerenderTest(t, Config{SharedCache: store, Routes: []Route{r}})
	app := sharedPageApp(t, c)
	w := sharedPageRead(t, app, "/", true, nil)
	if w.Code != 200 || w.Header().Get("X-Nextgo-Prerender") != "" || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") || loads.Load() != 2 {
		t.Fatal(w.Code, w.Header(), loads.Load())
	}
	big.Store(false)
	again := sharedPageApp(t, c)
	w = sharedPageRead(t, again, "/", true, nil)
	if w.Code != 200 || sharedPageEnvelope(t, w).HTML != "runtime" || w.Header().Get("X-Nextgo-Prerender") != "runtime" {
		t.Fatal(w.Code, w.Header())
	}
	if app.config.Cache.Len() != 0 {
		t.Fatal("oversize seed entered local cache")
	}
}

func TestSharedPrerenderStricterCodecAdmissionDoesNotBreakValidPage(t *testing.T) {
	_, store := sharedPrerenderMemory(t)
	title := strings.Repeat("a", maxSharedPageText+1)
	r := sharedPrerenderRoute(func(*Context) (Page, error) { return Page{Title: title, Body: ui.Text("valid large title")}, nil })
	c, _ := buildSharedPrerenderTest(t, Config{SharedCache: store, Routes: []Route{r}})
	app, err := New(c)
	if err != nil {
		t.Fatalf("valid server-rendered output failed shared seed admission: %v", err)
	}
	t.Cleanup(func() { _ = app.Close(context.Background()) })
	w := sharedPageRead(t, app, "/", true, nil)
	if w.Code != 200 || sharedPageEnvelope(t, w).Title != title || !strings.Contains(w.Header().Get("Cache-Control"), "no-store") {
		t.Fatal(w.Code, w.Header())
	}
	if err := app.ExpireCache(context.Background(), shared.Selector{Kind: "all"}); err != nil {
		t.Fatal(err)
	}
	again, err := New(c)
	if err != nil {
		t.Fatal("consumed codec-skipped artifact blocked restart", err)
	}
	defer again.Close(context.Background())
}

func TestSharedPrerenderEvictedSeedIsNotReimportedFromSameArtifact(t *testing.T) {
	memory, err := shared.NewMemory(1, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	store := sharedPageStore(t, memory, "evicted-prerender", "snapshot-v1")
	var runtime atomic.Bool
	var calls atomic.Int32
	page := func(c *Context) (Page, error) {
		calls.Add(1)
		source := "build"
		if runtime.Load() {
			source = "runtime"
		}
		return Page{Body: ui.Text(source + ":" + c.Request.URL.Path)}, nil
	}
	a, b := sharedPrerenderRoute(page), sharedPrerenderRoute(page)
	a.Pattern, b.Pattern = "/a", "/b"
	c, data := buildSharedPrerenderTest(t, Config{SharedCache: store, Routes: []Route{a, b}})
	var manifest prerenderManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	first := sharedPageApp(t, c)
	if n, _ := memory.Stats(); n != 1 {
		t.Fatal("test did not evict a seed", n)
	}
	runtime.Store(true)
	again := sharedPageApp(t, c)
	target := manifest.Pages[0].Path
	w := sharedPageRead(t, again, target, true, nil)
	if w.Code != 200 || sharedPageEnvelope(t, w).HTML != "runtime:"+target || w.Header().Get("X-Nextgo-Prerender") != "runtime" || calls.Load() != 3 {
		t.Fatal("consumed artifact refilled an evicted hole", w.Code, w.Header(), calls.Load())
	}
	// A previously constructed replica observes the authority's runtime value.
	w = sharedPageRead(t, first, target, true, nil)
	if sharedPageEnvelope(t, w).HTML != "runtime:"+target || calls.Load() != 3 {
		t.Fatal(w.Body.String(), calls.Load())
	}
}

func TestSharedPrerenderUnknownValidTicketSkipsWithoutConsumingAuthenticArtifact(t *testing.T) {
	_, store := sharedPrerenderMemory(t)
	var runtime atomic.Bool
	r := sharedPrerenderRoute(func(*Context) (Page, error) {
		if runtime.Load() {
			return Page{Body: ui.Text("runtime")}, nil
		}
		return Page{Body: ui.Text("build")}, nil
	})
	c, data := buildSharedPrerenderTest(t, Config{SharedCache: store, Routes: []Route{r}})
	unknown := sharedPrerenderRewriteArtifact(t, c, data, func(m *prerenderManifest) { m.SharedSeeds[0].Ticket = strings.Repeat("a", 64) })
	runtime.Store(true)
	app := sharedPageApp(t, unknown)
	w := sharedPageRead(t, app, "/", true, nil)
	if w.Code != 200 || sharedPageEnvelope(t, w).HTML != "runtime" || w.Header().Get("X-Nextgo-Prerender") != "runtime" {
		t.Fatal(w.Code, w.Header())
	}
	valid := sharedPageApp(t, c)
	w = sharedPageRead(t, valid, "/", true, nil)
	if w.Code != 200 || sharedPageEnvelope(t, w).HTML != "build" {
		t.Fatal("unknown token consumed authentic permission", w.Code, w.Body.String())
	}
}

type sharedPrerenderBackendOnly struct{ shared.Backend }

func TestSharedPrerenderMissingCapabilityFailsBeforeBuildLoaders(t *testing.T) {
	memory, _ := shared.NewMemory(8, 1<<20)
	store := sharedPageStore(t, sharedPrerenderBackendOnly{memory}, "unsupported-prerender", "snapshot-v1")
	var loads atomic.Int32
	r := sharedPrerenderRoute(func(*Context) (Page, error) { loads.Add(1); return Page{Body: ui.Text("build")}, nil })
	r.Pattern = "/:slug"
	r.StaticParams = func(context.Context) ([]Params, error) { loads.Add(1); return []Params{{"slug": "go"}}, nil }
	app := sharedPageApp(t, Config{BuildID: "source", PrerenderBuildMode: true, SharedCache: store, Routes: []Route{r}})
	if _, err := app.BuildPrerender(context.Background()); !errors.Is(err, shared.ErrProtocol) || loads.Load() != 0 {
		t.Fatal("unsupported capability ran build loaders", err, loads.Load())
	}
}

type sharedPrerenderBlockingBackend struct {
	*shared.Memory
	block   atomic.Bool
	consume atomic.Bool
	entered chan struct{}
}

func (b *sharedPrerenderBlockingBackend) BeginSeeds(ctx context.Context, ns, token string) (shared.SeedSession, error) {
	if b.block.Load() {
		if b.consume.Load() {
			if _, err := b.Memory.BeginSeeds(ctx, ns, token); err != nil {
				return shared.SeedSession{}, err
			}
		}
		select {
		case b.entered <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return shared.SeedSession{}, ctx.Err()
	}
	return b.Memory.BeginSeeds(ctx, ns, token)
}
func TestSharedPrerenderStartupCancellationBoundsImportButNotRequestLifetime(t *testing.T) {
	for _, localized := range []bool{false, true} {
		t.Run(fmt.Sprint(localized), func(t *testing.T) {
			memory, _ := shared.NewMemory(16, 1<<20)
			backend := &sharedPrerenderBlockingBackend{Memory: memory, entered: make(chan struct{}, 1)}
			store := sharedPageStore(t, backend, "cancel-prerender", "snapshot-v1")
			config := Config{SharedCache: store, Routes: []Route{sharedPrerenderRoute(static("build"))}}
			if localized {
				config.Locales = &LocaleConfig{Codes: []string{"en", "fr"}, Default: "en", PrefixDefault: true}
			}
			c, _ := buildSharedPrerenderTest(t, config)
			backend.block.Store(true)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				app, err := NewContext(ctx, c)
				if app != nil {
					_ = app.Close(context.Background())
				}
				done <- err
			}()
			select {
			case <-backend.entered:
			case <-time.After(2 * time.Second):
				t.Fatal("constructor did not reach shared import")
			}
			cancel()
			select {
			case err := <-done:
				if !errors.Is(err, context.Canceled) {
					t.Fatal("startup did not return cancellation", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("shared bootstrap ignored startup cancellation")
			}
			// Cancellation happened before consumption, so the authentic artifact may
			// still initialize once. Canceling a successful startup context afterward
			// must not cancel independent HTTP request work.
			backend.block.Store(false)
			startup, stop := context.WithCancel(context.Background())
			app, err := NewContext(startup, c)
			if err != nil {
				stop()
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = app.Close(context.Background()) })
			stop()
			target := "/"
			if localized {
				target = "/fr"
			}
			w := sharedPageRead(t, app, target, true, nil)
			if w.Code != 200 || sharedPageEnvelope(t, w).HTML != "build" || w.Header().Get("X-Nextgo-Prerender") != "build" {
				t.Fatal("startup context became request lifetime", w.Code, w.Header())
			}
		})
	}
}

func TestSharedPrerenderAlreadyCanceledStartupNeverEnumerates(t *testing.T) {
	_, store := sharedPrerenderMemory(t)
	var calls atomic.Int32
	r := sharedPrerenderRoute(static("public"))
	r.Pattern = "/:slug"
	r.StaticParams = func(context.Context) ([]Params, error) { calls.Add(1); return []Params{{"slug": "go"}}, nil }
	c, _ := buildSharedPrerenderTest(t, Config{SharedCache: store, Routes: []Route{r}})
	calls.Store(0)
	c.BasePath = "/different" // A successful startup would enumerate fallback membership.
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if app, err := NewContext(canceled, c); !errors.Is(err, context.Canceled) || app != nil || calls.Load() != 0 {
		t.Fatal(app, err, calls.Load())
	}
}

func TestSharedPrerenderCanceledStartupAfterConsumptionNeverReimports(t *testing.T) {
	memory, _ := shared.NewMemory(8, 1<<20)
	backend := &sharedPrerenderBlockingBackend{Memory: memory, entered: make(chan struct{}, 1)}
	store := sharedPageStore(t, backend, "unknown-startup-prerender", "snapshot-v1")
	var loads atomic.Int32
	route := sharedPrerenderRoute(func(*Context) (Page, error) { return Page{Body: ui.Text(fmt.Sprint(loads.Add(1)))}, nil })
	c, _ := buildSharedPrerenderTest(t, Config{SharedCache: store, Routes: []Route{route}})
	backend.block.Store(true)
	backend.consume.Store(true)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		app, err := NewContext(ctx, c)
		if app != nil {
			_ = app.Close(context.Background())
		}
		done <- err
	}()
	select {
	case <-backend.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("constructor did not consume ticket")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("constructor ignored cancellation")
	}
	backend.block.Store(false)
	for i := 0; i < 2; i++ {
		app := sharedPageApp(t, c)
		w := sharedPageRead(t, app, "/", true, nil)
		if w.Code != 200 || sharedPageEnvelope(t, w).HTML != "2" || w.Header().Get("X-Nextgo-Prerender") != "runtime" || loads.Load() != 2 {
			t.Fatal("unknown consume outcome replayed build", w.Code, w.Header(), loads.Load())
		}
	}
}
