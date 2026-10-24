package nextgo

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/ui"
)

func TestContextResponseHeadersCookiesAndStatus(t *testing.T) {
	var saved *Context
	cookie := &http.Cookie{Name: "session", Value: "original", Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteLaxMode, Unparsed: []string{"original"}}
	a := testApp(t, Config{Headers: http.Header{"X-Replace": []string{"config"}, "X-Append": []string{"config"}}, Routes: []Route{{Pattern: "/", Page: func(c *Context) (Page, error) {
		saved = c
		for _, err := range []error{c.SetHeader("X-Replace", "page"), c.AddHeader("X-Append", "page"), c.SetCookie(cookie), c.SetStatus(http.StatusAccepted), c.SetHeader("Cache-Control", "public, max-age=100"), c.SetHeader("X-Nextgo-Prefetch", "public")} {
			if err != nil {
				return Page{}, err
			}
		}
		cookie.Value = "mutated"
		cookie.Path = "/mutated"
		cookie.Unparsed[0] = "mutated"
		return Page{Body: ui.Text("accepted")}, nil
	}}}})
	w, envelope := runtimeEnvelope(t, a, "/", nil)
	if w.Code != http.StatusAccepted || envelope.Status != http.StatusAccepted || envelope.HTML != "accepted" {
		t.Fatalf("status controls not applied: %d %+v", w.Code, envelope)
	}
	if w.Header().Get("X-Replace") != "page" || strings.Join(w.Header().Values("X-Append"), ",") != "config,page" {
		t.Fatalf("header replacement/append: %v", w.Header())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Value != "original" || cookies[0].Path != "/" || !cookies[0].HttpOnly || !cookies[0].Secure {
		t.Fatalf("cookie alias or missing flags: %+v", cookies)
	}
	if w.Header().Get("Cache-Control") != "private, no-store" || w.Header().Get("X-Nextgo-Prefetch") != "" || w.Header().Get("Age") != "" {
		t.Fatalf("cookie output marked cacheable: %v", w.Header())
	}
	if err := saved.SetHeader("X-Late", "late"); !errors.Is(err, ErrHeadersCommitted) {
		t.Fatalf("late header accepted: %v", err)
	}
	if w.Header().Get("X-Late") != "" {
		t.Fatal("late header changed response")
	}
}

func TestContextRedirectUsesHTTPAndSeals(t *testing.T) {
	for _, status := range []int{http.StatusSeeOther, http.StatusTemporaryRedirect} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var saved *Context
			a := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: func(c *Context) (Page, error) {
				saved = c
				if err := c.SetCookie(&http.Cookie{Name: "notice", Value: "saved"}); err != nil {
					return Page{}, err
				}
				return Page{}, c.Redirect("/target/a%3Fb?q=yes", status)
			}}, {Pattern: "/target/:slug", Page: static("target")}}})
			for _, nav := range []bool{false, true} {
				w := request(a, "/", nav)
				if w.Code != status || w.Header().Get("Location") != "/target/a%3Fb?q=yes" || !strings.Contains(w.Header().Get("Content-Type"), "text/html") || w.Header().Get("Cache-Control") != "private, no-store" {
					t.Fatalf("redirect was not ordinary HTTP: %d %v %s", w.Code, w.Header(), w.Body.String())
				}
			}
			if err := saved.Redirect("/other", 307); !errors.Is(err, ErrHeadersCommitted) {
				t.Fatal(err)
			}
		})
	}
}

func TestContextResponseValidation(t *testing.T) {
	var c Context
	for _, header := range [][2]string{
		{"", "value"}, {"Bad Name", "value"}, {"Bad:Name", "value"}, {"X-Name\r\nInjected", "value"}, {"X-Name", "value\r\nInjected: yes"}, {"X-Name", "nul\x00value"}, {"X-Name", "bad\x7fvalue"},
		{"Connection", "close"}, {"Keep-Alive", "timeout=5"}, {"TE", "trailers"}, {"Trailer", "Digest"}, {"Transfer-Encoding", "chunked"}, {"Upgrade", "h2c"}, {"Content-Length", "1"}, {"Proxy-Connection", "close"}, {"Proxy-Authenticate", "Basic"}, {"HTTP2-Settings", "x"},
	} {
		if err := c.SetHeader(header[0], header[1]); err == nil {
			t.Errorf("SetHeader accepted %q %q", header[0], header[1])
		}
		if err := c.AddHeader(header[0], header[1]); err == nil {
			t.Errorf("AddHeader accepted %q %q", header[0], header[1])
		}
	}
	for _, cookie := range []*http.Cookie{nil, {Name: "bad name", Value: "value"}, {Name: "good", Value: "bad\r\nvalue"}, {Name: "good", Value: "value", Path: "/bad\npath"}} {
		if err := c.SetCookie(cookie); err == nil {
			t.Errorf("accepted invalid cookie %+v", cookie)
		}
	}
	for _, status := range []int{0, 99, 100, 199, 600, 999} {
		if err := c.SetStatus(status); err == nil {
			t.Errorf("accepted status %d", status)
		}
	}
	for _, location := range []string{"relative", "//other.test/path", "https://other.test/path", "/\\other.test/path", "/ok\r\nLocation:bad"} {
		if err := c.Redirect(location, 307); err == nil {
			t.Errorf("accepted redirect %q", location)
		}
	}
	for _, status := range []int{200, 304, 401, 500} {
		if err := c.Redirect("/ok", status); err == nil {
			t.Errorf("accepted redirect status %d", status)
		}
	}
	if err := c.SetHeader("X-Valid!#$%&'*+-.^_`|~", "tab\tand UTF-8 日本語"); err != nil {
		t.Fatal(err)
	}
	if err := c.SetHeader("X-Valid", "ok"); err != nil {
		t.Fatal(err)
	}
	if err := c.AddHeader("X-Valid", "second"); err != nil {
		t.Fatal(err)
	}
	snapshot := c.response.seal()
	if strings.Join(snapshot.headers.Values("X-Valid"), ",") != "ok,second" {
		t.Fatal(snapshot.headers)
	}
	snapshot.headers.Set("X-Valid", "mutated snapshot")
	if got := c.response.seal().headers.Get("X-Valid"); got != "ok" {
		t.Fatalf("snapshot aliases response state: %q", got)
	}
}

func TestContextControlsApplyToNotFoundAndErrors(t *testing.T) {
	a := testApp(t, Config{NotFound: func(c *Context) (Page, error) {
		if err := c.SetHeader("X-Boundary", "missing"); err != nil {
			return Page{}, err
		}
		if err := c.SetCookie(&http.Cookie{Name: "missing", Value: "yes"}); err != nil {
			return Page{}, err
		}
		return Page{Body: ui.Text("custom missing")}, nil
	}, Routes: []Route{{Pattern: "/failure", Page: func(c *Context) (Page, error) {
		_ = c.SetStatus(202)
		_ = c.SetCookie(&http.Cookie{Name: "failed", Value: "yes"})
		return Page{}, errors.New("private error")
	}}}})
	w, envelope := runtimeEnvelope(t, a, "/unknown", nil)
	if w.Code != 404 || envelope.HTML != "custom missing" || w.Header().Get("X-Boundary") != "missing" || len(w.Result().Cookies()) != 1 || w.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("notfound controls ignored: %d %v %+v", w.Code, w.Header(), envelope)
	}
	failure, _ := runtimeEnvelope(t, a, "/failure", nil)
	if failure.Code != 500 || failure.Header().Get("Cache-Control") != "private, no-store" {
		t.Fatalf("failure status hidden or cookies public: %d %v", failure.Code, failure.Header())
	}
}

type responseNodeFunc func(io.Writer) error

func (f responseNodeFunc) Render(w io.Writer) error { return f(w) }

func TestResponseControlsSealedBeforeRenderingAndDeferredWork(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(fmt.Sprint(streaming), func(t *testing.T) {
			failures := make(chan error, 8)
			a := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: func(c *Context) (Page, error) {
				_ = c.SetCookie(&http.Cookie{Name: "before", Value: "yes"})
				body := responseNodeFunc(func(w io.Writer) error {
					failures <- c.SetHeader("X-Late-Render", "bad")
					_, err := io.WriteString(w, "body")
					return err
				})
				if streaming {
					return Page{Stream: true, Body: ui.Defer("content", ui.Text("loading"), func(context.Context) (ui.Node, error) {
						failures <- c.SetCookie(&http.Cookie{Name: "late", Value: "bad"})
						failures <- c.SetStatus(404)
						failures <- c.Redirect("/late", 307)
						return body, nil
					})}, nil
				}
				return Page{Body: body}, nil
			}}}})
			w := request(a, "/", false)
			wantErrors := 1
			if streaming {
				wantErrors = 4
			}
			for i := 0; i < wantErrors; i++ {
				select {
				case err := <-failures:
					if !errors.Is(err, ErrHeadersCommitted) {
						t.Errorf("late mutation error: %v", err)
					}
				default:
					t.Fatal("deferred work did not execute")
				}
			}
			if w.Code != 200 || w.Header().Get("X-Late-Render") != "" || w.Header().Get("Location") != "" || len(w.Result().Cookies()) != 1 || w.Header().Get("Cache-Control") != "private, no-store" {
				t.Fatalf("late work mutated response: %d %v", w.Code, w.Header())
			}
		})
	}
}

func TestPublicCachedResponseMutationRejectedEvenWhenIgnored(t *testing.T) {
	for _, mutation := range []string{"set", "add", "cookie", "status", "redirect", "invalid", "render"} {
		t.Run(mutation, func(t *testing.T) {
			var loads atomic.Int64
			a := testApp(t, Config{Routes: []Route{{Pattern: "/", Cache: &CachePolicy{TTL: time.Minute}, Page: func(c *Context) (Page, error) {
				loads.Add(1)
				var err error
				switch mutation {
				case "set":
					err = c.SetHeader("X-User", "private")
				case "add":
					err = c.AddHeader("X-User", "private")
				case "cookie":
					err = c.SetCookie(&http.Cookie{Name: "private", Value: "session"})
				case "status":
					err = c.SetStatus(202)
				case "redirect":
					err = c.Redirect("/private", 307)
				case "invalid":
					err = c.SetHeader("Content-Length", "bad")
				case "render":
					return Page{Body: responseNodeFunc(func(w io.Writer) error {
						_ = c.SetCookie(&http.Cookie{Name: "private", Value: "session"})
						_, err := io.WriteString(w, "private output")
						return err
					})}, nil
				}
				if !errors.Is(err, ErrPublicCacheResponse) {
					t.Errorf("mutation returned %v", err)
				}
				return Page{Body: ui.Text("private output")}, nil
			}}}})
			for i := 0; i < 2; i++ {
				w, _ := runtimeEnvelope(t, a, "/", nil)
				if w.Code != 500 || w.Header().Get("X-User") != "" || len(w.Result().Cookies()) != 0 || w.Header().Get("Location") != "" || w.Header().Get("X-Nextgo-Prefetch") != "" || strings.Contains(w.Body.String(), "private") {
					t.Fatalf("public cache leaked response mutation: %d %v %s", w.Code, w.Header(), w.Body.String())
				}
			}
			if loads.Load() != 2 {
				t.Fatalf("forbidden response was cached: %d loads", loads.Load())
			}
		})
	}
}

func TestPublicCachedResponseCannotLeakAcrossConcurrentWaiters(t *testing.T) {
	var loads atomic.Int64
	started, release := make(chan struct{}), make(chan struct{})
	var first sync.Once
	a := testApp(t, Config{Routes: []Route{{Pattern: "/", Cache: &CachePolicy{TTL: time.Minute}, Page: func(c *Context) (Page, error) {
		loads.Add(1)
		first.Do(func() { close(started) })
		<-release
		_ = c.SetCookie(&http.Cookie{Name: "session", Value: "first-request"})
		return Page{Body: ui.Text("private session")}, nil
	}}}})
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w, _ := runtimeEnvelope(t, a, "/", nil)
			if w.Code != 500 || len(w.Result().Cookies()) != 0 || strings.Contains(w.Body.String(), "private session") {
				t.Errorf("coalesced response leaked session: %d %v %s", w.Code, w.Header(), w.Body.String())
			}
		}()
	}
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("public loader did not begin")
	}
	close(release)
	wg.Wait()
	before := loads.Load()
	w, _ := runtimeEnvelope(t, a, "/", nil)
	if w.Code != 500 || loads.Load() != before+1 {
		t.Fatalf("rejected coalesced response was cached: %d %d->%d", w.Code, before, loads.Load())
	}
}

func TestResponseMutationConcurrentWithSeal(t *testing.T) {
	var c Context
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			for _, err := range []error{c.AddHeader("X-Concurrent", fmt.Sprint(i)), c.SetCookie(&http.Cookie{Name: fmt.Sprintf("cookie%d", i), Value: "ok"}), c.SetStatus(202)} {
				if err != nil && !errors.Is(err, ErrHeadersCommitted) {
					t.Errorf("unexpected concurrent mutation error: %v", err)
				}
			}
		}(i)
	}
	close(start)
	c.response.seal()
	wg.Wait()
	for _, err := range []error{c.SetHeader("X-Late", "bad"), c.AddHeader("X-Late", "bad"), c.SetCookie(&http.Cookie{Name: "late", Value: "bad"}), c.SetStatus(404), c.Redirect("/late", 307)} {
		if !errors.Is(err, ErrHeadersCommitted) {
			t.Errorf("sealed response mutated: %v", err)
		}
	}
}

func TestBodylessResponseStatuses(t *testing.T) {
	for _, status := range []int{204, 304} {
		a := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: func(c *Context) (Page, error) { return Page{Body: ui.Text("must not send")}, c.SetStatus(status) }}}})
		w := request(a, "/", false)
		if w.Code != status || w.Body.Len() != 0 {
			t.Errorf("bodyless status %d emitted body: %d %s", status, w.Code, w.Body.String())
		}
	}
}

func TestSetStatusClearsEarlierRedirect(t *testing.T) {
	a := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: func(c *Context) (Page, error) {
		if err := c.Redirect("/redirect", 307); err != nil {
			return Page{}, err
		}
		return Page{Body: ui.Text("accepted")}, c.SetStatus(202)
	}}}})
	w, envelope := runtimeEnvelope(t, a, "/", nil)
	if w.Code != 202 || w.Header().Get("Location") != "" || envelope.HTML != "accepted" {
		t.Fatalf("stale redirect won over later status: %d %v %+v", w.Code, w.Header(), envelope)
	}
}

func TestInvalidResponseControlsReturnGeneric500WithoutSideEffects(t *testing.T) {
	a := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: func(c *Context) (Page, error) {
		_ = c.SetCookie(&http.Cookie{Name: "session", Value: "private"})
		_ = c.SetHeader("X-Staged", "private")
		_ = c.Redirect("/private", 307)
		_ = c.AddHeader("Content-Length", "7")
		return Page{Title: "private title", Body: ui.Text("private body")}, nil
	}}}})
	w, envelope := runtimeEnvelope(t, a, "/", nil)
	if w.Code != 500 || envelope.HTML != "<h1>Something went wrong</h1>" || len(w.Result().Cookies()) != 0 || w.Header().Get("X-Staged") != "" || w.Header().Get("Location") != "" || strings.Contains(w.Body.String(), "private") {
		t.Fatalf("invalid response retained staged side effects: %d %v %s", w.Code, w.Header(), w.Body.String())
	}
}

func TestBoundaryCanExplicitlyRedirect(t *testing.T) {
	for _, local := range []bool{false, true} {
		t.Run(fmt.Sprint(local), func(t *testing.T) {
			boundary := func(c *Context) (Page, error) { return Page{}, c.Redirect("/recovered", 303) }
			config := Config{NotFound: boundary}
			path := "/missing"
			if local {
				config.NotFound = nil
				config.Routes = []Route{{Pattern: path, Page: func(*Context) (Page, error) { return Page{}, ErrNotFound }, NotFound: boundary}}
			}
			a := testApp(t, config)
			w := request(a, path, false)
			if w.Code != 303 || w.Header().Get("Location") != "/recovered" {
				t.Fatalf("boundary redirect ignored: %d %v", w.Code, w.Header())
			}
		})
	}
	a := testApp(t, Config{Routes: []Route{{Pattern: "/failure", Page: func(*Context) (Page, error) { return Page{}, errors.New("failure") }, Error: func(c *Context, _ error) (Page, error) { return Page{}, c.Redirect("/login", 307) }}}})
	w := request(a, "/failure", true)
	if w.Code != 307 || w.Header().Get("Location") != "/login" {
		t.Fatalf("error boundary redirect ignored: %d %v", w.Code, w.Header())
	}
}

func TestCredentialHeaderPresenceAlwaysBypassesPublicCache(t *testing.T) {
	for _, headers := range []http.Header{
		{"Cookie": []string{"", "session=private"}},
		{"Authorization": []string{"", "Bearer private"}},
		{"Cookie": []string{""}},
		{"Authorization": []string{""}},
	} {
		var loads atomic.Int64
		a := testApp(t, Config{Routes: []Route{{Pattern: "/", Cache: &CachePolicy{TTL: time.Minute}, Page: func(*Context) (Page, error) { loads.Add(1); return Page{Body: ui.Text("uncached")}, nil }}}})
		for i := 0; i < 2; i++ {
			w, _ := runtimeEnvelope(t, a, "/", headers)
			if w.Code != 200 || w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Nextgo-Prefetch") != "" {
				t.Fatalf("credential fields shared cache: %v -> %d %v", headers, w.Code, w.Header())
			}
		}
		if loads.Load() != 2 {
			t.Fatalf("credential presence reused public result: %v loads=%d", headers, loads.Load())
		}
	}
}

func TestMultipleSetCookieFieldsAlwaysPrivateAndUncached(t *testing.T) {
	for _, values := range [][]string{{"", "session=private"}, {""}} {
		var loads atomic.Int64
		middleware := func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for _, value := range values {
					w.Header().Add("Set-Cookie", value)
				}
				next.ServeHTTP(w, r)
			})
		}
		a := testApp(t, Config{Middleware: []func(http.Handler) http.Handler{middleware}, Routes: []Route{{Pattern: "/", Cache: &CachePolicy{TTL: time.Minute}, Page: func(*Context) (Page, error) { loads.Add(1); return Page{Body: ui.Text("uncached")}, nil }}}})
		for i := 0; i < 2; i++ {
			w, _ := runtimeEnvelope(t, a, "/", nil)
			if w.Code != 200 || w.Header().Get("Cache-Control") != "private, no-store" || w.Header().Get("X-Nextgo-Prefetch") != "" {
				t.Fatalf("multi-value Set-Cookie response public: %v -> %d %v", values, w.Code, w.Header())
			}
		}
		if loads.Load() != 2 {
			t.Fatalf("Set-Cookie response reused public result: %v loads=%d", values, loads.Load())
		}
	}
	a := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: func(c *Context) (Page, error) {
		_ = c.SetHeader("Set-Cookie", "")
		_ = c.SetCookie(&http.Cookie{Name: "session", Value: "private"})
		return Page{Body: ui.Text("body")}, nil
	}}}})
	w := request(a, "/", false)
	if w.Header().Get("Cache-Control") != "private, no-store" || len(w.Header().Values("Set-Cookie")) != 2 {
		t.Fatalf("staged empty-first Set-Cookie bypassed privacy: %v", w.Header())
	}
}

func TestLoadingFallbackResponseControlsAndSealing(t *testing.T) {
	late := make(chan error, 1)
	a := testApp(t, Config{Routes: []Route{{Pattern: "/target", Page: static("target"), Loading: func(c *Context) (ui.Node, error) {
		if err := c.SetHeader("X-Loading", "yes"); err != nil {
			return nil, err
		}
		if err := c.SetCookie(&http.Cookie{Name: "loading", Value: "private"}); err != nil {
			return nil, err
		}
		if err := c.SetStatus(201); err != nil {
			return nil, err
		}
		return responseNodeFunc(func(w io.Writer) error {
			late <- c.SetHeader("X-Late", "bad")
			_, err := io.WriteString(w, "loading content")
			return err
		}), nil
	}}}})
	w := request(a, "/_nextgo/loading?path=/target", false)
	if w.Code != 201 || w.Body.String() != "loading content" || w.Header().Get("X-Loading") != "yes" || len(w.Result().Cookies()) != 1 || w.Header().Get("Cache-Control") != "private, no-store" || w.Header().Get("X-Late") != "" {
		t.Fatalf("loading controls ignored: %d %v %s", w.Code, w.Header(), w.Body.String())
	}
	if err := <-late; !errors.Is(err, ErrHeadersCommitted) {
		t.Fatalf("loading response unsealed: %v", err)
	}
}

func TestLoadingFallbackRedirectAndInvalidControls(t *testing.T) {
	a := testApp(t, Config{Routes: []Route{
		{Pattern: "/redirect", Page: static("target"), Loading: func(c *Context) (ui.Node, error) { return ui.Text("ignored"), c.Redirect("/login", 303) }},
		{Pattern: "/invalid", Page: static("target"), Loading: func(c *Context) (ui.Node, error) {
			_ = c.SetCookie(&http.Cookie{Name: "private", Value: "session"})
			_ = c.SetHeader("X-Bad", "bad\r\nvalue")
			return ui.Text("private fallback"), nil
		}},
	}})
	redirect := request(a, "/_nextgo/loading?path=/redirect", false)
	if redirect.Code != 303 || redirect.Header().Get("Location") != "/login" {
		t.Fatalf("loading redirect ignored: %d %v", redirect.Code, redirect.Header())
	}
	invalid := request(a, "/_nextgo/loading?path=/invalid", false)
	if invalid.Code != 500 || len(invalid.Result().Cookies()) != 0 || strings.Contains(invalid.Body.String(), "private") {
		t.Fatalf("invalid loading controls applied: %d %v %s", invalid.Code, invalid.Header(), invalid.Body.String())
	}
}
