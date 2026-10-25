package nextgo

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Suhaibinator/Next.go/actions"
	"github.com/Suhaibinator/Next.go/ui"
)

func TestNativeActionCookiesReauthorizeTheSameResponse(t *testing.T) {
	type identityKey struct{}
	var effects, authenticatedGETs atomic.Int64
	app := testApp(t, Config{BuildID: "native-cookie-view", BasePath: "/account", Middleware: []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			cookie, err := r.Cookie("session")
			authorized := err == nil && cookie.Value == "authorized"
			if authorized && r.Method == http.MethodGet {
				authenticatedGETs.Add(1)
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), identityKey{}, authorized)))
		})
	}}, Routes: []Route{{Pattern: "/", Page: func(c *Context) (Page, error) {
		if authorized, _ := c.Value(identityKey{}).(bool); !authorized {
			return Page{Title: "Anonymous", Body: ui.Text("anonymous")}, nil
		}
		return Page{Title: "Authenticated", Body: ui.Text("fresh authorized view")}, nil
	}}}, ActionRegistrars: []func(*actions.Router){actionViewTestRegistrar(func(c *actions.Context) (actions.Result, error) {
		effects.Add(1)
		if c.Request.Header.Get("X-Test-Logout") == "yes" {
			return actions.Result{Data: "signed out", Cookies: []*http.Cookie{{Name: "session", Path: "/account", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode}}}, nil
		}
		return actions.Result{Data: "signed in", Cookies: []*http.Cookie{{Name: "session", Value: "authorized", Path: "/account", HttpOnly: true, SameSite: http.SameSiteLaxMode}}}, nil
	})}})
	defer app.Close(context.Background())
	login := actionViewTestServe(app, actionViewTestRequest(app, "/account"))
	frames := actionViewTestFrames(t, login)
	page := actionViewTestPage(t, frames)
	if page.Title != "Authenticated" || page.HTML != "fresh authorized view" || effects.Load() != 1 || authenticatedGETs.Load() != 1 {
		t.Fatal("native cookie was not reauthorized in the same action response", page, effects.Load(), authenticatedGETs.Load())
	}
	cookies := login.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].Path != "/account" || strings.Contains(string(frames[0].Result), "authorized") {
		t.Fatal("native cookie transport or typed output boundary changed", login.Header(), string(frames[0].Result))
	}
	logout := actionViewTestRequest(app, "/account")
	logout.AddCookie(cookies[0])
	logout.Header.Set("X-Test-Logout", "yes")
	response := actionViewTestServe(app, logout)
	page = actionViewTestPage(t, actionViewTestFrames(t, response))
	if page.Title != "Anonymous" || page.HTML != "anonymous" || effects.Load() != 2 || authenticatedGETs.Load() != 1 || len(response.Result().Cookies()) != 1 || response.Result().Cookies()[0].MaxAge >= 0 {
		t.Fatal("native logout retained previous identity in fresh view", page, effects.Load(), authenticatedGETs.Load(), response.Header())
	}
}
