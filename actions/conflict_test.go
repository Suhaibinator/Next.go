package actions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestConflictErrorPublicDefaultsAndWrappedJSON(t *testing.T) {
	for _, message := range []string{"", "This record changed."} {
		t.Run(message, func(t *testing.T) {
			conflict := &ConflictError{Message: message, Fields: map[string]string{"revision": "Fetch the current version."}}
			invalidations, logs := 0, 0
			router := New(Options{OnRevalidate: func([]string, []string) { invalidations++ }, LogError: func(*http.Request, error) { logs++ }})
			router.Handle("update", func(*Context) (Result, error) {
				return Result{Data: make(chan int), Redirect: "/saved", Cookies: []*http.Cookie{{Name: "session", Value: "private"}}, RevalidateTags: []string{"record"}, RevalidatePaths: []string{"/record"}}, fmt.Errorf("private repository details: %w", conflict)
			})
			w := perform(router, request("POST", "/update", `{}`, "application/json"))
			var result Response
			if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
				t.Fatal(err)
			}
			if w.Code != http.StatusConflict || result.Error == nil || result.Error.Code != "conflict" || result.Error.Message != conflict.Error() || result.Error.Fields["revision"] != conflict.Fields["revision"] {
				t.Fatal(w.Code, w.Body.String())
			}
			if result.Data != nil || result.Redirect != "" || invalidations != 0 || logs != 0 || len(w.Header().Values("Set-Cookie")) != 0 || w.Header().Get("Location") != "" || strings.Contains(w.Body.String(), "private") || w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("conflict published success or private details", w.Header(), w.Body.String(), invalidations, logs)
			}
		})
	}
}

func TestConflictErrorProgressiveFormsEscapePublicMessages(t *testing.T) {
	for _, multipartMode := range []bool{false, true} {
		t.Run(fmt.Sprint("multipart=", multipartMode), func(t *testing.T) {
			var body bytes.Buffer
			contentType := "application/x-www-form-urlencoded"
			if multipartMode {
				writer := multipart.NewWriter(&body)
				if err := writer.WriteField("revision", "7"); err != nil {
					t.Fatal(err)
				}
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
				contentType = writer.FormDataContentType()
			} else {
				body.WriteString(url.Values{"revision": {"7"}}.Encode())
			}
			invalidations, handled := 0, 0
			router := New(Options{OnRevalidate: func([]string, []string) { invalidations++ }})
			router.Handle("update", func(c *Context) (Result, error) {
				values, err := c.ParseForm()
				if err != nil {
					return Result{}, err
				}
				if len(values["revision"]) != 1 || values.Get("revision") != "7" {
					t.Fatal(values)
				}
				handled++
				return Result{Cookies: []*http.Cookie{{Name: "session", Value: "private"}}, RevalidateTags: []string{"record"}}, &ConflictError{Message: `<script>alert("message")</script>`, Fields: map[string]string{`<b>revision</b>`: `<img src=x onerror=alert(1)>`}}
			})
			w := perform(router, request("POST", "/update", body.String(), contentType))
			if w.Code != http.StatusConflict || handled != 1 || invalidations != 0 || len(w.Header().Values("Set-Cookie")) != 0 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/html") {
				t.Fatal(w.Code, w.Header(), w.Body.String(), handled, invalidations)
			}
			if strings.Contains(w.Body.String(), "<script>") || strings.Contains(w.Body.String(), "<img") || strings.Contains(w.Body.String(), "<b>revision</b>") || !strings.Contains(w.Body.String(), "&lt;script&gt;") || !strings.Contains(w.Body.String(), "&lt;b&gt;revision&lt;/b&gt;") {
				t.Fatal("public conflict text was not escaped", w.Body.String())
			}
		})
	}
}

func TestConflictErrorActionViewNeverRendersSuccess(t *testing.T) {
	invalidations, renders := 0, 0
	router := New(Options{OnRevalidate: func([]string, []string) { invalidations++ }})
	router.Handle("update", func(*Context) (Result, error) {
		return Result{Data: "not confirmed", Cookies: []*http.Cookie{{Name: "session", Value: "private"}}, RevalidatePaths: []string{"/record"}}, &ConflictError{}
	})
	req := request("POST", "/update", "revision=1", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/x-nextgo-action-view+json")
	w := httptest.NewRecorder()
	router.ServeWithView(w, req, func(http.ResponseWriter, *http.Request, ViewSuccess) { renders++ })
	if w.Code != 409 || w.Header().Get("Content-Type") != "application/json; charset=utf-8" || !strings.Contains(w.Body.String(), `"code":"conflict"`) || renders != 0 || invalidations != 0 || len(w.Header().Values("Set-Cookie")) != 0 {
		t.Fatal(w.Code, w.Header(), w.Body.String(), renders, invalidations)
	}
}

func TestEnteredHandlerCancellationDoesNotAdviseBlindRetry(t *testing.T) {
	for _, canceled := range []error{context.Canceled, context.DeadlineExceeded} {
		for _, jsonMode := range []bool{false, true} {
			t.Run(fmt.Sprint(canceled, "/json=", jsonMode), func(t *testing.T) {
				commits, invalidations := 0, 0
				router := New(Options{OnRevalidate: func([]string, []string) { invalidations++ }})
				router.Handle("update", func(*Context) (Result, error) {
					commits++ // Deliberately model a backing commit before cancellation is returned.
					return Result{Cookies: []*http.Cookie{{Name: "session", Value: "private"}}, RevalidateTags: []string{"record"}}, fmt.Errorf("private repository details: %w", canceled)
				})
				req := request("POST", "/update", "", "application/x-www-form-urlencoded")
				if jsonMode {
					req.Header.Set("Accept", "application/json")
				}
				w := perform(router, req)
				if w.Code != 408 || commits != 1 || invalidations != 0 || len(w.Header().Values("Set-Cookie")) != 0 || strings.Contains(w.Body.String(), "Please try again") || strings.Contains(w.Body.String(), "private repository") || !strings.Contains(w.Body.String(), "outcome could not be confirmed") || !strings.Contains(w.Body.String(), "Reconcile before retrying") {
					t.Fatal(w.Code, w.Body.String(), commits, invalidations)
				}
				if jsonMode && !strings.Contains(w.Body.String(), `"code":"request_canceled"`) {
					t.Fatal(w.Body.String())
				}
			})
		}
	}
}

func TestConflictErrorJoinedPrecedencePreservesAuthorizationPrivacy(t *testing.T) {
	for _, test := range []struct {
		name        string
		joined      error
		status      int
		code        string
		wantMessage string
	}{
		{"forbidden", ErrForbidden, http.StatusForbidden, "forbidden", "You do not have permission to perform this action."},
		{"unauthorized", ErrUnauthorized, http.StatusUnauthorized, "unauthorized", "Authentication is required to perform this action."},
		{"validation", &ValidationError{Message: "private validation message", Fields: map[string]string{"private": "private validation field"}}, http.StatusConflict, "conflict", "The record changed."},
	} {
		t.Run(test.name, func(t *testing.T) {
			router := New(Options{})
			router.Handle("update", func(*Context) (Result, error) {
				conflict := &ConflictError{Message: "The record changed.", Fields: map[string]string{"revision": "Refresh the revision."}}
				return Result{}, errors.Join(fmt.Errorf("private wrapper: %w", conflict), test.joined)
			})
			w := perform(router, request("POST", "/update", `{}`, "application/json"))
			var response Response
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal(err)
			}
			if w.Code != test.status || response.Error == nil || response.Error.Code != test.code || response.Error.Message != test.wantMessage || strings.Contains(w.Body.String(), "private") {
				t.Fatal(w.Code, w.Body.String())
			}
			if test.status == http.StatusConflict {
				// Conflict has intentional precedence over joined validation errors.
				if response.Error.Fields["revision"] != "Refresh the revision." || len(response.Error.Fields) != 1 {
					t.Fatal(response.Error.Fields)
				}
			} else if len(response.Error.Fields) != 0 || strings.Contains(w.Body.String(), "record changed") || strings.Contains(w.Body.String(), "Refresh the revision") {
				t.Fatal("authorization failure exposed conflict details", w.Body.String())
			}
		})
	}
}
