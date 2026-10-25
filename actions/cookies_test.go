package actions

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/internal/actionview"
)

func TestActionCookieSuccessJSONFormAndView(t *testing.T) {
	for _, mode := range []string{"json", "form", "view"} {
		t.Run(mode, func(t *testing.T) {
			expires := time.Date(2030, 2, 3, 4, 5, 6, 0, time.UTC)
			cookies := []*http.Cookie{
				{Name: "session", Value: "credential-only-in-header", Path: "/app/en", Domain: ".example.test", Expires: expires, MaxAge: 300, Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode},
				{Name: "session", Value: "deleted", Path: "/app/en/private", Domain: "example.test", MaxAge: -1, Expires: time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)},
				{Name: "default-path", Value: "default"},
			}
			effects, renders := 0, 0
			w := httptest.NewRecorder()
			router := New(Options{OnRevalidate: func([]string, []string) {
				effects++
				if len(w.Header().Values("Set-Cookie")) != 0 {
					t.Fatal("cookies published before invalidation completed")
				}
			}})
			router.Handle("save", func(*Context) (Result, error) {
				return Result{Data: map[string]bool{"ok": true}, Redirect: "/done", Cookies: cookies, RevalidateTags: []string{"session"}}, nil
			})
			contentType, body := "application/json", "{}"
			if mode == "form" {
				contentType, body = "application/x-www-form-urlencoded", "name=Ada"
			}
			req := request("POST", "/save", body, contentType)
			if mode == "view" {
				req.Header.Set("Accept", actionview.MediaType)
				router.ServeWithView(w, req, func(w http.ResponseWriter, _ *http.Request, result ViewSuccess) {
					renders++
					if len(w.Header().Values("Set-Cookie")) != 3 {
						t.Fatal("fresh view cannot observe action cookies")
					}
					_, _ = w.Write(result.Encoded)
				})
			} else {
				router.ServeHTTP(w, req)
			}
			want := 200
			if mode == "form" {
				want = 303
			}
			if w.Code != want || effects != 1 || mode == "view" && renders != 1 || w.Header().Get("Cache-Control") != "private, no-store" {
				t.Fatal(w.Code, effects, renders, w.Header())
			}
			got := w.Result().Cookies()
			if len(got) != 3 {
				t.Fatal(w.Header())
			}
			if got[0].Name != "session" || got[0].Value != "credential-only-in-header" || got[0].Path != "/app/en" || got[0].Domain != "example.test" || !got[0].Expires.Equal(expires) || got[0].MaxAge != 300 || !got[0].Secure || !got[0].HttpOnly || got[0].SameSite != http.SameSiteLaxMode {
				t.Fatalf("cookie %#v", got[0])
			}
			if got[1].MaxAge != -1 || !got[1].Expires.Before(time.Now()) || got[2].Path != "" {
				t.Fatalf("delete/default semantics %#v %#v", got[1], got[2])
			}
			if strings.Contains(w.Body.String(), "credential-only-in-header") || strings.Contains(w.Body.String(), "Cookies") {
				t.Fatal("native cookie serialized into JSON/HTML")
			}
			data, err := json.Marshal(Result{Data: 1, Cookies: cookies})
			if err != nil || strings.Contains(string(data), "credential-only-in-header") || strings.Contains(string(data), "Cookies") {
				t.Fatal("Result cookies are not excluded from JSON", string(data), err)
			}
		})
	}
}

type cookieChangingData struct{ change func() }

func (d cookieChangingData) MarshalJSON() ([]byte, error) {
	d.change()
	return []byte(`{"ok":true}`), nil
}

func TestActionCookiesFreezeBeforeDataMarshalAndHooks(t *testing.T) {
	cookie := &http.Cookie{Name: "session", Value: "original", Path: "/app", Unparsed: []string{"original=metadata"}}
	router := New(Options{OnRevalidate: func([]string, []string) {
		cookie.Value = "hook-mutated"
		cookie.Path = "/elsewhere"
		cookie.Unparsed[0] = "hook\ninvalid"
	}})
	router.Handle("save", func(*Context) (Result, error) {
		return Result{Data: cookieChangingData{change: func() {
			cookie.Value = "marshal-mutated"
			cookie.Path = "relative"
			cookie.Unparsed[0] = "marshal\ninvalid"
		}}, Cookies: []*http.Cookie{cookie}, RevalidateTags: []string{"session"}}, nil
	})
	w := perform(router, request("POST", "/save", "{}", "application/json"))
	got := w.Result().Cookies()
	if w.Code != 200 || len(got) != 1 || got[0].Value != "original" || got[0].Path != "/app" || cookie.Value != "hook-mutated" {
		t.Fatal(w.Code, w.Header(), cookie)
	}
}

func TestInvalidCookieOutputsAreAtomicAndDoNotInvalidate(t *testing.T) {
	invalid := map[string][]*http.Cookie{
		"nil": {nil}, "bad-name": {{Name: "bad name", Value: "secret-value"}}, "bad-value": {{Name: "session", Value: "secret\nvalue"}},
		"relative-path": {{Name: "session", Value: "secret-value", Path: "private"}}, "path-backslash": {{Name: "session", Value: "secret-value", Path: "/path\\other"}}, "path-control": {{Name: "session", Value: "secret-value", Path: "/private\npath"}},
		"domain-invalid":      {{Name: "session", Value: "secret-value", Domain: "bad/domain"}},
		"duplicate":           {{Name: "session", Value: "one", Path: "/", Domain: ".example.test"}, {Name: "session", Value: "two", Path: "/", Domain: "EXAMPLE.TEST"}},
		"mixed-valid-invalid": {{Name: "valid", Value: "secret-value", Path: "/"}, {Name: "invalid", Value: "secret-value", Path: "relative"}},
		"raw-control":         {{Name: "session", Value: "secret-value", Raw: "secret\rraw"}}, "metadata-control": {{Name: "session", Value: "secret-value", Unparsed: []string{"secret\tmetadata"}}},
		"invalid-expiry": {{Name: "session", Value: "secret-value", Expires: time.Date(1500, 1, 1, 0, 0, 0, 0, time.UTC)}},
	}
	for name, cookies := range invalid {
		t.Run(name, func(t *testing.T) {
			mutations, effects, renders := 0, 0, 0
			var logs []string
			router := New(Options{LogError: func(_ *http.Request, err error) { logs = append(logs, err.Error()) }, OnRevalidate: func([]string, []string) { effects++ }})
			router.Handle("save", func(*Context) (Result, error) {
				mutations++
				return Result{Data: 1, Cookies: cookies, RevalidateTags: []string{"session"}}, nil
			})
			w := httptest.NewRecorder()
			router.ServeWithView(w, request("POST", "/save", "{}", "application/json"), func(http.ResponseWriter, *http.Request, ViewSuccess) { renders++ })
			if w.Code != 500 || len(w.Header().Values("Set-Cookie")) != 0 || effects != 0 || renders != 0 || mutations != 1 || len(logs) != 1 {
				t.Fatal(w.Code, w.Header(), effects, renders, mutations, logs)
			}
			if !strings.Contains(w.Body.String(), "Reconcile before retrying") || strings.Contains(w.Body.String(), "secret") || strings.Contains(strings.Join(logs, " "), "secret") {
				t.Fatal("cookie output failure leaked values or implied safe retry", w.Body.String(), logs)
			}
		})
	}
}

func TestCookieSecurityAndScopePolicy(t *testing.T) {
	tests := []struct {
		name   string
		cookie *http.Cookie
		valid  bool
	}{
		{"host-valid", &http.Cookie{Name: "__Host-session", Value: "x", Path: "/", Secure: true}, true},
		{"host-no-secure", &http.Cookie{Name: "__Host-session", Value: "x", Path: "/"}, false},
		{"host-no-path", &http.Cookie{Name: "__Host-session", Value: "x", Secure: true}, false},
		{"host-scoped", &http.Cookie{Name: "__Host-session", Value: "x", Path: "/app", Secure: true}, false},
		{"host-domain", &http.Cookie{Name: "__Host-session", Value: "x", Path: "/", Domain: "example.test", Secure: true}, false},
		{"secure-valid", &http.Cookie{Name: "__Secure-session", Value: "x", Secure: true}, true},
		{"secure-required", &http.Cookie{Name: "__Secure-session", Value: "x"}, false},
		{"none-secure", &http.Cookie{Name: "session", Value: "x", SameSite: http.SameSiteNoneMode, Secure: true}, true},
		{"none-insecure", &http.Cookie{Name: "session", Value: "x", SameSite: http.SameSiteNoneMode}, false},
		{"samesite-unknown", &http.Cookie{Name: "session", Value: "x", SameSite: http.SameSite(99)}, false},
		{"partitioned-secure", &http.Cookie{Name: "session", Value: "x", Partitioned: true, Secure: true}, true},
		{"partitioned-insecure", &http.Cookie{Name: "session", Value: "x", Partitioned: true}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers, err := prepareCookieHeaders([]*http.Cookie{tt.cookie})
			if (err == nil) != tt.valid || tt.valid && len(headers) != 1 {
				t.Fatal(tt.valid, headers, err)
			}
		})
	}
	for _, cookies := range [][]*http.Cookie{
		{{Name: "session", Value: "root", Path: "/"}, {Name: "session", Value: "scoped", Path: "/private"}},
		{{Name: "session", Value: "parent", Path: "/", Domain: "example.test"}, {Name: "session", Value: "subdomain", Path: "/", Domain: "child.example.test"}},
	} {
		if headers, err := prepareCookieHeaders(cookies); err != nil || len(headers) != 2 {
			t.Fatal("distinct scopes rejected", headers, err)
		}
	}
}

func TestCookieLimits(t *testing.T) {
	exact := &http.Cookie{Name: "x", Value: strings.Repeat("a", maxResponseCookieBytes-2)}
	if headers, err := prepareCookieHeaders([]*http.Cookie{exact}); err != nil || len(headers[0]) != maxResponseCookieBytes {
		t.Fatal(headers, err)
	}
	over := *exact
	over.Value += "a"
	if _, err := prepareCookieHeaders([]*http.Cookie{&over}); err == nil {
		t.Fatal("overlong cookie accepted")
	}
	var exactTotal []*http.Cookie
	for i := 0; i < 4; i++ {
		name := fmt.Sprintf("c%d", i)
		exactTotal = append(exactTotal, &http.Cookie{Name: name, Value: strings.Repeat("a", maxResponseCookieBytes-len(name)-1)})
	}
	if _, err := prepareCookieHeaders(exactTotal); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareCookieHeaders(append(exactTotal, &http.Cookie{Name: "extra", Value: "x"})); err == nil {
		t.Fatal("cookie total limit ignored")
	}
	var cookies []*http.Cookie
	for i := 0; i < maxResponseCookies; i++ {
		cookies = append(cookies, &http.Cookie{Name: fmt.Sprintf("c%d", i), Value: "x"})
	}
	if _, err := prepareCookieHeaders(cookies); err != nil {
		t.Fatal(err)
	}
	if _, err := prepareCookieHeaders(append(cookies, &http.Cookie{Name: "extra", Value: "x"})); err == nil {
		t.Fatal("cookie count limit ignored")
	}
}

func TestCookiesSuppressedOnErrorsAndInvalidViewOutput(t *testing.T) {
	for _, stage := range []string{"validation", "auth", "encode", "redirect", "hook", "view-size", "view-duplicate"} {
		t.Run(stage, func(t *testing.T) {
			effects := 0
			router := New(Options{LogError: func(*http.Request, error) {}, OnRevalidate: func([]string, []string) {
				effects++
				if stage == "hook" {
					panic("hook failed")
				}
			}})
			router.Handle("save", func(*Context) (Result, error) {
				result := Result{Data: 1, Cookies: []*http.Cookie{{Name: "session", Value: "credential", Path: "/"}}, RevalidateTags: []string{"session"}}
				switch stage {
				case "validation":
					return result, &ValidationError{Message: "Invalid"}
				case "auth":
					return result, ErrUnauthorized
				case "encode":
					result.Data = make(chan int)
				case "redirect":
					result.Redirect = "https://evil.test"
				case "view-size":
					result.Data = strings.Repeat("a", actionview.MaxResult)
				case "view-duplicate":
					result.Data = json.RawMessage(`{"nested":{"x":1,"x":2}}`)
				}
				return result, nil
			})
			w := httptest.NewRecorder()
			router.ServeWithView(w, request("POST", "/save", "{}", "application/json"), func(http.ResponseWriter, *http.Request, ViewSuccess) { t.Fatal("failed result rendered") })
			if w.Code < 400 || len(w.Header().Values("Set-Cookie")) != 0 {
				t.Fatal(stage, w.Code, w.Header())
			}
			wantEffects := 0
			if stage == "hook" || stage == "view-size" || stage == "view-duplicate" {
				wantEffects = 1
			}
			if effects != wantEffects {
				t.Fatal(stage, effects, wantEffects)
			}
		})
	}
	// The ordinary protocol has no new view result cap, and keeps cookies even
	// when a caller might independently impose a smaller response budget.
	router := New(Options{})
	router.Handle("large", func(*Context) (Result, error) {
		return Result{Data: strings.Repeat("a", actionview.MaxResult), Cookies: []*http.Cookie{{Name: "session", Value: "x", Path: "/"}}}, nil
	})
	w := perform(router, request("POST", "/large", "{}", "application/json"))
	if w.Code != 200 || len(w.Result().Cookies()) != 1 {
		t.Fatal(w.Code, w.Header())
	}
}

func TestLegacyInvalidCookiesStillUseProgressiveHTMLErrors(t *testing.T) {
	router := New(Options{LogError: func(*http.Request, error) {}})
	router.Handle("save", func(*Context) (Result, error) { return Result{Data: 1, Cookies: []*http.Cookie{nil}}, nil })
	w := perform(router, request("POST", "/save", "name=Ada", "application/x-www-form-urlencoded"))
	if w.Code != 500 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") || len(w.Result().Cookies()) != 0 || !strings.Contains(w.Body.String(), "Reconcile before retrying") {
		t.Fatal(w.Code, w.Header(), w.Body.String())
	}
}
