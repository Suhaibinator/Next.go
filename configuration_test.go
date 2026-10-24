package nextgo

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/ui"
)

func TestConfigurationIdentityAppearsInDocumentsEnvelopesAndLoading(t *testing.T) {
	for _, slash := range []bool{false, true} {
		pagePath := "/about"
		if slash {
			pagePath += "/"
		}
		a := testApp(t, Config{BuildID: "same", TrailingSlash: slash, Routes: []Route{{Pattern: "/about", Page: static("about"), Loading: func(c *Context) (ui.Node, error) {
			_ = c.SetHeader(navigation.ConfigurationHeader, "application value")
			return ui.Text("Loading"), nil
		}}, {Pattern: "/stream", Page: func(*Context) (Page, error) {
			return Page{Stream: true, Body: ui.Defer("late", ui.Text("Wait"), func(context.Context) (ui.Node, error) { return ui.Text("Ready"), nil })}, nil
		}}}})
		for _, path := range []string{pagePath, "/missing" + strings.TrimPrefix(pagePath, "/about"), "/stream" + strings.TrimPrefix(pagePath, "/about")} {
			document := request(a, path, false)
			if !strings.Contains(document.Body.String(), `data-nextgo-configuration="`+a.configurationID+`"`) || document.Header().Get(navigation.ConfigurationHeader) != a.configurationID {
				t.Fatalf("missing document identity at %s: %s", path, document.Body.String())
			}
			fragment := request(a, path, true)
			var data Envelope
			if err := json.Unmarshal(fragment.Body.Bytes(), &data); err != nil || data.Configuration != a.configurationID || fragment.Header().Get(navigation.ConfigurationHeader) != a.configurationID {
				t.Fatalf("fragment identity: %+v %v", data, err)
			}
		}
		loading := request(a, "/_nextgo/loading?path="+pagePath, false)
		if loading.Code != 200 || loading.Header().Get(navigation.ConfigurationHeader) != a.configurationID {
			t.Fatal("loading identity", loading.Code, loading.Header())
		}
	}
}

func TestConfigurationIdentityStableAcrossRouteRegistrationOrder(t *testing.T) {
	a := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: static("home")}, {Pattern: "/about", Page: static("about")}}, Handlers: map[string]http.Handler{"/x": http.NotFoundHandler(), "/y": http.NotFoundHandler()}})
	b := testApp(t, Config{Routes: []Route{{Pattern: "/about", Page: static("about")}, {Pattern: "/", Page: static("home")}}, Handlers: map[string]http.Handler{"/y": http.NotFoundHandler(), "/x": http.NotFoundHandler()}})
	if a.configurationID != b.configurationID {
		t.Fatal("registration order changes compatibility identity")
	}
	w := httptest.NewRecorder()
	a.ServeHTTP(w, httptest.NewRequest("GET", "/x", nil))
	if w.Header().Get(navigation.ConfigurationHeader) != "" {
		t.Fatal("plain handler advertised framework page identity")
	}
}

func TestConfigurationNamespacesPageSnapshotsAcrossSharedStores(t *testing.T) {
	store := testApp(t, Config{}).config.Cache
	for _, preserve := range []bool{false, true} {
		app := testApp(t, Config{BuildID: "same", Cache: store, PreserveLayouts: preserve, Routes: []Route{{Pattern: "/", Cache: &CachePolicy{TTL: time.Minute}, LayoutIDs: []string{"root"}, Layouts: []LayoutFunc{func(_ *Context, n ui.Node) (ui.Node, error) { return ui.El("main", nil, n), nil }}, Page: static("cached")}}})
		response := request(app, "/", true)
		var data Envelope
		if err := json.Unmarshal(response.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(data.HTML, "nextgo-layout") != preserve {
			t.Fatalf("snapshot reused across layout ownership: preserve=%v html=%s", preserve, data.HTML)
		}
	}
}
