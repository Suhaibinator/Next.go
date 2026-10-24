package actions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
)

func request(method, path, body, contentType string) *http.Request {
	r := httptest.NewRequest(method, "https://example.test"+path, strings.NewReader(body))
	r.Header.Set("Origin", "https://example.test")
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	return r
}

func perform(router http.Handler, req *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestJSONAndInvalidation(t *testing.T) {
	var tags, paths []string
	r := New(Options{OnRevalidate: func(ts, ps []string) { tags, paths = ts, ps }})
	r.Handle("profile/save", func(c *Context) (Result, error) {
		var input struct {
			Name string `json:"name"`
		}
		if err := c.DecodeJSON(&input); err != nil {
			return Result{}, err
		}
		if input.Name != "Ada" {
			t.Fatalf("input = %#v", input)
		}
		return Result{Data: map[string]string{"name": input.Name}, Redirect: "/profile?saved=1", RevalidateTags: []string{"profile"}, RevalidatePaths: []string{"/profile"}}, nil
	})
	w := perform(r, request("POST", "/profile/save", `{"name":"Ada"}`, "application/json"))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	var response Response
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Redirect != "/profile?saved=1" || response.Data.(map[string]any)["name"] != "Ada" || len(tags) != 1 || len(paths) != 1 {
		t.Fatalf("%#v tags=%v paths=%v", response, tags, paths)
	}
	if w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("mutations must not be cached")
	}
}

func TestProgressiveForm(t *testing.T) {
	r := New(Options{})
	r.Handle("save", func(c *Context) (Result, error) {
		values, err := c.ParseForm()
		if err != nil {
			return Result{}, err
		}
		if values.Get("name") != "Ada" {
			t.Fatalf("form: %v", values)
		}
		if values.Has("queryOnly") {
			t.Fatal("query parameters merged into body")
		}
		return Result{Redirect: "/done?ok=1#saved"}, nil
	})
	w := perform(r, request("POST", "/save?queryOnly=bad", "name=Ada", "application/x-www-form-urlencoded"))
	if w.Code != 303 || w.Header().Get("Location") != "/done?ok=1#saved" {
		t.Fatalf("%d %v", w.Code, w.Header())
	}
	r.Handle("no-redirect", func(c *Context) (Result, error) { return Result{}, nil })
	w = perform(r, request("POST", "/no-redirect", "", ""))
	if w.Code != 200 || !strings.Contains(w.Body.String(), "Your changes were saved.") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

func TestMultipartForm(t *testing.T) {
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("name", "Ada"); err != nil {
		t.Fatal(err)
	}
	file, err := writer.CreateFormFile("attachment", "hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = file.Write([]byte("hello"))
	_ = writer.Close()
	r := New(Options{})
	r.Handle("save", func(c *Context) (Result, error) {
		form, err := c.ParseForm()
		if err != nil {
			return Result{}, err
		}
		if form.Get("name") != "Ada" {
			t.Fatal(form)
		}
		file, _, err := c.Request.FormFile("attachment")
		if err != nil {
			return Result{}, err
		}
		defer file.Close()
		value, err := io.ReadAll(file)
		if string(value) != "hello" {
			t.Fatal(string(value))
		}
		return Result{}, err
	})
	w := perform(r, request("POST", "/save", body.String(), writer.FormDataContentType()))
	if w.Code != 200 {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

func TestValidationEscapesHTML(t *testing.T) {
	r := New(Options{})
	r.Handle("save", func(*Context) (Result, error) {
		return Result{}, &ValidationError{Message: "<script>alert(1)</script>", Fields: map[string]string{"<name>": "<img src=x>"}}
	})
	w := perform(r, request("POST", "/save", "", ""))
	if w.Code != 422 || strings.Contains(w.Body.String(), "<script>") || strings.Contains(w.Body.String(), "<img") || !strings.Contains(w.Body.String(), "&lt;script&gt;") {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	req := request("POST", "/save", "", " ")
	req.Header.Set("Accept", "application/json")
	w = perform(r, req)
	var response Response
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if w.Code != 422 || response.Error.Code != "validation_error" || response.Error.Fields["<name>"] != "<img src=x>" {
		t.Fatal(w.Body.String())
	}
}

func TestMethodsAndMissingActions(t *testing.T) {
	r := New(Options{})
	for _, method := range []string{"GET", "PUT", "DELETE", "OPTIONS", "HEAD"} {
		w := perform(r, request(method, "/save", "", ""))
		if w.Code != 405 || w.Header().Get("Allow") != "POST" {
			t.Fatalf("%s: %d", method, w.Code)
		}
	}
	w := perform(r, request("POST", "/missing", "", ""))
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
}

func TestOriginProtection(t *testing.T) {
	tests := []struct {
		name, origin, referer, fetch, forwarded string
		allowed                                 []string
		want                                    int
	}{
		{name: "same-origin", origin: "https://example.test", want: 200},
		{name: "default-port", origin: "https://example.test:443", want: 200},
		{name: "cross-origin", origin: "https://evil.test", want: 403},
		{name: "same-host-wrong-scheme", origin: "http://example.test", want: 403},
		{name: "same-host-wrong-port", origin: "https://example.test:9443", want: 403},
		{name: "subdomain", origin: "https://sub.example.test", want: 403},
		{name: "null", origin: "null", want: 403},
		{name: "path", origin: "https://example.test/", want: 403},
		{name: "credentials", origin: "https://attacker@example.test", want: 403},
		{name: "cross-site-metadata", origin: "https://example.test", fetch: "cross-site", want: 403},
		{name: "explicit-origin", origin: "https://public.test", allowed: []string{"https://public.test"}, want: 200},
		{name: "explicit-origin-cross-site", origin: "https://public.test", fetch: "cross-site", allowed: []string{"https://public.test"}, want: 403},
		{name: "no-evidence", want: 403},
		{name: "same-origin-metadata", fetch: "same-origin", want: 200},
		{name: "same-site-not-origin", fetch: "same-site", want: 403},
		{name: "referer", referer: "https://example.test/settings?q=1", want: 200},
		{name: "bad-referer", referer: "https://evil.test/settings", want: 403},
		{name: "forwarded-not-trusted", origin: "https://public.test", forwarded: "public.test", want: 403},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := New(Options{AllowedOrigins: tt.allowed})
			r.Handle("save", func(*Context) (Result, error) { return Result{}, nil })
			req := request("POST", "/save", "", "")
			req.Header.Del("Origin")
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.referer != "" {
				req.Header.Set("Referer", tt.referer)
			}
			if tt.fetch != "" {
				req.Header.Set("Sec-Fetch-Site", tt.fetch)
			}
			if tt.forwarded != "" {
				req.Header.Set("X-Forwarded-Host", tt.forwarded)
			}
			w := perform(r, req)
			if w.Code != tt.want {
				t.Fatalf("got %d want %d: %s", w.Code, tt.want, w.Body.String())
			}
		})
	}
}

func TestBodyErrors(t *testing.T) {
	tests := []struct {
		name, body, kind string
		limit            int64
		chunked          bool
		want             int
	}{
		{"invalid-json", `{`, "application/json", 1024, false, 400},
		{"unknown-fields", `{"extra":true}`, "application/json", 1024, false, 400},
		{"extra-json", `{} {}`, "application/json", 1024, false, 400},
		{"empty-json", "", "application/json", 1024, false, 400},
		{"bad-json-type", `true`, "application/json", 1024, false, 400},
		{"wrong-media", `{}`, "text/plain", 1024, false, 415},
		{"large-known", `{"name":"` + strings.Repeat("a", 100) + `"}`, "application/json", 32, false, 413},
		{"large-chunked", `{"name":"` + strings.Repeat("a", 100) + `"}`, "application/json", 32, true, 413},
		{"large-form", "name=" + strings.Repeat("a", 100), "application/x-www-form-urlencoded", 32, true, 413},
		{"invalid-form", "name=%XX", "application/x-www-form-urlencoded", 1024, false, 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := New(Options{MaxBodyBytes: tt.limit})
			r.Handle("save", func(c *Context) (Result, error) {
				if tt.kind == "application/x-www-form-urlencoded" {
					_, err := c.ParseForm()
					return Result{}, err
				}
				var input struct {
					Name string `json:"name"`
				}
				err := c.DecodeJSON(&input)
				return Result{}, err
			})
			req := request("POST", "/save", tt.body, tt.kind)
			if tt.chunked {
				req.ContentLength = -1
			}
			w := perform(r, req)
			if w.Code != tt.want {
				t.Fatalf("got %d want %d: %s", w.Code, tt.want, w.Body.String())
			}
		})
	}
}

func TestRedirectSafety(t *testing.T) {
	for _, target := range []string{"https://evil.test", "//evil.test", "javascript:alert(1)", "/\\evil.test", "/%5cevil.test", "/%2f%2fevil.test", "/ok\r\nLocation: evil", "/ok%0aevil", "relative/path"} {
		t.Run(url.QueryEscape(target), func(t *testing.T) {
			logged := false
			invalidated := false
			r := New(Options{LogError: func(*http.Request, error) { logged = true }, OnRevalidate: func([]string, []string) { invalidated = true }})
			r.Handle("save", func(*Context) (Result, error) { return Result{Redirect: target, RevalidateTags: []string{"x"}}, nil })
			w := perform(r, request("POST", "/save", "", ""))
			if w.Code != 500 || !logged || invalidated || w.Header().Get("Location") != "" {
				t.Fatalf("%d %s logged=%v invalidated=%v", w.Code, w.Body.String(), logged, invalidated)
			}
		})
	}
}

func TestInternalErrorsAreGeneric(t *testing.T) {
	for _, kind := range []string{"error", "panic", "encoding"} {
		t.Run(kind, func(t *testing.T) {
			var logged error
			r := New(Options{LogError: func(_ *http.Request, err error) { logged = err }})
			r.Handle("save", func(*Context) (Result, error) {
				switch kind {
				case "error":
					return Result{}, errors.New("database password secret")
				case "panic":
					panic("database password secret")
				default:
					return Result{Data: make(chan int)}, nil
				}
			})
			w := perform(r, request("POST", "/save", "", "application/json"))
			if w.Code != 500 || logged == nil || strings.Contains(w.Body.String(), "secret") {
				t.Fatalf("%d %s logged=%v", w.Code, w.Body.String(), logged)
			}
		})
	}
}

func TestRequestCancellation(t *testing.T) {
	r := New(Options{})
	r.Handle("save", func(c *Context) (Result, error) {
		select {
		case <-c.Done():
			return Result{}, c.Err()
		default:
			t.Fatal("request cancellation not forwarded")
			return Result{}, nil
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	w := perform(r, request("POST", "/save", "", "").WithContext(ctx))
	if w.Code != 408 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestOversizedInputDoesNotExecuteHandler(t *testing.T) {
	for _, chunked := range []bool{false, true} {
		r := New(Options{MaxBodyBytes: 8})
		r.Handle("save", func(*Context) (Result, error) {
			t.Fatal("oversized input executed a mutation")
			return Result{}, nil
		})
		req := request("POST", "/save", strings.Repeat("x", 16), "text/plain")
		if chunked {
			req.ContentLength = -1
		}
		w := perform(r, req)
		if w.Code != 413 {
			t.Fatal(w.Code, w.Body.String())
		}
	}
}

func TestFailureDoesNotInvalidate(t *testing.T) {
	r := New(Options{OnRevalidate: func([]string, []string) { t.Fatal("failure invalidated cache") }})
	r.Handle("save", func(*Context) (Result, error) {
		return Result{RevalidateTags: []string{"x"}}, &ValidationError{Message: "Invalid"}
	})
	w := perform(r, request("POST", "/save", "", ""))
	if w.Code != 422 {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestDuplicateOriginRejected(t *testing.T) {
	r := New(Options{})
	r.Handle("save", func(*Context) (Result, error) { t.Fatal("ambiguous origin executed"); return Result{}, nil })
	req := request("POST", "/save", "", "")
	req.Header.Add("Origin", "https://evil.test")
	if w := perform(r, req); w.Code != 403 {
		t.Fatal(w.Code)
	}
}

func TestRegistrationConcurrency(t *testing.T) {
	r := New(Options{})
	r.Handle("save", func(*Context) (Result, error) { return Result{}, nil })
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w := perform(r, request("POST", "/save", "", ""))
			if w.Code != 200 {
				t.Errorf("%d", w.Code)
			}
		}()
	}
	r.Handle("other", func(*Context) (Result, error) { return Result{}, nil })
	wg.Wait()
	for _, name := range []string{"", "/save", "bad.name", "../save", "double//slash", "save"} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				if recover() == nil {
					t.Fatal("expected panic")
				}
			}()
			r.Handle(name, func(*Context) (Result, error) { return Result{}, nil })
		})
	}
}
