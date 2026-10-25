//go:build !js

package mutation

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Suhaibinator/Next.go/actions"
	"github.com/Suhaibinator/Next.go/internal/actionwire"
)

func TestBindConflictJSONAndProgressiveFormsPreserveExistingProtocol(t *testing.T) {
	contract := Must[input, output]("records/update", "v1")
	for _, formMode := range []bool{false, true} {
		t.Run(fmt.Sprint("form=", formMode), func(t *testing.T) {
			invalidations, handlers, mapped := 0, 0, 0
			router := actions.New(actions.Options{OnRevalidate: func([]string, []string) { invalidations++ }})
			contract.Bind(router, func(_ *actions.Context, value input) (Result[output], error) {
				handlers++
				if value.Amount != 1 {
					t.Fatal(value)
				}
				return Result[output]{Data: output{Count: 99}, Cookies: []*http.Cookie{{Name: "session", Value: "private"}}, Redirect: "/saved", RevalidateTags: []string{"records"}}, fmt.Errorf("private storage details: %w", &actions.ConflictError{Fields: map[string]string{"revision": "Load the current revision."}})
			}, BindOptions[input]{Form: func(_ *actions.Context, form url.Values) (input, error) {
				mapped++
				if len(form["amount"]) != 1 || form.Get("amount") != "1" {
					t.Fatal(form)
				}
				return input{Amount: 1}, nil
			}})
			body, media := `{"amount":1}`, "application/json"
			if formMode {
				body, media = "amount=1", "application/x-www-form-urlencoded"
			}
			req := httptest.NewRequest("POST", "http://example.test/"+contract.Key(), strings.NewReader(body))
			req.Header.Set("Origin", "http://example.test")
			req.Header.Set("Content-Type", media)
			if !formMode {
				req.Header.Set(actionwire.ProtocolHeader, actionwire.Protocol)
				req.Header.Set(actionwire.RevisionHeader, contract.Revision())
			}
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)
			if w.Code != 409 || handlers != 1 || invalidations != 0 || len(w.Header().Values("Set-Cookie")) != 0 || w.Header().Get("Location") != "" || strings.Contains(w.Body.String(), "private storage") || w.Header().Get(actionwire.ProtocolHeader) != actionwire.Protocol || w.Header().Get(actionwire.RevisionHeader) != contract.Revision() {
				t.Fatal(w.Code, w.Header(), w.Body.String(), handlers, invalidations)
			}
			if formMode {
				if mapped != 1 || !strings.Contains(w.Body.String(), "Load the current revision.") || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
					t.Fatal(mapped, w.Header(), w.Body.String())
				}
			} else {
				_, err := decode[output](w.Code, w.Header().Get(actionwire.ProtocolHeader), w.Header().Get(actionwire.RevisionHeader), contract.Revision(), w.Body.Bytes())
				var public *Error
				if mapped != 0 || !errors.As(err, &public) || public.Status != 409 || public.Code != "conflict" || public.Fields["revision"] != "Load the current revision." || errors.Is(err, ErrProtocol) {
					t.Fatal(mapped, err)
				}
			}
		})
	}
}

func TestBindAuthorizationStillPrecedesConflictHandler(t *testing.T) {
	contract := Must[input, output]("records/protected", "v1")
	router := actions.New(actions.Options{})
	handlers, mapped := 0, 0
	contract.Bind(router, func(*actions.Context, input) (Result[output], error) {
		handlers++
		return Result[output]{}, &actions.ConflictError{}
	}, BindOptions[input]{Authorize: func(*actions.Context) error { return actions.ErrForbidden }, Form: func(*actions.Context, url.Values) (input, error) {
		mapped++
		return input{}, nil
	}})
	req := httptest.NewRequest("POST", "http://example.test/"+contract.Key(), strings.NewReader("amount=1"))
	req.Header.Set("Origin", "http://example.test")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != 403 || handlers != 0 || mapped != 0 || !strings.Contains(w.Body.String(), `"code":"forbidden"`) {
		t.Fatal(w.Code, w.Body.String(), handlers, mapped)
	}
}
