//go:build !js

package mutation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/Suhaibinator/Next.go/actions"
	"github.com/Suhaibinator/Next.go/internal/actionwire"
)

type input struct {
	Amount int `json:"amount"`
}
type output struct {
	Count int `json:"count"`
}

func TestBindTypedJSONProgressiveFormsAndVersionMissBeforeMutation(t *testing.T) {
	c := Must[input, output]("counter/add", "v1")
	count, revalidated := 0, 0
	router := actions.New(actions.Options{OnRevalidate: func(tags, paths []string) { revalidated++ }})
	c.Bind(router, func(ctx *actions.Context, in input) (Result[output], error) {
		if in.Amount < 1 {
			return Result[output]{}, &actions.ValidationError{Message: "Positive only", Fields: map[string]string{"amount": "Positive only"}}
		}
		count += in.Amount
		return Result[output]{Data: output{count}, Redirect: "/counter", RevalidatePaths: []string{"/counter"}}, nil
	}, BindOptions[input]{Form: func(ctx *actions.Context, values url.Values) (input, error) {
		if values.Get("amount") != "1" {
			return input{}, &actions.ValidationError{Message: "One only"}
		}
		return input{1}, nil
	}})
	for _, tc := range []struct {
		path, body, kind, revision, protocol string
		status, count                        int
	}{
		{c.Key(), `{"amount":2}`, "application/json", "v1", "1", 200, 2},
		{c.Key(), `{"amount":2,"extra":true}`, "application/json", "v1", "1", 400, 2},
		{c.Key(), `{"amount":0}`, "application/json", "v1", "1", 422, 2},
		{"counter/add/v0", `{"amount":10}`, "application/json", "v0", "1", 404, 2},
		{c.Key(), `{"amount":10}`, "application/json", "v0", "1", 409, 2},
		{c.Key(), `{"amount":10}`, "application/json", "v1", "2", 409, 2},
		{c.Key(), "amount=1", "application/x-www-form-urlencoded", "", "", 303, 3},
	} {
		req := httptest.NewRequest("POST", "http://example.test/"+tc.path, strings.NewReader(tc.body))
		req.Header.Set("Origin", "http://example.test")
		req.Header.Set("Content-Type", tc.kind)
		if tc.revision != "" {
			req.Header.Set(actionwire.RevisionHeader, tc.revision)
		}
		if tc.protocol != "" {
			req.Header.Set(actionwire.ProtocolHeader, tc.protocol)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != tc.status || count != tc.count || w.Header().Get(actionwire.ProtocolHeader) != "1" {
			t.Fatal(tc, w.Code, count, w.Body.String())
		}
	}
	if revalidated != 2 {
		t.Fatal("failed requests triggered effects", revalidated)
	}
}

type protectedInput struct{}

var protectedDecoded bool

func (*protectedInput) UnmarshalJSON([]byte) error { protectedDecoded = true; return nil }

func TestBindAuthorizationPrecedesDecoderAndInternalErrorsStayPrivate(t *testing.T) {
	router := actions.New(actions.Options{LogError: func(*http.Request, error) {}})
	c := Must[protectedInput, *output]("protected", "v1")
	handled := false
	protectedDecoded = false
	c.Bind(router, func(*actions.Context, protectedInput) (Result[*output], error) {
		handled = true
		return Result[*output]{}, nil
	}, BindOptions[protectedInput]{Authorize: func(*actions.Context) error { return actions.ErrUnauthorized }})
	req := httptest.NewRequest("POST", "http://example.test/"+c.Key(), strings.NewReader(`{}`))
	req.Header.Set("Origin", "http://example.test")
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != 401 || handled || protectedDecoded {
		t.Fatal(w.Code, handled, protectedDecoded)
	}
	bad := Must[input, chan int]("bad", "v1")
	bad.Bind(router, func(*actions.Context, input) (Result[chan int], error) {
		return Result[chan int]{Data: make(chan int)}, nil
	})
	req = httptest.NewRequest("POST", "http://example.test/"+bad.Key(), strings.NewReader(`{"amount":1}`))
	req.Header.Set("Origin", "http://example.test")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != 500 || strings.Contains(w.Body.String(), "chan int") {
		t.Fatal(w.Code, w.Body.String())
	}
	null := Must[input, *output]("null", "v1")
	null.Bind(router, func(*actions.Context, input) (Result[*output], error) { return Result[*output]{}, nil })
	req = httptest.NewRequest("POST", "http://example.test/"+null.Key(), strings.NewReader(`{"amount":1}`))
	req.Header.Set("Origin", "http://example.test")
	req.Header.Set("Content-Type", "application/json")
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	var value map[string]json.RawMessage
	json.Unmarshal(w.Body.Bytes(), &value)
	if w.Code != 200 || string(value["data"]) != "null" {
		t.Fatal(w.Code, w.Body.String())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req = req.WithContext(ctx)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != 408 {
		t.Fatal(w.Code, w.Body.String())
	}
}
