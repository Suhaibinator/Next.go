package nextgo

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/actions"
	"github.com/Suhaibinator/Next.go/mutation"
	"github.com/Suhaibinator/Next.go/ui"
)

func declaredActionRequest(handler http.Handler, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "http://example.com"+path, strings.NewReader(body))
	r.Header.Set("Origin", "http://example.com")
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func TestDeclaredActionsDefaultRouterInvalidatesScopedLocalPages(t *testing.T) {
	for _, base := range []string{"", "/base"} {
		t.Run(base, func(t *testing.T) {
			var count, loads atomic.Int64
			contract := mutation.Must[struct{}, int64]("declared/increment", "v1")
			app := testApp(t, Config{BasePath: base, Routes: []Route{{Pattern: "/counter", Cache: &CachePolicy{TTL: time.Hour, Tags: []string{"declared-counter"}}, Page: func(*Context) (Page, error) {
				loads.Add(1)
				return Page{Body: ui.Text(strconv.FormatInt(count.Load(), 10))}, nil
			}}}, ActionRegistrars: []func(*actions.Router){func(router *actions.Router) {
				contract.Bind(router, func(*actions.Context, struct{}) (mutation.Result[int64], error) {
					return mutation.Result[int64]{Data: count.Add(1), RevalidateTags: []string{"declared-counter"}, RevalidatePaths: []string{"/counter"}}, nil
				})
			}}})
			defer app.Close(t.Context())
			for i := 0; i < 2; i++ {
				if response := request(app, base+"/counter", false); response.Code != 200 {
					t.Fatal(response.Code)
				}
			}
			if loads.Load() != 1 {
				t.Fatal("page did not reuse pre-mutation value", loads.Load())
			}
			if response := declaredActionRequest(app, base+contract.Path(), `{}`); response.Code != 200 || !strings.Contains(response.Body.String(), `"data":1`) {
				t.Fatal("declared typed action failed", response.Code, response.Body.String())
			}
			if response := request(app, base+"/counter", true); response.Code != 200 || !strings.Contains(response.Body.String(), `"html":"1"`) || loads.Load() != 2 {
				t.Fatal("successful action retained old page", response.Code, loads.Load(), response.Body.String())
			}
			if len(app.config.ActionRegistrars) != 0 {
				t.Fatal("application retained startup factories")
			}
		})
	}
}

func TestDeclaredActionsCloneManualRouterPerApplication(t *testing.T) {
	var invalidations atomic.Int64
	original := actions.New(actions.Options{OnRevalidate: func(_, _ []string) { invalidations.Add(1) }})
	original.Handle("manual", func(*actions.Context) (actions.Result, error) { return actions.Result{Data: "manual"}, nil })
	config := Config{Actions: original, Routes: []Route{{Pattern: "/", Page: static("home")}}, ActionRegistrars: []func(*actions.Router){func(router *actions.Router) {
		router.Handle("generated", func(*actions.Context) (actions.Result, error) {
			return actions.Result{Data: "generated", RevalidateTags: []string{"changed"}}, nil
		})
	}}}
	first, second := testApp(t, config), testApp(t, config)
	defer first.Close(t.Context())
	defer second.Close(t.Context())
	if first.config.Actions == original || second.config.Actions == original || first.config.Actions == second.config.Actions {
		t.Fatal("application aliases original registrations")
	}
	if response := declaredActionRequest(original, "/generated", `{}`); response.Code != 404 {
		t.Fatal("declaration changed original router", response.Code)
	}
	for _, app := range []*App{first, second} {
		for _, name := range []string{"manual", "generated"} {
			if response := declaredActionRequest(app, "/_nextgo/actions/"+name, `{}`); response.Code != 200 || !strings.Contains(response.Body.String(), name) {
				t.Fatal("manual/generated action missing", response.Code, response.Body.String())
			}
		}
	}
	if invalidations.Load() != 2 {
		t.Fatal("custom invalidation hook lost or duplicated", invalidations.Load())
	}
	original.Handle("later", func(*actions.Context) (actions.Result, error) { return actions.Result{}, nil })
	if response := declaredActionRequest(first, "/_nextgo/actions/later", `{}`); response.Code != 404 {
		t.Fatal("late original registration changed running application")
	}
}

func TestDeclaredActionsRejectInvalidStartupWithoutPublishingPartialClone(t *testing.T) {
	original := actions.New(actions.Options{})
	original.Handle("existing", func(*actions.Context) (actions.Result, error) { return actions.Result{}, nil })
	var called atomic.Int64
	for _, test := range []struct {
		name       string
		handler    http.Handler
		registrars []func(*actions.Router)
	}{
		{"nil registrar", original, []func(*actions.Router){nil}},
		{"opaque handler", http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}), []func(*actions.Router){func(*actions.Router) { called.Add(1) }}},
		{"typed nil", (*actions.Router)(nil), []func(*actions.Router){func(*actions.Router) { called.Add(1) }}},
		{"too many", original, make([]func(*actions.Router), maxActionRegistrars+1)},
		{"duplicate after prior registration", original, []func(*actions.Router){func(router *actions.Router) {
			router.Handle("partial", func(*actions.Context) (actions.Result, error) { return actions.Result{}, nil })
			router.Handle("existing", func(*actions.Context) (actions.Result, error) { return actions.Result{}, nil })
		}}},
		{"application panic", original, []func(*actions.Router){func(*actions.Router) { panic("bad declaration") }}},
		{"nil panic", original, []func(*actions.Router){func(*actions.Router) { panic(nil) }}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if app, err := New(Config{Actions: test.handler, ActionRegistrars: test.registrars, Routes: []Route{{Pattern: "/", Page: static("home")}}}); err == nil || app != nil {
				t.Fatal("invalid action configuration accepted", app, err)
			}
			if response := declaredActionRequest(original, "/partial", `{}`); response.Code != 404 {
				t.Fatal("failed startup mutated original router")
			}
		})
	}
	if called.Load() != 0 {
		t.Fatal("invalid configuration executed registrar")
	}
}
