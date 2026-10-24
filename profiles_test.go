package nextgo

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/internal/postresponse"
)

func TestProfileWindowIsBoundedByGenerationAndHardExpiry(t *testing.T) {
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	store := cache.New(8, cache.WithClock(func() time.Time { return time.Unix(0, clock.Load()) }))
	app := testApp(t, Config{Cache: store, CacheProfiles: map[string]cache.Profile{"demo": {Stale: 10 * time.Second, Revalidate: time.Second, Expire: 5 * time.Second}}, Routes: []Route{{Pattern: "/", Cache: &CachePolicy{Profile: "demo"}, Page: static("snapshot")}}})
	read := func() (*httptest.ResponseRecorder, *postresponse.Collector) {
		ctx, done := postresponse.Collect(context.Background())
		w := httptest.NewRecorder()
		r := httptest.NewRequest("GET", "/", nil).WithContext(ctx)
		r.Header.Set("X-Nextgo-Navigate", "1")
		app.ServeHTTP(w, r)
		return w, done
	}
	for i := 0; i < 2; i++ {
		response, done := read()
		budget, err := strconv.ParseInt(response.Header().Get(navigation.StaleHeader), 10, 64)
		maxBudget := int64(5000 - i*4000)
		if err != nil || budget <= 0 || budget > maxBudget || response.Header().Get("Cache-Control") != "public, max-age=0, must-revalidate" || response.Header().Get("X-Nextgo-Prefetch") != "public" || response.Header().Get("Age") != "" {
			t.Fatal("profile window escaped generation lifetime", response.Header(), maxBudget)
		}
		done.Finish(context.Background(), false)
		clock.Add(int64(4 * time.Second))
	}
}

func TestProfileBrowserAgeDoesNotResetWhenOldSnapshotIsReused(t *testing.T) {
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	app := testApp(t, Config{Cache: cache.New(8, cache.WithClock(func() time.Time { return time.Unix(0, clock.Load()) })), CacheProfiles: map[string]cache.Profile{"demo": {Stale: time.Second, Revalidate: 10 * time.Second, Expire: time.Minute}}, Routes: []Route{{Pattern: "/", Cache: &CachePolicy{Profile: "demo"}, Page: static("same")}}})
	_ = request(app, "/", true)
	clock.Add(int64(2 * time.Second))
	response := request(app, "/", true)
	if response.Header().Get(navigation.StaleHeader) != "" || response.Header().Get("X-Nextgo-Prefetch") != "" || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("old snapshot received a new browser lifetime", response.Header())
	}
}

func TestProfileBypassesNeverAdvertiseBrowserReuse(t *testing.T) {
	for _, kind := range []string{"cookie", "authorization", "body", "set-cookie", "development"} {
		for _, profile := range []string{"seconds", "default"} {
			t.Run(kind+"/"+profile, func(t *testing.T) {
				cfg := Config{Dev: kind == "development", Headers: http.Header{navigation.StaleHeader: {"9999"}, "X-Nextgo-Prefetch": {"public"}}, Routes: []Route{{Pattern: "/", Cache: &CachePolicy{Profile: profile}, Page: static("body")}}}
				if kind == "set-cookie" {
					cfg.Middleware = []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
						return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
							http.SetCookie(w, &http.Cookie{Name: "session", Value: "private"})
							next.ServeHTTP(w, r)
						})
					}}
				}
				app := testApp(t, cfg)
				r := httptest.NewRequest("GET", "/", nil)
				if kind == "body" {
					r = httptest.NewRequest("GET", "/", strings.NewReader("private"))
				}
				if kind == "cookie" {
					r.Header.Set("Cookie", "session=private")
				}
				if kind == "authorization" {
					r.Header.Set("Authorization", "Bearer private")
				}
				w := httptest.NewRecorder()
				app.ServeHTTP(w, r)
				if !strings.Contains(w.Header().Get("Cache-Control"), "no-store") || w.Header().Get(navigation.StaleHeader) != "" || w.Header().Get("X-Nextgo-Prefetch") != "" || app.config.Cache.Len() != 0 {
					t.Fatal("bypassed profile advertised reuse", w.Header())
				}
			})
		}
	}
}

func TestEffectiveCachePolicyIdentityIsStableAndNamespacesSnapshots(t *testing.T) {
	store := cache.New(8)
	makeApp := func(stale time.Duration, tags []string, body string) *App {
		return testApp(t, Config{BuildID: "same-build", Cache: store, CacheProfiles: map[string]cache.Profile{"demo": {Stale: stale, Revalidate: time.Minute, Expire: time.Hour}}, Routes: []Route{{Pattern: "/", Page: static(body), Cache: &CachePolicy{Profile: "demo", Tags: tags}}}})
	}
	first := makeApp(time.Second, []string{"b", "a"}, "first")
	_, before := runtimeEnvelope(t, first, "/", nil)
	same := makeApp(time.Second, []string{"a", "b", "a"}, "first")
	if first.configurationID != same.configurationID {
		t.Fatal("equivalent tag order changed cache ownership")
	}
	if makeApp(time.Second, nil, "same").configurationID != makeApp(time.Second, []string{}, "same").configurationID {
		t.Fatal("empty label representation changed cache ownership")
	}
	changed := makeApp(2*time.Second, []string{"a", "b"}, "second")
	_, after := runtimeEnvelope(t, changed, "/", nil)
	if first.configurationID == changed.configurationID || before.HTML != "first" || after.HTML != "second" {
		t.Fatal("changed profile shared a snapshot", before, after)
	}
	var manifest navigation.Manifest
	if err := json.Unmarshal([]byte(first.navigationJSON), &manifest); err != nil || !navigation.ValidConfigurationID(manifest.Cache) {
		t.Fatal(manifest, err)
	}
}

func TestProfileConfigurationValidationAndOwnership(t *testing.T) {
	for _, cfg := range []Config{
		{Routes: []Route{{Pattern: "/", Page: static("page"), Cache: &CachePolicy{Profile: "unknown"}}}},
		{Routes: []Route{{Pattern: "/", Page: static("page"), Cache: &CachePolicy{Profile: "minutes", TTL: time.Second}}}},
		{Routes: []Route{{Pattern: "/", Page: static("page"), Cache: &CachePolicy{Profile: "minutes", Lifetime: &cache.Lifetime{Expire: time.Minute}}}}},
		{CacheProfiles: map[string]cache.Profile{"bad name": {Expire: time.Minute}}},
		{CacheProfiles: map[string]cache.Profile{"bad": {Stale: 25 * time.Hour, Expire: time.Minute}}},
	} {
		app, err := New(cfg)
		if err == nil {
			_ = app.Close(context.Background())
			t.Fatal("invalid profile accepted", cfg)
		}
	}
	profiles := map[string]cache.Profile{"demo": {Stale: time.Second, Revalidate: time.Minute, Expire: time.Hour}}
	policy := &CachePolicy{Profile: "demo", Tags: []string{"original"}}
	app := testApp(t, Config{CacheProfiles: profiles, Routes: []Route{{Pattern: "/", Page: static("owned"), Cache: policy}}})
	identity := app.configurationID
	policyIdentity := app.cachePolicyIdentity()
	profiles["demo"] = cache.Profile{}
	policy.Profile = "unknown"
	policy.Tags[0] = "changed"
	if w := request(app, "/", true); w.Code != 200 || w.Header().Get(navigation.StaleHeader) == "" || app.cachePolicyIdentity() != policyIdentity || identity != app.configurationID {
		t.Fatal("caller mutation changed frozen policy", w.Code, w.Header())
	}
	if int64(cache.MaxClientStale/time.Millisecond) != navigation.MaxStaleMillis {
		t.Fatal("server/browser stale bounds differ")
	}
}

func TestLegacyHTTPAgeCannotExtendPastHardExpiry(t *testing.T) {
	var clock atomic.Int64
	clock.Store(time.Now().UnixNano())
	app := testApp(t, Config{Cache: cache.New(4, cache.WithClock(func() time.Time { return time.Unix(0, clock.Load()) })), Routes: []Route{{Pattern: "/", Cache: &CachePolicy{TTL: time.Second}, Page: static("body")}}})
	_ = request(app, "/", true)
	clock.Add(int64(500 * time.Millisecond))
	response := request(app, "/", true)
	if response.Header().Get("Cache-Control") != "public, max-age=1" || response.Header().Get("Age") != "1" {
		t.Fatal("fractional age extended the public window", response.Header())
	}
}

func TestConfiguredProfileLookupInRequestsAfterAndStaticEnumeration(t *testing.T) {
	if _, err := LookupCacheProfile(context.Background(), "minutes"); !errors.Is(err, ErrCacheProfileScope) {
		t.Fatal(err)
	}
	observed := make(chan cache.Profile, 1)
	profile := cache.Profile{Stale: 2 * time.Second, Revalidate: 3 * time.Second, Expire: 4 * time.Second}
	app := testApp(t, Config{CacheProfiles: map[string]cache.Profile{"minutes": profile}, Routes: []Route{
		{Pattern: "/:slug", Page: static("page"), StaticParams: func(ctx context.Context) ([]Params, error) {
			found, err := LookupCacheProfile(ctx, "minutes")
			if err != nil || found != profile {
				t.Error(found, err)
			}
			return []Params{{"slug": "one"}}, err
		}},
		{Pattern: "/api/profile", API: func(w http.ResponseWriter, r *http.Request) {
			found, err := LookupCacheProfile(r.Context(), "minutes")
			if err != nil || found != profile {
				t.Error(found, err)
			}
			if _, err := LookupCacheProfile(r.Context(), "unknown"); err == nil {
				t.Error("unknown profile resolved")
			}
			if err := After(r.Context(), func(work context.Context) error {
				found, err := LookupCacheProfile(work, "minutes")
				if err == nil {
					observed <- found
				}
				return err
			}); err != nil {
				t.Error(err)
			}
			w.WriteHeader(204)
		}},
	}})
	if paths, err := app.StaticPaths(context.Background()); err != nil || len(paths) != 1 || paths[0] != "/one" {
		t.Fatal(paths, err)
	}
	if response := request(app, "/api/profile", false); response.Code != 204 {
		t.Fatal(response.Code)
	}
	select {
	case found := <-observed:
		if found != profile {
			t.Fatal(found)
		}
	case <-time.After(time.Second):
		t.Fatal("completion work lost its profile registry")
	}
}
