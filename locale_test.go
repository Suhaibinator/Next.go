package nextgo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/actions"
	"github.com/Suhaibinator/Next.go/seo"
	"github.com/Suhaibinator/Next.go/ui"
)

func localeConfig(prefix bool) *LocaleConfig {
	return &LocaleConfig{Codes: []string{"en", "fr", "ar"}, Default: "en", PrefixDefault: prefix, Detect: true, Directions: map[string]string{"ar": "rtl"}}
}
func localizedPage(c *Context) (Page, error) {
	return Page{Title: "Locale " + CurrentLocale(c), Body: ui.El("a", ui.Attrs{"href": MustURL(c, "/posts/a?q=1#entry")}, ui.Text(CurrentLocale(c)+"|"+c.Params["slug"]+"|"+c.Query.Get("q")))}, nil
}
func localizedRequest(app *App, path, host, lang, cookie string, nav bool) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", path, nil)
	if host != "" {
		r.Host = host
	}
	if lang != "" {
		r.Header.Set("Accept-Language", lang)
	}
	if cookie != "" {
		r.Header.Set("Cookie", cookie)
	}
	if nav {
		r.Header.Set("X-Nextgo-Navigate", "1")
	}
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	return w
}
func TestLocalePrefixNegotiationAndDocuments(t *testing.T) {
	app := testApp(t, Config{BasePath: "/shop", Locales: localeConfig(true), Routes: []Route{{Pattern: "/", Page: localizedPage}, {Pattern: "/posts/:slug", Page: localizedPage}}, NotFound: static("missing")})
	for _, tc := range []struct{ path, lang, cookie, want string }{{"/shop/posts/a?q=x%20y", "fr-CA, en;q=0.5", "", "/shop/fr/posts/a?q=x%20y"}, {"/shop/", "ar", "nextgo_locale=en", "/shop/en"}, {"/shop/posts/a", "fr;q=0,en;q=.9", "", "/shop/en/posts/a"}, {"/shop/", "fr;q=1e0,en;q=0.5", "", "/shop/en"}} {
		w := localizedRequest(app, tc.path, "", tc.lang, tc.cookie, false)
		if w.Code != 307 || w.Header().Get("Location") != tc.want || w.Header().Get("Cache-Control") != "private, no-store" || !strings.Contains(strings.Join(w.Header().Values("Vary"), ","), "Accept-Language") {
			t.Fatal(tc, w.Code, w.Header())
		}
	}
	for _, code := range []string{"en", "fr", "ar"} {
		w := localizedRequest(app, "/shop/"+code+"/posts/caf%C3%A9?q=recent", "", "fr", "", false)
		direction := "ltr"
		if code == "ar" {
			direction = "rtl"
		}
		if w.Code != 200 || w.Header().Get("Content-Language") != code || !strings.Contains(w.Body.String(), `<html lang="`+code+`" dir="`+direction+`">`) || !strings.Contains(w.Body.String(), code+"|café|recent") || !strings.Contains(w.Body.String(), `href="/shop/`+code+`/posts/a?q=1#entry"`) {
			t.Fatal(code, w.Code, w.Body.String())
		}
		w = localizedRequest(app, "/shop/"+code+"/posts/a?q=recent", "", "", "", true)
		var e Envelope
		if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || e.Document.Language != code || e.Document.Direction != direction || e.URL != "/shop/"+code+"/posts/a?q=recent" {
			t.Fatal(code, e, err)
		}
	}
	w := localizedRequest(app, "/shop/FR/posts/a?q=1", "", "", "", false)
	if w.Code != 308 || w.Header().Get("Location") != "/shop/fr/posts/a?q=1" {
		t.Fatal(w.Code, w.Header())
	}
	w = localizedRequest(app, "/shop/ar/missing", "", "", "", false)
	if w.Code != 404 || !strings.Contains(w.Body.String(), `lang="ar" dir="rtl"`) {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestLocaleDomainsAndTrustedSwitchURLs(t *testing.T) {
	lc := localeConfig(false)
	lc.Domains = []LocaleDomain{{Host: "EN.Example", Default: "en"}, {Host: "fr.example", Default: "fr", HTTP: true}}
	app := testApp(t, Config{Locales: lc, Routes: []Route{{Pattern: "/", Page: localizedPage}, {Pattern: "/links", Page: func(c *Context) (Page, error) {
		u, err := LocaleURL(c, "fr", "/posts/caf%C3%A9?q=x%20y#part")
		return Page{Body: ui.Text(u)}, err
	}}}})
	for _, tc := range []struct{ host, path, code string }{{"en.example", "/", "en"}, {"fr.example", "/", "fr"}, {"fr.example", "/en/", "en"}} {
		w := localizedRequest(app, tc.path, tc.host, "", "", false)
		// Non-trailing mode canonicalizes explicit prefix roots.
		if w.Code == 308 {
			w = localizedRequest(app, w.Header().Get("Location"), tc.host, "", "", false)
		}
		if w.Code != 200 || w.Header().Get("Content-Language") != tc.code {
			t.Fatal(tc, w.Code, w.Header())
		}
	}
	w := localizedRequest(app, "/links", "en.example", "", "", false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "http://fr.example/posts/caf%C3%A9?q=x%20y#part") {
		t.Fatal(w.Code, w.Body.String())
	}
	w = localizedRequest(app, "/", "en.example", "fr", "", false)
	if w.Code != 307 || w.Header().Get("Location") != "http://fr.example/fr" {
		t.Fatal("unsafe/loop-prone domain redirect", w.Code, w.Header())
	}
	// Explicit destination prefix overrides a conflicting destination cookie.
	w = localizedRequest(app, "/fr", "fr.example", "en", "nextgo_locale=en", false)
	if w.Code != 200 || w.Header().Get("Content-Language") != "fr" {
		t.Fatal("domain redirect bounced", w.Code, w.Header())
	}
	w = localizedRequest(app, "/", "evil.example", "", "", false)
	if w.Code != 421 || strings.Contains(w.Header().Get("Location"), "evil") {
		t.Fatal(w.Code, w.Header())
	}
}

func TestLocaleEndpointActionAndMetadataContext(t *testing.T) {
	router := actions.New(actions.Options{})
	router.Handle("save", func(c *actions.Context) (actions.Result, error) {
		return actions.Result{Data: CurrentLocale(c.Context), Redirect: "/posts/a"}, nil
	})
	app := testApp(t, Config{Locales: localeConfig(true), Actions: router, Routes: []Route{
		{Pattern: "/", Page: localizedPage},
		{Pattern: "/api/info", API: func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, CurrentLocale(r.Context())) }},
		{Pattern: "/explicit", Page: func(*Context) (Page, error) {
			return Page{Metadata: seo.Metadata{Language: "de", Direction: "ltr"}, Body: ui.Text("explicit")}, nil
		}},
		{Pattern: "/bad", Page: func(*Context) (Page, error) { return Page{Meta: map[string]string{"viewport": "bad"}}, nil }},
	}})
	for _, path := range []string{"/api/info", "/fr/api/info"} {
		w := localizedRequest(app, path, "", "ar", "", false)
		want := "en"
		if strings.Contains(path, "/fr/") {
			want = "fr"
		}
		if w.Code != 200 || w.Body.String() != want {
			t.Fatal(path, w.Code, w.Body.String())
		}
	}
	r := httptest.NewRequest("POST", "http://example.com/fr/_nextgo/actions/save", strings.NewReader("{}"))
	r.Header.Set("Origin", "http://example.com")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	app.ServeHTTP(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"data":"fr"`) || !strings.Contains(w.Body.String(), `"redirect":"/fr/posts/a"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = localizedRequest(app, "/fr/explicit", "", "", "", false)
	if w.Code != 200 || w.Header().Get("Content-Language") != "de" || !strings.Contains(w.Body.String(), `lang="de"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	w = localizedRequest(app, "/ar/bad", "", "", "", false)
	if w.Code != 500 || !strings.Contains(w.Body.String(), `lang="ar" dir="rtl"`) {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestLocaleEnumerationContextAndBounds(t *testing.T) {
	app := testApp(t, Config{BasePath: "/shop", Locales: localeConfig(false), Routes: []Route{{Pattern: "/", Page: localizedPage}, {Pattern: "/posts/:slug", Page: localizedPage, StaticParams: func(ctx context.Context) ([]Params, error) { return []Params{{"slug": CurrentLocale(ctx)}}, nil }}}})
	got, err := app.StaticPaths(context.Background())
	want := []string{"/", "/ar", "/ar/posts/ar", "/fr", "/fr/posts/fr", "/posts/en"}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatal(got, err)
	}
	values := make([]Params, 4000)
	for i := range values {
		values[i] = Params{"slug": "same"}
	}
	app = testApp(t, Config{Locales: localeConfig(true), Routes: []Route{{Pattern: "/:slug", Page: localizedPage, StaticParams: func(context.Context) ([]Params, error) { return values, nil }}}})
	if _, err := app.StaticPaths(context.Background()); err == nil {
		t.Fatal("per-locale enumeration multiplied the input budget")
	}
}

func TestLocaleConfigurationOwnershipAndErrors(t *testing.T) {
	codes := []string{"en", "fr"}
	directions := map[string]string{"fr": "ltr"}
	lc := &LocaleConfig{Codes: codes, Default: "en", Directions: directions}
	app := testApp(t, Config{Locales: lc, Routes: []Route{{Pattern: "/", Page: localizedPage}}})
	codes[1] = "ar"
	directions["fr"] = "rtl"
	lc.Default = "ar"
	w := localizedRequest(app, "/fr", "", "", "", false)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `lang="fr" dir="ltr"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	for _, cfg := range []Config{
		{Locales: &LocaleConfig{Codes: []string{"en", "EN"}, Default: "en"}},
		{Locales: &LocaleConfig{Codes: []string{"en"}, Default: "fr"}},
		{Locales: &LocaleConfig{Codes: []string{"en"}, Default: "en", Domains: []LocaleDomain{{Host: "evil.example/path", Default: "en"}}}},
		{Locales: &LocaleConfig{Codes: []string{"en"}, Default: "en", Directions: map[string]string{"en": "bad"}}},
		{Locales: localeConfig(true), Routes: []Route{{Pattern: "/fr/page", Page: localizedPage}}},
	} {
		a, err := New(cfg)
		if err == nil {
			_ = a.Close(context.Background())
			t.Fatal("invalid locale configuration accepted", cfg)
		}
	}
	if CurrentLocale(context.Background()) != "" || len(AvailableLocales(context.Background())) != 0 {
		t.Fatal("locale escaped scope")
	}
	if _, err := LocaleURL(context.Background(), "fr", "/"); !errors.Is(err, ErrLocaleScope) {
		t.Fatal(err)
	}
}

func TestLocaleCompletionWorkHasOneGlobalCapacity(t *testing.T) {
	release, started := make(chan struct{}), make(chan struct{}, 1)
	defer close(release)
	app := testApp(t, Config{Locales: localeConfig(true), After: AfterOptions{Concurrency: 1, MaxPending: 2, Timeout: 5 * time.Second}, Routes: []Route{{Pattern: "/after", API: func(w http.ResponseWriter, r *http.Request) {
		err := After(r.Context(), func(ctx context.Context) error {
			select {
			case started <- struct{}{}:
			default:
			}
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		if err != nil {
			w.WriteHeader(503)
			return
		}
		w.WriteHeader(202)
	}}}})
	first := localizedRequest(app, "/en/after", "", "", "", false)
	if first.Code != 202 {
		t.Fatal(first.Code)
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("work did not start")
	}
	second := localizedRequest(app, "/fr/after", "", "", "", false)
	if second.Code != 202 {
		t.Fatal(second.Code)
	}
	third := localizedRequest(app, "/ar/after", "", "", "", false)
	if third.Code != 503 {
		t.Fatal("locale mounts multiplied completion capacity", third.Code)
	}
}

func TestLocaleSnapshotsAndCompletionContextsAreIsolated(t *testing.T) {
	var revision atomic.Int64
	revision.Store(1)
	seen := make(chan string, 1)
	app := testApp(t, Config{Locales: localeConfig(true), Routes: []Route{
		{Pattern: "/", Cache: &CachePolicy{Profile: "default", Tags: []string{"localized"}}, Page: func(c *Context) (Page, error) {
			value, err := Memo(c.Context, "data", func(ctx context.Context) (string, error) {
				return fmt.Sprintf("%s:%d", CurrentLocale(ctx), revision.Load()), nil
			})
			return Page{Body: ui.Text(value)}, err
		}},
		{Pattern: "/after", API: func(w http.ResponseWriter, r *http.Request) {
			err := After(r.Context(), func(ctx context.Context) error {
				seen <- CurrentLocale(ctx) + MustLocaleURL(ctx, "ar", "/")
				return nil
			})
			if err != nil {
				t.Error(err)
			}
		}},
	}})
	ids := map[string]bool{}
	for _, code := range []string{"en", "fr", "ar"} {
		w := localizedRequest(app, "/"+code, "", "", "", true)
		var e Envelope
		if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil || e.HTML != code+":1" || ids[e.Configuration] {
			t.Fatal(code, e, err)
		}
		ids[e.Configuration] = true
	}
	if app.config.Cache.Len() != 3 {
		t.Fatal("locale snapshots shared keys", app.config.Cache.Len())
	}
	revision.Store(2)
	app.RevalidatePath("/")
	w := localizedRequest(app, "/fr", "", "", "", true)
	var e Envelope
	_ = json.Unmarshal(w.Body.Bytes(), &e)
	if e.HTML != "fr:2" {
		t.Fatal("parent invalidation missed locale snapshot", e)
	}
	_ = localizedRequest(app, "/fr/after", "", "", "", false)
	select {
	case value := <-seen:
		if value != "fr/ar" {
			t.Fatal(value)
		}
	case <-time.After(time.Second):
		t.Fatal("completion lost locale context")
	}
}

func TestLocalePreferenceAndExportContracts(t *testing.T) {
	app := testApp(t, Config{BasePath: "/shop", Locales: localeConfig(true), Routes: []Route{{Pattern: "/cookie", API: func(w http.ResponseWriter, r *http.Request) {
		available := AvailableLocales(r.Context())
		available[0] = "changed"
		cookie, err := LocaleCookie(r.Context(), "FR", true)
		if err != nil {
			t.Error(err)
			return
		}
		http.SetCookie(w, cookie)
		if _, err := LocaleCookie(r.Context(), "unknown", true); err == nil {
			t.Error("unknown preference accepted")
		}
		if _, err := LocaleURL(r.Context(), "fr", "//untrusted.example"); err == nil {
			t.Error("unsafe URL accepted")
		}
		fmt.Fprint(w, CurrentLocale(r.Context())+AvailableLocales(r.Context())[0])
	}}}})
	w := localizedRequest(app, "/shop/fr/cookie", "", "", "", false)
	cookies := w.Result().Cookies()
	if w.Code != 200 || w.Body.String() != "fren" || len(cookies) != 1 || cookies[0].Value != "fr" || cookies[0].Path != "/shop" || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteLaxMode {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
	if err := app.CheckStaticExport(); err == nil {
		t.Fatal("locale export silently accepted")
	}
	plain := testApp(t, Config{})
	if err := plain.CheckStaticExport(); err != nil {
		t.Fatal(err)
	}
}

func TestLocaleDomainDefaultPortsFollowConfiguredSchemeAndRejectOverlaps(t *testing.T) {
	for _, plainHTTP := range []bool{false, true} {
		port := "443"
		scheme := "https"
		wrongPort := "80"
		if plainHTTP {
			port, wrongPort, scheme = "80", "443", "http"
		}
		locales := localeConfig(false)
		locales.Detect = false
		locales.Domains = []LocaleDomain{{Host: "FR.Example.:" + port, Default: "fr", HTTP: plainHTTP}}
		app := testApp(t, Config{Locales: locales, Routes: []Route{{Pattern: "/", Page: func(c *Context) (Page, error) { return Page{Body: ui.Text(MustLocaleURL(c, "fr", "/"))}, nil }}}})
		for _, host := range []string{"fr.example", "fr.example:" + port} {
			w := localizedRequest(app, "/", host, "", "", false)
			if w.Code != 200 || !strings.Contains(w.Body.String(), scheme+"://fr.example/") {
				t.Fatal(plainHTTP, host, w.Code, w.Body.String())
			}
			info, err := app.StaticExportConfig(StaticExportOptions{Domain: host})
			if err != nil || info.Domain != "fr.example" {
				t.Fatal(host, info, err)
			}
		}
		w := localizedRequest(app, "/", "fr.example:"+wrongPort, "", "", false)
		if w.Code != 421 {
			t.Fatal("foreign port accepted", plainHTTP, w.Code)
		}
		if _, err := app.StaticExportConfig(StaticExportOptions{Domain: "fr.example:" + wrongPort}); err == nil {
			t.Fatal("foreign port selected for export")
		}
		locales.Domains = append(locales.Domains, LocaleDomain{Host: "fr.example:" + port, Default: "en", HTTP: !plainHTTP})
		if other, err := New(Config{Locales: locales}); err == nil {
			_ = other.Close(context.Background())
			t.Fatal("ambiguous explicit/default-port domain policies accepted")
		}
	}
}
