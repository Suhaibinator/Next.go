package nextgo_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/assets"
	"github.com/Suhaibinator/Next.go/examples/demo/localfonts"
	"github.com/Suhaibinator/Next.go/ui"
)

func TestManagedFontsFreezeDeclarationsAndPublicDispatch(t *testing.T) {
	sets := []*assets.LocalFonts{localfonts.Set(), localfonts.Set()}
	app, err := nextgo.New(nextgo.Config{BuildID: "fonts", BasePath: "/app", AssetPrefix: "https://cdn.example/static", LocalFonts: sets,
		Middleware: []func(http.Handler) http.Handler{func(http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "page middleware", 403) })
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer app.Close(context.Background())
	sets[0], sets[1] = nil, nil
	config, err := app.StaticExportConfig()
	if err != nil || config.Version != 4 || len(config.Fonts) != 3 {
		t.Fatal(config, err)
	}
	url := config.Fonts[0].URL
	config.Fonts[0].URL = "/mutated"
	again, _ := app.StaticExportConfig()
	if again.Fonts[0].URL != url {
		t.Fatal("export inventory borrowed application memory")
	}
	response := httptest.NewRecorder()
	app.ServeHTTP(response, httptest.NewRequest("GET", "/app"+url, nil))
	if response.Code != 200 || response.Header().Get("X-Nextgo-Build") != "fonts" || !strings.Contains(response.Header().Get("Cache-Control"), "immutable") {
		t.Fatal(response.Code, response.Header())
	}
	missing := httptest.NewRecorder()
	app.ServeHTTP(missing, httptest.NewRequest("GET", "/app/_nextgo/local-fonts/missing.css", nil))
	if missing.Code == 200 {
		t.Fatal("unregistered font was served")
	}
}

func TestManagedFontsRejectConflictingAndUnconstructedDeclarations(t *testing.T) {
	set := localfonts.Set()
	for _, config := range []nextgo.Config{
		{LocalFonts: []*assets.LocalFonts{nil}},
		{LocalFonts: []*assets.LocalFonts{{}}},
		{LocalFonts: []*assets.LocalFonts{set}, Handlers: map[string]http.Handler{"/_nextgo/": http.NotFoundHandler()}},
		{LocalFonts: []*assets.LocalFonts{set}, Routes: []nextgo.Route{{Pattern: "/_nextgo/local-fonts/:file", Page: func(*nextgo.Context) (nextgo.Page, error) { return nextgo.Page{Body: ui.Text("collision")}, nil }}}},
	} {
		if app, err := nextgo.New(config); err == nil {
			app.Close(context.Background())
			t.Fatal("accepted invalid font declaration")
		}
	}
}
