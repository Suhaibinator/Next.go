package nextgo

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/actions"
	"github.com/Suhaibinator/Next.go/internal/postresponse"
	"github.com/Suhaibinator/Next.go/mutation"
)

func TestActionRegistryAuditLocaleAuthorizationAndPlatformCompletion(t *testing.T) {
	type authorizedKey struct{}
	contract := mutation.Must[struct{}, string]("registry-audit", "v1")
	var factories, effects, completed atomic.Int64
	afterValues := make(chan string, 2)
	app := testApp(t, Config{BasePath: "/docs", Locales: &LocaleConfig{Codes: []string{"en", "fr"}, Default: "en", PrefixDefault: true}, Routes: []Route{{Pattern: "/", Page: static("home")}}, Middleware: []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := context.WithValue(r.Context(), authorizedKey{}, r.Header.Get("X-Audit-Authorized") == "yes")
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}}, ActionRegistrars: []func(*actions.Router){func(router *actions.Router) {
		factories.Add(1)
		contract.Bind(router, func(c *actions.Context, _ struct{}) (mutation.Result[string], error) {
			effects.Add(1)
			locale := CurrentLocale(c.Context)
			if _, err := LookupCacheProfile(c.Context, "default"); err != nil {
				return mutation.Result[string]{}, err
			}
			if err := After(c.Context, func(work context.Context) error {
				completed.Add(1)
				afterValues <- CurrentLocale(work) + ":" + MustURL(work, "/result")
				return nil
			}); err != nil {
				return mutation.Result[string]{}, err
			}
			return mutation.Result[string]{Data: locale, Redirect: "/result"}, nil
		}, mutation.BindOptions[struct{}]{Authorize: func(c *actions.Context) error {
			if permitted, _ := c.Value(authorizedKey{}).(bool); !permitted {
				return actions.ErrForbidden
			}
			return nil
		}})
	}}})
	defer app.Close(context.Background())
	if factories.Load() != 2 {
		t.Fatal("registrars not scoped to locale children", factories.Load())
	}
	for _, locale := range []string{"en", "fr"} {
		path := "/docs/" + locale + contract.Path()
		denied := declaredActionRequest(app, path, `{broken`)
		if denied.Code != 403 || effects.Load() != int64(completed.Load()) {
			t.Fatal("authorization did not precede decode/mutation", denied.Code, denied.Body.String(), effects.Load(), completed.Load())
		}
		ctx, collector := postresponse.Collect(context.Background())
		defer collector.Finish(context.Background(), false)
		request := httptest.NewRequest("POST", "http://example.com"+path, strings.NewReader(`{}`)).WithContext(ctx)
		request.Header.Set("Origin", "http://example.com")
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Audit-Authorized", "yes")
		response := httptest.NewRecorder()
		app.ServeHTTP(response, request)
		if response.Code != 200 || !strings.Contains(response.Body.String(), `"data":"`+locale+`"`) || !strings.Contains(response.Body.String(), `"redirect":"/docs/`+locale+`/result"`) {
			t.Fatal("action lost locale/paths/middleware", response.Code, response.Body.String())
		}
		select {
		case value := <-afterValues:
			t.Fatal("action After ran before platform accepted response", value)
		default:
		}
		completionCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		collector.Finish(completionCtx, true)
		cancel()
		select {
		case value := <-afterValues:
			if value != locale+":/docs/"+locale+"/result" {
				t.Fatal("completion lost locale context", value)
			}
		case <-time.After(time.Second):
			t.Fatal("action completion did not run")
		}
	}
	if effects.Load() != 2 || completed.Load() != 2 {
		t.Fatal("declaration bypassed action/completion lifecycle", effects.Load(), completed.Load())
	}
}

func TestActionRegistryAuditEmptyRegistrarsPreserveOpaqueHandler(t *testing.T) {
	var calls atomic.Int64
	opaque := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); fmt.Fprintf(w, "opaque:%s", r.URL.Path) })
	app := testApp(t, Config{Actions: opaque, Routes: []Route{{Pattern: "/", Page: static("home")}}})
	defer app.Close(context.Background())
	response := declaredActionRequest(app, "/_nextgo/actions/manual", `{}`)
	if response.Code != 200 || response.Body.String() != "opaque:manual" || calls.Load() != 1 {
		t.Fatal("empty registrars changed opaque dispatch", response.Code, response.Body.String(), calls.Load())
	}
}
