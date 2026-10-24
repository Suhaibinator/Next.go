package nextgo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/ui"
)

func profileAge(d time.Duration) *time.Duration { return &d }

func TestEquivalentPartialNamedAndInlinePoliciesShareSnapshotIdentity(t *testing.T) {
	store := cache.New(16)
	var calls atomic.Int64
	profile := cache.Profile{Stale: 3 * time.Second, Revalidate: 5 * time.Second, Expire: time.Minute}
	page := func(*Context) (Page, error) { return Page{Body: ui.Text(fmt.Sprint(calls.Add(1)))}, nil }
	definitions := []Config{
		{CacheProfiles: map[string]cache.Profile{"named": profile}, Routes: []Route{{Pattern: "/", Page: page, Cache: &CachePolicy{Profile: "named"}}}},
		{CacheProfiles: map[string]cache.Profile{"default": profile}, CacheProfileSpecs: map[string]cache.ProfileSpec{"alias": {}}, Routes: []Route{{Pattern: "/", Page: page, Cache: &CachePolicy{Profile: "alias"}}}},
		{CacheProfileSpecs: map[string]cache.ProfileSpec{"default": {Stale: &profile.Stale, Revalidate: &profile.Revalidate, Expire: &profile.Expire}, "alias": {}}, Routes: []Route{{Pattern: "/", Page: page, Cache: &CachePolicy{Profile: "alias"}}}},
		{CacheProfiles: map[string]cache.Profile{"default": profile}, Routes: []Route{{Pattern: "/", Page: page, Cache: &CachePolicy{ProfileSpec: &cache.ProfileSpec{}}}}},
		{Routes: []Route{{Pattern: "/", Page: page, Cache: &CachePolicy{ProfileSpec: &cache.ProfileSpec{Stale: &profile.Stale, Revalidate: &profile.Revalidate, Expire: &profile.Expire}}}}},
	}
	var identity string
	for i, cfg := range definitions {
		cfg.Cache = store
		cfg.BuildID = "shared-build"
		app := testApp(t, cfg)
		if i == 0 {
			identity = app.configurationID
		} else if app.configurationID != identity {
			t.Fatal("equivalent resolved policy changed identity", i, app.configurationID, identity)
		}
		_, e := runtimeEnvelope(t, app, "/", nil)
		if e.HTML != "1" || calls.Load() != 1 {
			t.Fatal("equivalent policies failed to share snapshot", i, e, calls.Load())
		}
	}
	changed := profile
	changed.Stale += time.Second
	app := testApp(t, Config{Cache: store, BuildID: "shared-build", CacheProfileSpecs: map[string]cache.ProfileSpec{"default": {Stale: &changed.Stale, Revalidate: &changed.Revalidate, Expire: &changed.Expire}, "alias": {}}, Routes: []Route{{Pattern: "/", Page: page, Cache: &CachePolicy{Profile: "alias"}}}})
	_, e := runtimeEnvelope(t, app, "/", nil)
	if app.configurationID == identity || e.HTML != "2" || calls.Load() != 2 {
		t.Fatal("changed inherited policy reused old namespace", e, calls.Load())
	}
}

func TestPartialProfilesAndInlinePoliciesOwnAllCallerValues(t *testing.T) {
	stale, fresh, hard := 3*time.Second, 5*time.Second, time.Minute
	want := cache.Profile{Stale: stale, Revalidate: fresh, Expire: hard}
	specs := map[string]cache.ProfileSpec{"default": {Stale: &stale, Revalidate: &fresh, Expire: &hard}, "alias": {}}
	inline := &cache.ProfileSpec{}
	policy := &CachePolicy{ProfileSpec: inline}
	app := testApp(t, Config{CacheProfileSpecs: specs, Routes: []Route{
		{Pattern: "/", Cache: policy, Page: static("snapshot")},
		{Pattern: "/profile", API: func(w http.ResponseWriter, r *http.Request) {
			p, err := LookupCacheProfile(r.Context(), "alias")
			if err != nil {
				t.Error(err)
			}
			_ = json.NewEncoder(w).Encode(p)
		}},
	}})
	identity := app.configurationID
	stale, fresh, hard = 0, 0, 0
	specs["default"] = cache.ProfileSpec{Expire: &hard}
	delete(specs, "alias")
	inline.Expire = &hard
	policy.ProfileSpec = &cache.ProfileSpec{Expire: &hard}
	response := request(app, "/profile", false)
	var found cache.Profile
	if err := json.Unmarshal(response.Body.Bytes(), &found); err != nil || found != want {
		t.Fatal("registry retained caller pointers", found, err)
	}
	response = request(app, "/", true)
	window, err := strconv.ParseInt(response.Header().Get(navigation.StaleHeader), 10, 64)
	if err != nil || window <= 0 || window > 3000 || app.configurationID != identity {
		t.Fatal("inline policy retained caller pointers", response.Header())
	}
	var life *cache.Lifetime
	var staleWindow time.Duration
	for _, route := range app.routes {
		if route.Pattern == "/" {
			life, staleWindow = app.cacheLifetime(route.Cache)
		}
	}
	if life == nil || *life != want.Lifetime() || staleWindow != want.Stale {
		t.Fatal("frozen inline ages changed", life, staleWindow)
	}
}

func TestInlineProfileExplicitZeroDisablesBrowserReuseOnly(t *testing.T) {
	var calls atomic.Int64
	app := testApp(t, Config{Routes: []Route{{Pattern: "/", Cache: &CachePolicy{ProfileSpec: &cache.ProfileSpec{Stale: profileAge(0)}}, Page: func(*Context) (Page, error) { return Page{Body: ui.Text(fmt.Sprint(calls.Add(1)))}, nil }}}})
	for i := 0; i < 2; i++ {
		response := request(app, "/", true)
		if response.Code != 200 || response.Header().Get("Cache-Control") != "no-store" || response.Header().Get(navigation.StaleHeader) != "" || response.Header().Get("X-Nextgo-Prefetch") != "" {
			t.Fatal(response.Code, response.Header())
		}
	}
	if calls.Load() != 1 || app.config.Cache.Len() != 1 {
		t.Fatal("zero browser window disabled server cache", calls.Load())
	}
	for _, policy := range []*CachePolicy{nil, {}} {
		calls.Store(0)
		app := testApp(t, Config{Routes: []Route{{Pattern: "/", Cache: policy, Page: func(*Context) (Page, error) { return Page{Body: ui.Text(fmt.Sprint(calls.Add(1)))}, nil }}}})
		_ = request(app, "/", true)
		_ = request(app, "/", true)
		if calls.Load() != 2 || app.config.Cache.Len() != 0 {
			t.Fatal("implicit caching introduced", policy, calls.Load())
		}
	}
}

func TestPartialAndInlineProfileConfigurationRejectsAmbiguity(t *testing.T) {
	for _, policy := range []*CachePolicy{
		{TTL: time.Second, ProfileSpec: &cache.ProfileSpec{}},
		{Lifetime: &cache.Lifetime{Expire: time.Minute}, ProfileSpec: &cache.ProfileSpec{}},
		{Profile: "default", ProfileSpec: &cache.ProfileSpec{}},
		{ProfileSpec: &cache.ProfileSpec{Expire: profileAge(0)}},
		{ProfileSpec: &cache.ProfileSpec{Expire: profileAge(time.Minute)}}, // Default revalidate is 15m.
	} {
		app, err := New(Config{Routes: []Route{{Pattern: "/", Page: static("body"), Cache: policy}}})
		if err == nil {
			_ = app.Close(context.Background())
			t.Fatal("invalid inline policy accepted", policy)
		}
	}
}
