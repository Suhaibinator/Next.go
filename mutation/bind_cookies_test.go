//go:build !js

package mutation

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Suhaibinator/Next.go/actions"
)

type cookieOutput struct {
	cookie *http.Cookie
	fail   bool
}

func (o cookieOutput) MarshalJSON() ([]byte, error) {
	o.cookie.Value = "changed-in-marshal"
	o.cookie.Unparsed[0] = "invalid\nmetadata"
	if o.fail {
		return nil, errors.New("private encoding failure")
	}
	return []byte(`{"ok":true}`), nil
}

func TestBindForwardsFrozenNativeCookiesWithoutJSONExposure(t *testing.T) {
	for _, mode := range []string{"json", "form", "view", "encode-error", "auth-error"} {
		t.Run(mode, func(t *testing.T) {
			cookie := &http.Cookie{Name: "session", Value: "original-cookie-value", Path: "/app", HttpOnly: true, Unparsed: []string{"original=metadata"}}
			effects := 0
			router := actions.New(actions.Options{LogError: func(*http.Request, error) {}, OnRevalidate: func([]string, []string) { effects++ }})
			contract := Must[input, cookieOutput]("cookie/save", "v1")
			contract.Bind(router, func(*actions.Context, input) (Result[cookieOutput], error) {
				return Result[cookieOutput]{Data: cookieOutput{cookie: cookie, fail: mode == "encode-error"}, Redirect: "/done", Cookies: []*http.Cookie{cookie}, RevalidateTags: []string{"session"}}, nil
			}, BindOptions[input]{Authorize: func(*actions.Context) error {
				if mode == "auth-error" {
					return actions.ErrUnauthorized
				}
				return nil
			}, Form: func(*actions.Context, url.Values) (input, error) { return input{1}, nil }})
			body, kind := "{}", "application/json"
			if mode == "form" {
				body, kind = "amount=1", "application/x-www-form-urlencoded"
			}
			req := httptest.NewRequest("POST", "https://example.test/"+contract.Key(), strings.NewReader(body))
			req.Header.Set("Origin", "https://example.test")
			req.Header.Set("Content-Type", kind)
			w := httptest.NewRecorder()
			if mode == "view" {
				router.ServeWithView(w, req, func(w http.ResponseWriter, _ *http.Request, result actions.ViewSuccess) {
					if len(w.Header().Values("Set-Cookie")) != 1 {
						t.Fatal("typed view misses cookies")
					}
					_, _ = w.Write(result.Encoded)
				})
			} else {
				router.ServeHTTP(w, req)
			}
			if mode == "encode-error" || mode == "auth-error" {
				if w.Code < 400 || len(w.Result().Cookies()) != 0 || effects != 0 {
					t.Fatal(w.Code, w.Header(), effects)
				}
				return
			}
			want := 200
			if mode == "form" {
				want = 303
			}
			cookies := w.Result().Cookies()
			if w.Code != want || effects != 1 || len(cookies) != 1 || cookies[0].Value != "original-cookie-value" || cookies[0].Path != "/app" || !cookies[0].HttpOnly {
				t.Fatal(w.Code, w.Header(), effects)
			}
			if strings.Contains(w.Body.String(), "original-cookie-value") || strings.Contains(w.Body.String(), "Cookies") {
				t.Fatal("native cookie exposed as typed data")
			}
			data, err := json.Marshal(Result[output]{Data: output{1}, Cookies: []*http.Cookie{cookie}})
			if err != nil || strings.Contains(string(data), "changed-in-marshal") || strings.Contains(string(data), "Cookies") {
				t.Fatal(string(data), err)
			}
		})
	}
}
