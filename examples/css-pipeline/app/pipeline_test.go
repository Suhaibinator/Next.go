//go:build !js

package app

import (
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	nextgo "github.com/Suhaibinator/Next.go"
)

func TestGeneratedStylesheetReferenceAndRevalidation(t *testing.T) {
	t.Setenv("NEXTGO_CSS_STATIC", "")
	for _, prefix := range []string{"", "/cdn"} {
		config := nextgo.Config{BuildID: "css-example", AssetPrefix: prefix, Assets: fstest.MapFS{"public/style.css": {Data: []byte("body { color: blue; }")}}, Routes: []nextgo.Route{{Pattern: "/", Page: Page}}}
		Configure(&config)
		app, err := nextgo.New(config)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer app.Close(context.Background())
			page := httptest.NewRecorder()
			app.ServeHTTP(page, httptest.NewRequest("GET", "/", nil))
			if page.Code != 200 || !strings.Contains(page.Body.String(), `href="`+prefix+`/assets/style.css"`) {
				t.Fatal(page.Code, page.Body.String())
			}
			css := httptest.NewRecorder()
			app.ServeHTTP(css, httptest.NewRequest("GET", prefix+"/assets/style.css", nil))
			if css.Code != 200 || css.Header().Get("Cache-Control") != "no-cache, must-revalidate" || !strings.Contains(css.Body.String(), "blue") {
				t.Fatal(css.Code, css.Header(), css.Body.String())
			}
		}()
	}
}

func TestGeneratedStylesheetRenderedWithBasePathAndAbsoluteCDN(t *testing.T) {
	t.Setenv("NEXTGO_CSS_STATIC", "")
	config := nextgo.Config{BuildID: "css-example-cdn", BasePath: "/site", AssetPrefix: "https://cdn.test/static", Assets: fstest.MapFS{"public/style.css": {Data: []byte("body { color: blue; }")}}, Routes: []nextgo.Route{{Pattern: "/", Page: Page}}}
	Configure(&config)
	app, err := nextgo.New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close(context.Background())
	page := httptest.NewRecorder()
	app.ServeHTTP(page, httptest.NewRequest("GET", "/site", nil))
	markup := page.Body.String()
	if page.Code != 200 || !strings.Contains(markup, `data-nextgo-head="" href="https://cdn.test/static/assets/style.css" rel="stylesheet"`) {
		t.Fatal(page.Code, markup)
	}
	for _, wrong := range []string{"https://cdn.test/static/site/assets/style.css", "/site/site/assets/style.css", "https://cdn.test/static/assets/assets/style.css"} {
		if strings.Contains(markup, wrong) {
			t.Fatal("stylesheet mount applied twice", wrong, markup)
		}
	}
	css := httptest.NewRecorder()
	app.AssetHandler().ServeHTTP(css, httptest.NewRequest("GET", "/assets/style.css", nil))
	if css.Code != 200 || css.Header().Get("Cache-Control") != "no-cache, must-revalidate" {
		t.Fatal(css.Code, css.Header())
	}
}

func TestNativeCSSPolicyRejectsStaticExport(t *testing.T) {
	t.Setenv("NEXTGO_CSS_STATIC", "")
	config := nextgo.Config{BuildID: "css-native-export-check", Routes: []nextgo.Route{{Pattern: "/", Page: Page}}}
	Configure(&config)
	app, err := nextgo.New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close(context.Background())
	if _, err := app.StaticExportConfig(); err == nil || !strings.Contains(err.Error(), "response header rules require an HTTP server") {
		t.Fatal("native response policy must refuse static export", err)
	} else {
		t.Log(err)
	}
}

func TestStaticCSSDeploymentModeSupportsExport(t *testing.T) {
	t.Setenv("NEXTGO_CSS_STATIC", "true")
	config := nextgo.Config{BuildID: "css-static-export-check", BasePath: "/site", AssetPrefix: "https://cdn.test/static", Assets: fstest.MapFS{"public/style.css": {Data: []byte("body { color: blue; }")}}, Routes: []nextgo.Route{{Pattern: "/", Page: Page}}}
	Configure(&config)
	if len(config.HeaderRules) != 0 {
		t.Fatal("static deployment mode retained native-only header rule")
	}
	app, err := nextgo.New(config)
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close(context.Background())
	export, err := app.StaticExportConfig()
	if err != nil {
		t.Fatal("explicit static deployment mode refused export", err)
	}
	if export.Build != config.BuildID || export.Paths.BasePath != "/site" || export.Paths.AssetPrefix != "https://cdn.test/static" {
		t.Fatal("export config lost deployment paths", export)
	}
	page := httptest.NewRecorder()
	app.ServeHTTP(page, httptest.NewRequest("GET", "/site", nil))
	if page.Code != 200 || !strings.Contains(page.Body.String(), `data-nextgo-head="" href="https://cdn.test/static/assets/style.css" rel="stylesheet"`) {
		t.Fatal(page.Code, page.Body.String())
	}
	css := httptest.NewRecorder()
	app.AssetHandler().ServeHTTP(css, httptest.NewRequest("GET", "/assets/style.css", nil))
	if css.Code != 200 || !strings.Contains(css.Body.String(), "blue") {
		t.Fatal(css.Code, css.Body.String())
	}
}
