//go:build !js

package embeds

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	nextgo "github.com/Suhaibinator/Next.go"
)

func TestEmbedsPageDeclaresServerDependencyAndResolvesMountedResources(t *testing.T) {
	app, err := nextgo.New(nextgo.Config{
		BasePath: "/site", AssetPrefix: "https://cdn.example.test/static",
		Routes: []nextgo.Route{{Pattern: "/embeds", Page: Page}},
	})
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/site/embeds", nil))
	if w.Code != http.StatusOK || w.Header().Get(nextgo.RequiresServerHeader) != "1" {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
	for _, want := range []string{`src="/site/api/embed?`, `poster="/site/_nextgo/image?`, `src="https://cdn.example.test/static/assets/embeds-demo.webm"`, `src="https://cdn.example.test/static/assets/embeds-demo.mp4"`, `src="https://cdn.example.test/static/assets/embeds-demo.wav"`, `src="https://cdn.example.test/static/assets/embeds-demo.vtt"`, `href="/site/media"`} {
		if !strings.Contains(w.Body.String(), want) {
			t.Errorf("missing mounted resource %q", want)
		}
	}
}
