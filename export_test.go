package nextgo

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestStaticExportScopeDeclarationsMatchRenderedOwnership(t *testing.T) {
	for _, prefixed := range []bool{false, true} {
		locales := localeConfig(prefixed)
		locales.Detect = false
		app := testApp(t, Config{BasePath: "/shop", Locales: locales, Routes: []Route{{Pattern: "/", Page: localizedPage}}})
		info, err := app.StaticExportConfig()
		if err != nil || info.Version != 1 || info.Paths.BasePath != "/shop" || len(info.Scopes) != 3 {
			t.Fatal(info, err)
		}
		seen := map[string]bool{}
		for _, scope := range info.Scopes {
			prefix := scope.Prefix
			if scope.Locale == "en" && !prefixed {
				if prefix != "" {
					t.Fatal("default prefix alias leaked into export")
				}
			} else if prefix != "/"+scope.Locale {
				t.Fatal(scope)
			}
			target := "/shop" + prefix
			response := localizedRequest(app, target, "", "", "", true)
			var envelope Envelope
			if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || envelope.Configuration != scope.Configuration || envelope.Document.Language != scope.Locale || seen[scope.Configuration] {
				t.Fatal(scope, envelope, err)
			}
			seen[scope.Configuration] = true
		}
		info.Scopes[0].Prefix = "/changed"
		again, err := app.StaticExportConfig()
		if err != nil || again.Scopes[0].Prefix == "/changed" {
			t.Fatal("export config aliases mutable application state", again, err)
		}
	}
}

func TestDomainStaticExportDeclaresOnlySelectedHostAndEnumeratesItsLocales(t *testing.T) {
	locales := localeConfig(false)
	locales.Detect = false
	locales.Domains = []LocaleDomain{{Host: "en.example", Default: "en"}, {Host: "fr.example:8443", Default: "fr"}}
	app := testApp(t, Config{Locales: locales, Routes: []Route{{Pattern: "/", Page: localizedPage}, {Pattern: "/posts/:slug", Page: localizedPage, StaticParams: func(ctx context.Context) ([]Params, error) {
		link, err := LocaleURL(ctx, "fr", "/posts/a")
		if err != nil || link != "https://fr.example:8443/posts/a" {
			t.Error("enumeration lost selected domain", link, err)
		}
		return []Params{{"slug": CurrentLocale(ctx)}}, nil
	}}}})
	info, err := app.StaticExportConfig(StaticExportOptions{Domain: "FR.Example.:8443"})
	if err != nil || info.Domain != "fr.example:8443" || len(info.Scopes) != 3 {
		t.Fatal(info, err)
	}
	for _, scope := range info.Scopes {
		if scope.Locale == "fr" && scope.Prefix != "" || scope.Locale != "fr" && scope.Prefix != "/"+scope.Locale {
			t.Fatal("wrong domain default", scope)
		}
		response := localizedRequest(app, scopedRoot(scope.Prefix), info.Domain, "", "", true)
		var envelope Envelope
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil || envelope.Configuration != scope.Configuration {
			t.Fatal(scope, envelope, err)
		}
	}
	paths, err := app.StaticPathsForDomain(context.Background(), "FR.Example.:8443")
	if err != nil || !reflect.DeepEqual(paths, []string{"/", "/ar", "/ar/posts/ar", "/en", "/en/posts/en", "/posts/fr"}) {
		t.Fatal(paths, err)
	}
	if _, err := app.StaticPaths(context.Background()); err == nil {
		t.Fatal("domain enumeration omitted required host selection")
	}
	if _, err := app.StaticPathsForDomain(nil, info.Domain); err == nil {
		t.Fatal("nil enumeration context accepted")
	}
	for _, domain := range []string{"", "unconfigured.example", "https://fr.example:8443", "user@fr.example:8443", "fr.example:8443/path"} {
		if _, err := app.StaticExportConfig(StaticExportOptions{Domain: domain}); err == nil {
			t.Fatal("invalid/missing selection accepted", domain)
		}
	}
	if _, err := app.StaticExportConfig(StaticExportOptions{}, StaticExportOptions{}); err == nil {
		t.Fatal("multiple options silently accepted")
	}
	plain := testApp(t, Config{})
	if _, err := plain.StaticExportConfig(StaticExportOptions{Domain: "fr.example"}); err == nil {
		t.Fatal("plain app accepted a foreign domain selector")
	}
}

func scopedRoot(prefix string) string {
	if prefix == "" {
		return "/"
	}
	return prefix
}

func TestStaticExportRejectsRequestTimeLocaleRouting(t *testing.T) {
	for _, domain := range []bool{false, true} {
		locales := localeConfig(true)
		if domain {
			locales.Detect = false
			locales.Domains = []LocaleDomain{{Host: "example.test", Default: "en"}}
		}
		app := testApp(t, Config{Locales: locales})
		if _, err := app.StaticExportConfig(); err == nil {
			t.Fatal("request-time locale behavior silently accepted for static hosting")
		}
	}
	plain := testApp(t, Config{})
	info, err := plain.StaticExportConfig()
	if err != nil || len(info.Scopes) != 1 || info.Scopes[0].Configuration != plain.configurationID || info.Scopes[0].Prefix != "" || info.Scopes[0].Locale != "" {
		t.Fatal(info, err)
	}
}
