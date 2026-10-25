package actions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/actionview"
	"github.com/Suhaibinator/Next.go/internal/actionwire"
)

func TestServeWithViewExecutesExactlyOnceAndPreservesEffects(t *testing.T) {
	var calls, invalidations, renders int
	tags, paths := []string{"counter"}, []string{"/current"}
	router := New(Options{OnRevalidate: func(ts, ps []string) {
		invalidations++
		if calls != 1 || renders != 0 || ts[0] != "counter" || ps[0] != "/current" {
			t.Fatal("effect order/selectors", calls, renders, ts, ps)
		}
		ts[0] = "callback-mutated"
		ps[0] = "callback-mutated"
	}})
	router.HandleContract("add/v1", "v1", func(c *Context) (Result, error) {
		calls++
		var input struct {
			Amount int `json:"amount"`
		}
		if err := c.DecodeJSON(&input); err != nil {
			return Result{}, err
		}
		return Result{Data: map[string]int{"count": input.Amount + 1}, Redirect: "/current", RevalidateTags: tags, RevalidatePaths: paths}, nil
	})
	req := request("POST", "/add/v1", `{"amount":2}`, "application/json")
	req.Header.Set(actionwire.RevisionHeader, "v1")
	w := httptest.NewRecorder()
	router.ServeWithView(w, req, func(w http.ResponseWriter, got *http.Request, success ViewSuccess) {
		renders++
		if got == req || got.Context() != req.Context() || got.Header.Get("Accept") != "application/json" || calls != 1 || invalidations != 1 || success.Redirect != "/current" || success.RevalidateTags[0] != "counter" || success.RevalidatePaths[0] != "/current" {
			t.Fatal("wrong view inputs/order", success)
		}
		var response Response
		if err := json.Unmarshal(success.Encoded, &response); err != nil {
			t.Fatal(err)
		}
		if response.Data.(map[string]any)["count"] != float64(3) {
			t.Fatal(response)
		}
		success.RevalidateTags[0] = "view-mutated"
		success.RevalidatePaths[0] = "view-mutated"
		w.Header().Set("Content-Type", actionview.MediaType)
		_, _ = w.Write([]byte("view output"))
	})
	if w.Code != 200 || w.Body.String() != "view output" || calls != 1 || invalidations != 1 || renders != 1 || tags[0] != "counter" || paths[0] != "/current" {
		t.Fatal(w.Code, w.Body.String(), calls, invalidations, renders, tags, paths)
	}
	if w.Header().Get(actionwire.ProtocolHeader) != actionwire.Protocol || w.Header().Get(actionwire.RevisionHeader) != "v1" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal(w.Header())
	}
	if req.Header.Get("Accept") != "" {
		t.Fatal("view negotiation modified caller headers")
	}
}

func TestServeWithViewFormErrorsAlwaysUseJSONAndKeepLegacyHTML(t *testing.T) {
	for _, kind := range []string{"urlencoded", "multipart"} {
		for _, failure := range []string{"validation", "unauthorized", "forbidden", "internal"} {
			t.Run(kind+"/"+failure, func(t *testing.T) {
				body, contentType := "name=Ada", "application/x-www-form-urlencoded"
				if kind == "multipart" {
					var buffer bytes.Buffer
					writer := multipart.NewWriter(&buffer)
					if err := writer.WriteField("name", "Ada"); err != nil {
						t.Fatal(err)
					}
					if err := writer.Close(); err != nil {
						t.Fatal(err)
					}
					body, contentType = buffer.String(), writer.FormDataContentType()
				}
				mutations, effects, renders := 0, 0, 0
				router := New(Options{LogError: func(*http.Request, error) {}, OnRevalidate: func([]string, []string) { effects++ }})
				router.Handle("save", func(c *Context) (Result, error) {
					if failure == "unauthorized" {
						return Result{}, ErrUnauthorized
					}
					if failure == "forbidden" {
						return Result{}, ErrForbidden
					}
					if _, err := c.ParseForm(); err != nil {
						return Result{}, err
					}
					if failure == "validation" {
						return Result{}, &ValidationError{Message: "Check name", Fields: map[string]string{"name": "Name is invalid"}}
					}
					if failure == "internal" {
						return Result{}, errors.New("private storage detail")
					}
					mutations++
					return Result{Data: 1, RevalidateTags: []string{"counter"}}, nil
				})
				want := map[string]int{"validation": 422, "unauthorized": 401, "forbidden": 403, "internal": 500}[failure]
				req := request("POST", "/save", body, contentType)
				req.Header.Set("Accept", actionview.MediaType)
				w := httptest.NewRecorder()
				router.ServeWithView(w, req, func(http.ResponseWriter, *http.Request, ViewSuccess) { renders++ })
				var response Response
				if w.Code != want || !strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") || json.Unmarshal(w.Body.Bytes(), &response) != nil || response.Error == nil || strings.Contains(w.Body.String(), "private storage detail") {
					t.Fatal(w.Code, w.Header(), w.Body.String())
				}
				if req.Header.Get("Accept") != actionview.MediaType || mutations != 0 || effects != 0 || renders != 0 {
					t.Fatal("negotiation/effects changed", req.Header, mutations, effects, renders)
				}
				if (failure == "unauthorized" || failure == "forbidden") && w.Header().Get("Cache-Control") != "private, no-store" {
					t.Fatal(w.Header())
				}
				legacy := httptest.NewRecorder()
				legacyReq := request("POST", "/save", body, contentType)
				legacyReq.Header.Set("Accept", actionview.MediaType)
				router.ServeHTTP(legacy, legacyReq)
				if legacy.Code != want || !strings.HasPrefix(legacy.Header().Get("Content-Type"), "text/html") || !strings.Contains(legacy.Body.String(), "<!doctype html>") {
					t.Fatal("legacy form negotiation changed", legacy.Code, legacy.Header(), legacy.Body.String())
				}
			})
		}
	}
}

func TestServeWithViewErrorsDoNotRenderOrInvalidate(t *testing.T) {
	tests := []struct {
		name, method, origin, revision, body string
		limit                                int64
		fail                                 error
		want                                 int
	}{
		{name: "method", method: "GET", want: 405},
		{name: "origin", origin: "https://evil.test", want: 403},
		{name: "revision", revision: "v2", want: 409},
		{name: "request-limit", body: strings.Repeat("x", 33), limit: 32, want: 413},
		{name: "validation", fail: &ValidationError{Message: "Check input"}, want: 422},
		{name: "unauthorized-before-decode", body: "malformed-json", fail: ErrUnauthorized, want: 401},
		{name: "forbidden", fail: ErrForbidden, want: 403},
		{name: "internal", fail: errors.New("private database error"), want: 500},
		{name: "bad-json", body: "malformed-json", want: 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options := Options{MaxBodyBytes: tt.limit, LogError: func(*http.Request, error) {}, OnRevalidate: func([]string, []string) { t.Fatal("failed action invalidated") }}
			router := New(options)
			router.HandleContract("add/v1", "v1", func(c *Context) (Result, error) {
				if tt.fail != nil {
					return Result{}, tt.fail
				}
				var input struct{}
				if err := c.DecodeJSON(&input); err != nil {
					return Result{}, err
				}
				t.Fatal("unexpected successful mutation")
				return Result{}, nil
			})
			method := tt.method
			if method == "" {
				method = "POST"
			}
			body := tt.body
			if body == "" {
				body = "{}"
			}
			req := request(method, "/add/v1", body, "application/json")
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if tt.revision != "" {
				req.Header.Set(actionwire.RevisionHeader, tt.revision)
			}
			w := httptest.NewRecorder()
			router.ServeWithView(w, req, func(http.ResponseWriter, *http.Request, ViewSuccess) { t.Fatal("failed action rendered") })
			if w.Code != tt.want || strings.Contains(w.Body.String(), "private database") {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

func TestServeWithViewOversizedResultKeepsCommittedMutationEffects(t *testing.T) {
	for _, view := range []bool{false, true} {
		calls, effects := 0, 0
		router := New(Options{LogError: func(*http.Request, error) {}, OnRevalidate: func([]string, []string) { effects++ }})
		router.Handle("large", func(*Context) (Result, error) {
			calls++
			return Result{Data: strings.Repeat("a", actionview.MaxResult), RevalidateTags: []string{"counter"}}, nil
		})
		w := httptest.NewRecorder()
		req := request("POST", "/large", "{}", "application/json")
		if view {
			router.ServeWithView(w, req, func(http.ResponseWriter, *http.Request, ViewSuccess) { t.Fatal("oversized result rendered") })
			if w.Code != 500 || !strings.Contains(w.Body.String(), "Reconcile before retrying") {
				t.Fatal(w.Code, w.Body.String())
			}
		} else {
			router.ServeHTTP(w, req)
			if w.Code != 200 || len(w.Body.Bytes()) <= actionview.MaxResult {
				t.Fatal("ordinary action changed", w.Code)
			}
		}
		if calls != 1 || effects != 1 {
			t.Fatal("mutation effects changed", calls, effects)
		}
	}
}

func TestServeWithViewResultBoundaryAndEncodingFailures(t *testing.T) {
	router := New(Options{})
	router.Handle("exact", func(*Context) (Result, error) { return Result{Data: strings.Repeat("a", actionview.MaxResult-11)}, nil })
	w := httptest.NewRecorder()
	rendered := false
	router.ServeWithView(w, request("POST", "/exact", "{}", "application/json"), func(w http.ResponseWriter, _ *http.Request, result ViewSuccess) {
		rendered = true
		if len(result.Encoded) != actionview.MaxResult {
			t.Fatal(len(result.Encoded))
		}
		w.WriteHeader(204)
	})
	if w.Code != 204 || !rendered {
		t.Fatal(w.Code, rendered)
	}
	for _, kind := range []string{"encoding", "redirect"} {
		t.Run(kind, func(t *testing.T) {
			logs := 0
			router := New(Options{LogError: func(*http.Request, error) { logs++ }, OnRevalidate: func([]string, []string) { t.Fatal("invalid successful result invalidated") }})
			router.Handle("save", func(*Context) (Result, error) {
				result := Result{Data: 1, RevalidateTags: []string{"counter"}}
				if kind == "encoding" {
					result.Data = make(chan int)
				} else {
					result.Redirect = "https://evil.test/private"
				}
				return result, nil
			})
			w := httptest.NewRecorder()
			router.ServeWithView(w, request("POST", "/save", "{}", "application/json"), func(http.ResponseWriter, *http.Request, ViewSuccess) { t.Fatal("invalid successful result rendered") })
			if w.Code != 500 || logs != 1 || !strings.Contains(w.Body.String(), "Reconcile before retrying") || strings.Contains(w.Body.String(), "evil.test") {
				t.Fatal(w.Code, logs, w.Body.String())
			}
		})
	}
}

func TestServeWithViewRejectsAmbiguousRawResultBeforeRendering(t *testing.T) {
	for _, view := range []bool{false, true} {
		t.Run(map[bool]string{false: "legacy-permissive", true: "view-strict"}[view], func(t *testing.T) {
			calls, effects, logs := 0, 0, 0
			router := New(Options{OnRevalidate: func([]string, []string) { effects++ }, LogError: func(*http.Request, error) { logs++ }})
			router.Handle("raw", func(*Context) (Result, error) {
				calls++
				return Result{Data: json.RawMessage(`{"nested":{"count":1,"count":2}}`), RevalidateTags: []string{"counter"}}, nil
			})
			w := httptest.NewRecorder()
			req := request("POST", "/raw", "{}", "application/json")
			if view {
				router.ServeWithView(w, req, func(http.ResponseWriter, *http.Request, ViewSuccess) {
					t.Fatal("ambiguous successful result reached renderer")
				})
				if w.Code != 500 || logs != 1 || !strings.Contains(w.Body.String(), "Reconcile before retrying") || strings.Contains(w.Body.String(), "nested") {
					t.Fatal(w.Code, logs, w.Body.String())
				}
			} else {
				router.ServeHTTP(w, req)
				if w.Code != 200 || logs != 0 || !strings.Contains(w.Body.String(), `"count":1,"count":2`) {
					t.Fatal("ordinary action behavior changed", w.Code, logs, w.Body.String())
				}
			}
			if calls != 1 || effects != 1 {
				t.Fatal("committed mutation/effect count changed", calls, effects)
			}
		})
	}
}

func TestServeWithViewCallbackPanicDoesNotAppendAfterPartialView(t *testing.T) {
	for _, started := range []bool{false, true} {
		t.Run(map[bool]string{false: "before-output", true: "after-output"}[started], func(t *testing.T) {
			logs, calls := 0, 0
			router := New(Options{LogError: func(*http.Request, error) { logs++ }})
			router.Handle("save", func(*Context) (Result, error) { calls++; return Result{Data: 1}, nil })
			w := httptest.NewRecorder()
			router.ServeWithView(w, request("POST", "/save", "{}", "application/json"), func(w http.ResponseWriter, _ *http.Request, _ ViewSuccess) {
				if started {
					w.Header().Set("Content-Type", actionview.MediaType)
					_, _ = w.Write([]byte("partial-view\n"))
					w.(http.Flusher).Flush()
				}
				panic("private renderer detail")
			})
			if logs != 1 || calls != 1 || strings.Contains(w.Body.String(), "private renderer") {
				t.Fatal(logs, calls, w.Body.String())
			}
			if started {
				if w.Body.String() != "partial-view\n" || w.Code != 200 {
					t.Fatal("incompatible failure appended", w.Code, w.Body.String())
				}
			} else if w.Code != 500 || !strings.Contains(w.Body.String(), "Reconcile before retrying") {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

func TestServeWithViewPanicInvalidationNilRendererAndCancellation(t *testing.T) {
	for _, stage := range []string{"handler", "invalidation", "nil-renderer", "canceled"} {
		t.Run(stage, func(t *testing.T) {
			calls := 0
			router := New(Options{LogError: func(*http.Request, error) {}, OnRevalidate: func([]string, []string) {
				if stage == "invalidation" {
					panic("private callback detail")
				}
			}})
			router.Handle("save", func(c *Context) (Result, error) {
				calls++
				if stage == "handler" {
					panic("private handler detail")
				}
				if err := c.Err(); err != nil {
					return Result{}, err
				}
				return Result{Data: 1, RevalidateTags: []string{"counter"}}, nil
			})
			req := request("POST", "/save", "{}", "application/json")
			if stage == "canceled" {
				ctx, cancel := context.WithCancel(req.Context())
				cancel()
				req = req.WithContext(ctx)
			}
			w := httptest.NewRecorder()
			var render func(http.ResponseWriter, *http.Request, ViewSuccess)
			if stage != "nil-renderer" {
				render = func(http.ResponseWriter, *http.Request, ViewSuccess) { t.Fatal("unexpected renderer") }
			}
			router.ServeWithView(w, req, render)
			want := 500
			if stage == "canceled" {
				want = 408
			}
			if w.Code != want || strings.Contains(w.Body.String(), "private") {
				t.Fatal(w.Code, w.Body.String())
			}
			if stage == "nil-renderer" && calls != 0 {
				t.Fatal("nil renderer executed handler")
			}
		})
	}
}

func TestClonedRouterServeWithViewAndOrdinaryResultsRemainIndependent(t *testing.T) {
	router := New(Options{})
	router.Handle("save", func(*Context) (Result, error) { return Result{Data: "saved"}, nil })
	clone := router.Clone()
	clone.Handle("extra", func(*Context) (Result, error) { return Result{Data: 1}, nil })
	w := httptest.NewRecorder()
	clone.ServeWithView(w, request("POST", "/save", "{}", "application/json"), func(w http.ResponseWriter, _ *http.Request, success ViewSuccess) { _, _ = w.Write(success.Encoded) })
	want := httptest.NewRecorder()
	router.ServeHTTP(want, request("POST", "/save", "{}", "application/json"))
	if !bytes.Equal(bytes.TrimSpace(w.Body.Bytes()), bytes.TrimSpace(want.Body.Bytes())) {
		t.Fatal(w.Body.String(), want.Body.String())
	}
	w = httptest.NewRecorder()
	router.ServeWithView(w, request("POST", "/extra", "{}", "application/json"), func(http.ResponseWriter, *http.Request, ViewSuccess) { t.Fatal("clone registration leaked") })
	if w.Code != 404 {
		t.Fatal(w.Code)
	}
}
