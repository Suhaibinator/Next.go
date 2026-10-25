package nextgo

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/actions"
	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/cache/shared"
	"github.com/Suhaibinator/Next.go/draft"
	"github.com/Suhaibinator/Next.go/internal/navigation"
	"github.com/Suhaibinator/Next.go/mutation"
	"github.com/Suhaibinator/Next.go/ui"
)

type actionViewFailExpire struct {
	*shared.Memory
	calls atomic.Int64
}

func (b *actionViewFailExpire) Expire(context.Context, string, shared.Selector) (int, error) {
	b.calls.Add(1)
	return 0, errors.New("private remote authority diagnostic")
}
func TestActionViewAuditInvalidationFailureKeepsConfirmedDataAndSkipsView(t *testing.T) {
	memory, err := shared.NewMemory(8, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	backend := &actionViewFailExpire{Memory: memory}
	store := sharedPageStore(t, backend, "action-view-audit", "v1")
	local := cache.New(8)
	var effects, views atomic.Int64
	app := testApp(t, Config{BuildID: "action-view-shared", Cache: local, SharedCache: store, Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) { views.Add(1); return Page{Body: ui.Text("should not render")}, nil }}}, ActionRegistrars: []func(*actions.Router){actionViewTestRegistrar(func(*actions.Context) (actions.Result, error) {
		return actions.Result{Data: effects.Add(1), RevalidateTags: []string{"changed"}}, nil
	})}})
	defer app.Close(context.Background())
	response := actionViewTestServe(app, actionViewTestRequest(app, "/"))
	frames := actionViewTestFrames(t, response)
	if !strings.Contains(string(frames[0].Result), `"data":1`) || effects.Load() != 1 || views.Load() != 0 || backend.calls.Load() != 1 {
		t.Fatal("invalidation failure lost result, repeated mutation, or rendered stale UI", effects.Load(), views.Load(), backend.calls.Load(), response.Body.String())
	}
	last := frames[len(frames)-1]
	if last.Type != "view-error" || last.Error == nil || last.Error.Status != 503 || strings.Contains(response.Body.String(), "private remote") {
		t.Fatal("invalidation failure not safely separated", response.Body.String())
	}
}
func TestActionViewAuditTypedAuthorizationAndLegacyTransport(t *testing.T) {
	var effects, views atomic.Int64
	contract := mutation.Must[struct{}, int64]("view-audit", "v1")
	app := testApp(t, Config{BuildID: "action-view-authorization", Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) { views.Add(1); return Page{Body: ui.Text("fresh view")}, nil }}}, ActionRegistrars: []func(*actions.Router){func(router *actions.Router) {
		contract.Bind(router, func(*actions.Context, struct{}) (mutation.Result[int64], error) {
			return mutation.Result[int64]{Data: effects.Add(1)}, nil
		}, mutation.BindOptions[struct{}]{Authorize: func(c *actions.Context) error {
			if c.Request.Header.Get("X-Allow") != "yes" {
				return actions.ErrForbidden
			}
			return nil
		}})
	}}})
	defer app.Close(context.Background())
	denied := actionViewTestRequest(app, "/")
	denied.Body = http.NoBody
	denied.ContentLength = 0
	response := actionViewTestServe(app, denied)
	if response.Code != 403 || effects.Load() != 0 || views.Load() != 0 {
		t.Fatal("authorization happened after mutation/view", response.Code, response.Body.String())
	}
	legacy := actionViewTestRequest(app, "/")
	legacy.Header.Del("X-Nextgo-Action-View")
	legacy.Header.Set("Accept", "application/json")
	legacy.Header.Set("X-Allow", "yes")
	response = actionViewTestServe(app, legacy)
	var data struct {
		Data int64 `json:"data"`
	}
	if response.Code != 200 || json.Unmarshal(response.Body.Bytes(), &data) != nil || data.Data != 1 || views.Load() != 0 || strings.Contains(response.Body.String(), `"type":"result"`) {
		t.Fatal("legacy typed transport changed", response.Code, response.Body.String(), views.Load())
	}
}
func TestActionViewAuditOpaqueHandlerRejectsCapabilityBeforeExecution(t *testing.T) {
	var calls atomic.Int64
	app := testApp(t, Config{BuildID: "action-view-opaque", Actions: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.Write([]byte(`{"data":"opaque"}`)) }), Routes: []Route{{Pattern: "/", Page: static("home")}}})
	defer app.Close(context.Background())
	response := actionViewTestServe(app, actionViewTestRequest(app, "/"))
	if response.Code < 400 || calls.Load() != 0 {
		t.Fatal("unsupported capability executed opaque mutation", response.Code, calls.Load(), response.Body.String())
	}
	legacy := actionViewTestRequest(app, "/")
	legacy.Header.Del("X-Nextgo-Action-View")
	response = actionViewTestServe(app, legacy)
	if response.Code != 200 || calls.Load() != 1 {
		t.Fatal("unsupported action-view changed legacy opaque route", response.Code, calls.Load())
	}
}
func TestActionViewAuditRedirectAndResponseControlsAreStaged(t *testing.T) {
	var effects atomic.Int64
	app := testApp(t, Config{BuildID: "action-view-redirect", BasePath: "/docs", Routes: []Route{{Pattern: "/source", Page: static("old")}, {Pattern: "/target", Page: func(c *Context) (Page, error) {
		if err := c.SetCookie(&http.Cookie{Name: "action_view", Value: "staged", Path: "/docs", HttpOnly: true, SameSite: http.SameSiteLaxMode}); err != nil {
			return Page{}, err
		}
		if err := c.SetHeader("X-View-Control", "prepared"); err != nil {
			return Page{}, err
		}
		return Page{Title: "Destination", Body: ui.Text(c.Query.Get("q"))}, nil
	}}}, ActionRegistrars: []func(*actions.Router){actionViewTestRegistrar(func(*actions.Context) (actions.Result, error) {
		effects.Add(1)
		return actions.Result{Data: "saved", Redirect: "/target?q=fresh#saved"}, nil
	})}})
	defer app.Close(context.Background())
	response := actionViewTestServe(app, actionViewTestRequest(app, "/docs/source?q=old"))
	frames := actionViewTestFrames(t, response)
	page := actionViewTestPage(t, frames)
	var result actions.Response
	if err := json.Unmarshal(frames[0].Result, &result); err != nil {
		t.Fatal(err)
	}
	if result.Redirect != "/docs/target?q=fresh#saved" || page.URL != "/docs/target?q=fresh" || page.HTML != "fresh" || page.Title != "Destination" || effects.Load() != 1 {
		t.Fatal("redirect target metadata/data incorrect", result, page, effects.Load())
	}
	if response.Result().Header.Get("X-View-Control") != "prepared" || len(response.Result().Cookies()) != 1 || !response.Result().Cookies()[0].HttpOnly {
		t.Fatal("view response controls committed too late", response.Header())
	}
}
func TestActionViewAuditLaterViewFailureCannotEraseOrRepeatMutation(t *testing.T) {
	for _, kind := range []string{"panic", "deadline"} {
		t.Run(kind, func(t *testing.T) {
			var effects atomic.Int64
			app := testApp(t, Config{BuildID: "action-view-failure", Streaming: StreamingOptions{Timeout: 20 * time.Millisecond}, Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) {
				if kind == "panic" {
					panic("private page diagnostic")
				}
				return Page{Stream: true, Body: ui.Defer("timeout", ui.Text("pending"), func(ctx context.Context) (ui.Node, error) { <-ctx.Done(); return nil, ctx.Err() })}, nil
			}}}, ActionRegistrars: []func(*actions.Router){actionViewTestRegistrar(func(*actions.Context) (actions.Result, error) { return actions.Result{Data: effects.Add(1)}, nil })}})
			defer app.Close(context.Background())
			response := actionViewTestServe(app, actionViewTestRequest(app, "/"))
			frames := actionViewTestFrames(t, response)
			if !strings.Contains(string(frames[0].Result), `"data":1`) || effects.Load() != 1 || strings.Contains(response.Body.String(), "private page diagnostic") {
				t.Fatal("view failure affected confirmation or privacy", response.Body.String(), effects.Load())
			}
			failed := false
			for _, frame := range frames {
				if frame.Type == "view-error" || frame.Type == "chunk" && frame.Failed {
					failed = true
				}
				if frame.Type == "shell" {
					var page Envelope
					_ = json.Unmarshal(frame.Page, &page)
					if page.Status >= 500 {
						failed = true
					}
				}
			}
			if !failed {
				t.Fatal("failed view reported success", response.Body.String())
			}
		})
	}
}
func TestActionViewAuditOversizedTypedResultNeverStartsView(t *testing.T) {
	var effects, views atomic.Int64
	app := testApp(t, Config{BuildID: "action-view-bounds", Routes: []Route{{Pattern: "/", Page: func(*Context) (Page, error) { views.Add(1); return Page{Body: ui.Text("view")}, nil }}}, ActionRegistrars: []func(*actions.Router){actionViewTestRegistrar(func(*actions.Context) (actions.Result, error) {
		effects.Add(1)
		return actions.Result{Data: strings.Repeat("x", 64<<10)}, nil
	})}})
	defer app.Close(context.Background())
	response := actionViewTestServe(app, actionViewTestRequest(app, "/"))
	if response.Code < 400 || effects.Load() != 1 || views.Load() != 0 || strings.Contains(response.Body.String(), `"type":"result"`) {
		t.Fatal("oversize typed result admitted or view started", response.Code, effects.Load(), views.Load(), response.Body.Len())
	}
}

func TestActionViewAuditPageAuthorizationUsesPostMutationState(t *testing.T) {
	var effects atomic.Int64
	var allowed atomic.Bool
	allowed.Store(true)
	app := testApp(t, Config{BuildID: "action-view-view-auth", Routes: []Route{{Pattern: "/", Page: func(c *Context) (Page, error) {
		if c.Request.Header.Get("Authorization") != "Bearer current" || !allowed.Load() {
			return Page{}, ErrForbidden
		}
		return Page{Body: ui.Text("private previous UI")}, nil
	}}}, ActionRegistrars: []func(*actions.Router){actionViewTestRegistrar(func(c *actions.Context) (actions.Result, error) {
		if c.Request.Header.Get("Authorization") != "Bearer current" {
			return actions.Result{}, actions.ErrUnauthorized
		}
		effects.Add(1)
		allowed.Store(false)
		return actions.Result{Data: "confirmed revoke"}, nil
	})}})
	defer app.Close(context.Background())
	r := actionViewTestRequest(app, "/")
	r.Header.Set("Authorization", "Bearer current")
	response := actionViewTestServe(app, r)
	frames := actionViewTestFrames(t, response)
	page := actionViewTestPage(t, frames)
	if !strings.Contains(string(frames[0].Result), "confirmed revoke") || effects.Load() != 1 || page.Status != 403 || strings.Contains(page.HTML, "private previous UI") {
		t.Fatal("view authorization reused pre-mutation state", effects.Load(), response.Body.String())
	}
}

func TestActionViewAuditInternalRewritePreservesSourceURLAndQuery(t *testing.T) {
	app := testApp(t, Config{BuildID: "action-view-rewrite", Rewrites: []RewriteRule{{Source: "/alias", Destination: "/actual"}}, Routes: []Route{{Pattern: "/actual", Page: func(c *Context) (Page, error) {
		return Page{Body: ui.Text(c.Request.URL.Path + ":" + c.Query.Get("q"))}, nil
	}}}, ActionRegistrars: []func(*actions.Router){actionViewTestRegistrar(func(*actions.Context) (actions.Result, error) { return actions.Result{Data: "confirmed"}, nil })}})
	defer app.Close(context.Background())
	response := actionViewTestServe(app, actionViewTestRequest(app, "/alias?q=fresh"))
	page := actionViewTestPage(t, actionViewTestFrames(t, response))
	if page.URL != "/alias?q=fresh" || page.HTML != "/actual:fresh" {
		t.Fatal("rewrite changed public source identity or query", page, response.Body.String())
	}
}

func TestActionViewAuditRedirectCookieChainIsAppliedBeforeFinalGET(t *testing.T) {
	var effects, steps, finals atomic.Int64
	app := testApp(t, Config{BuildID: "action-view-cookie-chain", BasePath: "/docs", Middleware: []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				http.SetCookie(w, &http.Cookie{Name: "outer_cookie", Value: "posted", Path: "/docs", HttpOnly: true})
			}
			next.ServeHTTP(w, r)
		})
	}}, Routes: []Route{
		{Pattern: "/source", Page: static("source")},
		{Pattern: "/step", Page: func(c *Context) (Page, error) {
			steps.Add(1)
			if original, err := c.Request.Cookie("original_cookie"); err != nil || original.Value != "kept" {
				return Page{}, errors.New("original credential missing")
			}
			if outer, err := c.Request.Cookie("outer_cookie"); err != nil || outer.Value != "posted" {
				return Page{}, errors.New("outer action cookie missing")
			}
			if err := c.SetCookie(&http.Cookie{Name: "chain_cookie", Value: "step", Path: "/docs", HttpOnly: true, SameSite: http.SameSiteLaxMode}); err != nil {
				return Page{}, err
			}
			return Page{}, c.Redirect("/final?x=1", http.StatusSeeOther)
		}},
		{Pattern: "/final", Page: func(c *Context) (Page, error) {
			finals.Add(1)
			original, err := c.Request.Cookie("original_cookie")
			if err != nil {
				return Page{}, err
			}
			outer, err := c.Request.Cookie("outer_cookie")
			if err != nil {
				return Page{}, err
			}
			chain, err := c.Request.Cookie("chain_cookie")
			if err != nil {
				return Page{}, err
			}
			return Page{Body: ui.Text(original.Value + ":" + outer.Value + ":" + chain.Value + ":" + c.Query.Get("x"))}, nil
		}},
	}, ActionRegistrars: []func(*actions.Router){actionViewTestRegistrar(func(*actions.Context) (actions.Result, error) {
		effects.Add(1)
		return actions.Result{Data: "confirmed", Redirect: "/step"}, nil
	})}})
	defer app.Close(context.Background())
	r := actionViewTestRequest(app, "/docs/source")
	r.AddCookie(&http.Cookie{Name: "original_cookie", Value: "kept"})
	response := actionViewTestServe(app, r)
	frames := actionViewTestFrames(t, response)
	page := actionViewTestPage(t, frames)
	results := 0
	for _, frame := range frames {
		if frame.Type == "result" {
			results++
		}
	}
	if results != 1 || effects.Load() != 1 || steps.Load() != 1 || finals.Load() != 1 || page.URL != "/docs/final?x=1" || page.HTML != "kept:posted:step:1" {
		t.Fatal("redirect cookie chain lost current GET credentials or repeated action", results, effects.Load(), steps.Load(), finals.Load(), page, response.Body.String())
	}
	cookies := map[string]*http.Cookie{}
	for _, cookie := range response.Result().Cookies() {
		cookies[cookie.Name] = cookie
	}
	if cookies["outer_cookie"] == nil || cookies["chain_cookie"] == nil || cookies["chain_cookie"].Value != "step" || !cookies["chain_cookie"].HttpOnly || cookies["chain_cookie"].Path != "/docs" {
		t.Fatal("redirect cookies missing from actual combined response headers", response.Result().Header)
	}
}

func TestActionViewAuditOuterCookieDefaultPathUsesActionEndpoint(t *testing.T) {
	app := testApp(t, Config{BuildID: "action-view-cookie-default", BasePath: "/docs", Middleware: []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				http.SetCookie(w, &http.Cookie{Name: "action_path_only", Value: "scoped"})
			}
			next.ServeHTTP(w, r)
		})
	}}, Routes: []Route{{Pattern: "/source", Page: static("source")}, {Pattern: "/final", Page: func(c *Context) (Page, error) {
		if _, err := c.Request.Cookie("action_path_only"); !errors.Is(err, http.ErrNoCookie) {
			return Page{}, errors.New("action endpoint default cookie path leaked into page")
		}
		return Page{Body: ui.Text("cookie absent")}, nil
	}}}, ActionRegistrars: []func(*actions.Router){actionViewTestRegistrar(func(*actions.Context) (actions.Result, error) {
		return actions.Result{Data: "confirmed", Redirect: "/final"}, nil
	})}})
	defer app.Close(context.Background())
	response := actionViewTestServe(app, actionViewTestRequest(app, "/docs/source"))
	page := actionViewTestPage(t, actionViewTestFrames(t, response))
	if page.HTML != "cookie absent" || page.URL != "/docs/final" {
		t.Fatal("outer cookie defaulted against source page instead of action endpoint", page, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "action_path_only" || cookies[0].Path != "" {
		t.Fatal("combined response changed original default-path cookie", response.Result().Header)
	}
}

func TestActionViewAuditDuplicateOriginalCookiePreservesFirstCredential(t *testing.T) {
	var effects atomic.Int64
	app := testApp(t, Config{BuildID: "action-view-cookie-duplicate", Routes: []Route{{Pattern: "/", Page: func(c *Context) (Page, error) {
		cookie, err := c.Request.Cookie("credential")
		if err != nil {
			return Page{}, err
		}
		return Page{Body: ui.Text(cookie.Value)}, nil
	}}}, ActionRegistrars: []func(*actions.Router){actionViewTestRegistrar(func(c *actions.Context) (actions.Result, error) {
		cookie, err := c.Request.Cookie("credential")
		if err != nil {
			return actions.Result{}, err
		}
		effects.Add(1)
		return actions.Result{Data: cookie.Value}, nil
	})}})
	defer app.Close(context.Background())
	r := actionViewTestRequest(app, "/")
	r.Header.Set("Cookie", "credential=first; credential=second")
	response := actionViewTestServe(app, r)
	frames := actionViewTestFrames(t, response)
	page := actionViewTestPage(t, frames)
	if effects.Load() != 1 || !strings.Contains(string(frames[0].Result), `"data":"first"`) || page.HTML != "first" {
		t.Fatal("request-local jar changed first credential selection", effects.Load(), response.Body.String())
	}
}

func TestActionViewAuditManagedDraftToggleStopsBeforeFreshMiddleware(t *testing.T) {
	for _, enable := range []bool{false, true} {
		t.Run(fmt.Sprint("enable=", enable), func(t *testing.T) {
			manager, err := draft.New(draft.Options{Keys: map[string][]byte{"a": bytes.Repeat([]byte{7}, 32)}, ActiveKey: "a", Scope: "action-view-draft", Path: "/docs", AllowInsecure: true})
			if err != nil {
				t.Fatal(err)
			}
			var effects, middlewareGETs, loads atomic.Int64
			app := testApp(t, Config{BuildID: "action-view-draft", BasePath: "/docs", Draft: manager, Middleware: []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method == http.MethodGet {
						middlewareGETs.Add(1)
					}
					if r.Method == http.MethodPost {
						cookie := manager.ClearCookie()
						if enable {
							var err error
							cookie, err = manager.Cookie(r)
							if err != nil {
								t.Error(err)
								http.Error(w, "cookie error", 500)
								return
							}
						}
						http.SetCookie(w, cookie)
					}
					next.ServeHTTP(w, r)
				})
			}}, Routes: []Route{{Pattern: "/source", Page: func(c *Context) (Page, error) {
				loads.Add(1)
				return Page{Body: ui.Text(fmt.Sprint(draft.Enabled(c.Context)))}, nil
			}}}, ActionRegistrars: []func(*actions.Router){actionViewTestRegistrar(func(*actions.Context) (actions.Result, error) {
				effects.Add(1)
				return actions.Result{Data: "confirmed"}, nil
			})}})
			defer app.Close(context.Background())
			r := actionViewTestRequest(app, "/docs/source")
			r.Header.Set(navigation.DraftHeader, "0")
			if !enable {
				cookie, err := manager.Cookie(r)
				if err != nil {
					t.Fatal(err)
				}
				r.AddCookie(cookie)
				r.Header.Set(navigation.DraftHeader, "1")
			}
			response := actionViewTestServe(app, r)
			frames := actionViewTestFrames(t, response)
			last := frames[len(frames)-1]
			if !strings.Contains(string(frames[0].Result), `"data":"confirmed"`) || effects.Load() != 1 || middlewareGETs.Load() != 0 || loads.Load() != 0 || last.Type != "view-error" || last.Error == nil || last.Error.Status != 409 {
				t.Fatal("managed draft change reused stale verified state", effects.Load(), middlewareGETs.Load(), loads.Load(), response.Body.String())
			}
			if len(response.Result().Cookies()) != 1 {
				t.Fatal("draft transition cookie missing from combined response", response.Result().Header)
			}
		})
	}
}

func TestActionViewAuditAmbiguousDraftCookiesRemainUnverified(t *testing.T) {
	manager, err := draft.New(draft.Options{Keys: map[string][]byte{"a": bytes.Repeat([]byte{9}, 32)}, ActiveKey: "a", Scope: "action-view-draft-ambiguous", Path: "/docs", AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	app := testApp(t, Config{BuildID: "action-view-draft-ambiguous", BasePath: "/docs", Draft: manager, Routes: []Route{{Pattern: "/source", Page: func(c *Context) (Page, error) { return Page{Body: ui.Text(fmt.Sprint(draft.Enabled(c.Context)))}, nil }}}, ActionRegistrars: []func(*actions.Router){actionViewTestRegistrar(func(*actions.Context) (actions.Result, error) { return actions.Result{Data: "confirmed"}, nil })}})
	defer app.Close(context.Background())
	r := actionViewTestRequest(app, "/docs/source")
	cookie, err := manager.Cookie(r)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Cookie", cookie.Name+"="+cookie.Value+"; "+cookie.Name+"=invalid")
	r.Header.Set(navigation.DraftHeader, "0")
	response := actionViewTestServe(app, r)
	page := actionViewTestPage(t, actionViewTestFrames(t, response))
	if page.HTML != "false" || response.Header().Get(navigation.DraftHeader) != "0" {
		t.Fatal("cookie reconstruction upgraded ambiguous preview credentials", page, response.Body.String())
	}
}

func TestActionViewAuditPageCookiePathsPreserveEscapedScopeOnEveryResponseKind(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, pathMode := range []string{"explicit", "default", "invalid"} {
			t.Run(fmt.Sprintf("stream=%v/%s", stream, pathMode), func(t *testing.T) {
				var effects atomic.Int64
				app := testApp(t, Config{BuildID: "action-view-cookie-escaped", BasePath: "/docs", Routes: []Route{
					{Pattern: "/:section/source", Page: static("source")},
					{Pattern: "/:section/step", Page: func(c *Context) (Page, error) {
						path := ""
						if pathMode == "explicit" {
							path = "/docs/a%20b"
						} else if pathMode == "invalid" {
							path = "relative"
						}
						if err := c.SetCookie(&http.Cookie{Name: "step_scope", Value: "scoped", Path: path, HttpOnly: true}); err != nil {
							return Page{}, err
						}
						return Page{}, c.Redirect("/a%20b/final?x=1", http.StatusSeeOther)
					}},
					{Pattern: "/:section/final", Page: func(c *Context) (Page, error) {
						cookie, err := c.Request.Cookie("step_scope")
						if err != nil {
							return Page{}, err
						}
						if err := c.SetCookie(&http.Cookie{Name: "final_scope", Value: "set-on-final", HttpOnly: true}); err != nil {
							return Page{}, err
						}
						body := ui.Text(cookie.Value + ":" + c.Params["section"] + ":" + c.Query.Get("x"))
						if stream {
							return Page{Stream: true, Body: ui.Fragment(body, ui.Defer("cookie-tail", ui.Text("pending"), func(context.Context) (ui.Node, error) { return ui.Text("ready"), nil }))}, nil
						}
						return Page{Body: body}, nil
					}},
				}, ActionRegistrars: []func(*actions.Router){actionViewTestRegistrar(func(*actions.Context) (actions.Result, error) {
					effects.Add(1)
					return actions.Result{Data: "confirmed", Redirect: "/a%20b/step"}, nil
				})}})
				defer app.Close(context.Background())
				response := actionViewTestServe(app, actionViewTestRequest(app, "/docs/a%20b/source"))
				frames := actionViewTestFrames(t, response)
				page := actionViewTestPage(t, frames)
				if effects.Load() != 1 || page.URL != "/docs/a%20b/final?x=1" || !strings.Contains(page.HTML, "scoped:a b:1") {
					t.Fatal("escaped cookie path failed within redirect/current view", effects.Load(), page, response.Body.String())
				}
				cookies := map[string]*http.Cookie{}
				for _, cookie := range response.Result().Cookies() {
					cookies[cookie.Name] = cookie
				}
				for _, name := range []string{"step_scope", "final_scope"} {
					cookie := cookies[name]
					if cookie == nil || cookie.Path != "/docs/a%20b" || !cookie.HttpOnly {
						t.Fatal("browser would store page cookie under action/default decoded path", name, cookie, response.Result().Header)
					}
				}
			})
		}
	}
}

func TestActionViewAuditManagedDraftRenewalKeepsOneVerifiedCredential(t *testing.T) {
	manager, err := draft.New(draft.Options{Keys: map[string][]byte{"a": bytes.Repeat([]byte{3}, 32)}, ActiveKey: "a", Scope: "action-view-draft-renew", Path: "/docs", AllowInsecure: true})
	if err != nil {
		t.Fatal(err)
	}
	app := testApp(t, Config{BuildID: "action-view-draft-renew", BasePath: "/docs", Draft: manager, Middleware: []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodPost {
				cookie, err := manager.Cookie(r)
				if err != nil {
					t.Error(err)
					http.Error(w, "cookie error", 500)
					return
				}
				http.SetCookie(w, cookie)
			}
			next.ServeHTTP(w, r)
		})
	}}, Routes: []Route{{Pattern: "/source", Page: func(c *Context) (Page, error) { return Page{Body: ui.Text(fmt.Sprint(draft.Enabled(c.Context)))}, nil }}}, ActionRegistrars: []func(*actions.Router){actionViewTestRegistrar(func(*actions.Context) (actions.Result, error) { return actions.Result{Data: "confirmed"}, nil })}})
	defer app.Close(context.Background())
	r := actionViewTestRequest(app, "/docs/source")
	cookie, err := manager.Cookie(r)
	if err != nil {
		t.Fatal(err)
	}
	r.AddCookie(cookie)
	r.Header.Set(navigation.DraftHeader, "1")
	response := actionViewTestServe(app, r)
	page := actionViewTestPage(t, actionViewTestFrames(t, response))
	if page.HTML != "true" || response.Header().Get(navigation.DraftHeader) != "1" || len(response.Result().Cookies()) != 1 {
		t.Fatal("renewing verified preview created ambiguous/stale credential", page, response.Body.String(), response.Result().Header)
	}
}

func TestActionViewAuditUnrepresentableDefaultCookiePathFailsSafely(t *testing.T) {
	for _, mode := range []string{"redirect", "buffered", "streamed", "explicit-valid"} {
		t.Run(mode, func(t *testing.T) {
			var effects, finals atomic.Int64
			app := testApp(t, Config{BuildID: "action-view-cookie-semicolon", BasePath: "/docs", Routes: []Route{
				{Pattern: "/source", Page: static("source")},
				{Pattern: "/:section/step", Page: func(c *Context) (Page, error) {
					if err := c.SetCookie(&http.Cookie{Name: "unsafe_default", Value: "must-not-broaden"}); err != nil {
						return Page{}, err
					}
					return Page{}, c.Redirect("/a;b/final", http.StatusSeeOther)
				}},
				{Pattern: "/:section/final", Page: func(c *Context) (Page, error) {
					finals.Add(1)
					path := ""
					if mode == "explicit-valid" {
						path = "/docs"
					}
					if err := c.SetCookie(&http.Cookie{Name: "unsafe_default", Value: "must-not-broaden", Path: path}); err != nil {
						return Page{}, err
					}
					body := ui.Text("must not adopt unsafe cookie UI")
					if mode == "streamed" {
						return Page{Stream: true, Body: ui.Defer("unsafe-cookie", body, func(context.Context) (ui.Node, error) { return ui.Text("loaded"), nil })}, nil
					}
					return Page{Body: body}, nil
				}},
			}, ActionRegistrars: []func(*actions.Router){actionViewTestRegistrar(func(*actions.Context) (actions.Result, error) {
				effects.Add(1)
				target := "/a;b/final"
				if mode == "redirect" {
					target = "/a;b/step"
				}
				return actions.Result{Data: "confirmed", Redirect: target}, nil
			})}})
			defer app.Close(context.Background())
			response := actionViewTestServe(app, actionViewTestRequest(app, "/docs/source"))
			frames := actionViewTestFrames(t, response)
			if effects.Load() != 1 || !strings.Contains(string(frames[0].Result), `"data":"confirmed"`) {
				t.Fatal("cookie-path failure lost confirmation or repeated mutation", effects.Load(), response.Body.String())
			}
			if mode == "explicit-valid" {
				page := actionViewTestPage(t, frames)
				cookies := response.Result().Cookies()
				if page.Status != 200 || len(cookies) != 1 || cookies[0].Path != "/docs" {
					t.Fatal("valid explicit cookie scope was rejected", page, response.Result().Header)
				}
				return
			}
			last := frames[len(frames)-1]
			if last.Type != "view-error" || last.Error == nil || last.Error.Status != 500 || len(response.Result().Cookies()) != 0 {
				t.Fatal("unrepresentable cookie default was forwarded with different browser scope", response.Body.String(), response.Result().Header)
			}
			for _, frame := range frames {
				if frame.Type == "shell" {
					t.Fatal("unsafe cookie view was committed", response.Body.String())
				}
			}
			if mode == "redirect" && finals.Load() != 0 {
				t.Fatal("redirect continued after unrepresentable cookie scope")
			}
		})
	}
}
