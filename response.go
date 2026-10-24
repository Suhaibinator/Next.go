package nextgo

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
)

// ErrHeadersCommitted means the page's response controls have been sealed.
// Headers must be set in the page, layout, or error boundary before it returns;
// rendering and deferred loaders cannot change an HTTP response already begun.
var ErrHeadersCommitted = errors.New("nextgo: response headers are committed")

// ErrPublicCacheResponse means a public cached loader attempted to change its
// request's response. Public cached pages must be request-independent: headers,
// cookies, statuses, and redirects cannot be cached or shared between waiters.
// Ignoring this error still rejects the render from the cache and returns 500.
var ErrPublicCacheResponse = errors.New("nextgo: public cached pages cannot mutate the response")

type responseState struct {
	mu       sync.Mutex
	readonly error
	headers  http.Header
	replace  map[string]bool
	status   int
	redirect string
	sealed   bool
	public   bool
	violated bool
	err      error
}

type responseSnapshot struct {
	headers  http.Header
	replace  map[string]bool
	status   int
	redirect string
	violated bool
	err      error
}

// Context views for retained primary pages share response controls without
// copying a mutex or changing the request visible to an outer layout.
func (c *Context) responseControls() *responseState {
	if c.responseRef != nil {
		return c.responseRef
	}
	return &c.response
}

func (s *responseState) mutate(change func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sealed {
		return ErrHeadersCommitted
	}
	if s.readonly != nil {
		s.err = s.readonly
		return s.readonly
	}
	if s.public {
		s.violated = true
		return ErrPublicCacheResponse
	}
	if s.headers == nil {
		s.headers = make(http.Header)
		s.replace = make(map[string]bool)
	}
	err := change()
	if err != nil && s.err == nil {
		s.err = err
	}
	return err
}

func (s *responseState) seal() responseSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sealed = true
	replace := make(map[string]bool, len(s.replace))
	for key, value := range s.replace {
		replace[key] = value
	}
	return responseSnapshot{headers: s.headers.Clone(), replace: replace, status: s.status, redirect: s.redirect, violated: s.violated, err: s.err}
}

// SetHeader replaces a response header. Framing and hop-by-hop headers are
// reserved for the HTTP server. Names and values follow HTTP field syntax.
func (c *Context) SetHeader(name, value string) error {
	return c.responseControls().mutate(func() error {
		if err := validateResponseHeader(name, value); err != nil {
			return err
		}
		name = http.CanonicalHeaderKey(name)
		c.responseControls().headers.Set(name, value)
		c.responseControls().replace[name] = true
		return nil
	})
}

// AddHeader appends a response field, including fields set by middleware.
func (c *Context) AddHeader(name, value string) error {
	return c.responseControls().mutate(func() error {
		if err := validateResponseHeader(name, value); err != nil {
			return err
		}
		c.responseControls().headers.Add(name, value)
		return nil
	})
}

// SetCookie appends a validated cookie. The supplied value is copied before it
// is stored. Any Set-Cookie response is marked private, no-store automatically.
func (c *Context) SetCookie(cookie *http.Cookie) error {
	return c.responseControls().mutate(func() error {
		if cookie == nil {
			return fmt.Errorf("nextgo: nil response cookie")
		}
		copyCookie := *cookie
		copyCookie.Unparsed = append([]string(nil), cookie.Unparsed...)
		if err := copyCookie.Valid(); err != nil {
			return fmt.Errorf("nextgo: invalid response cookie: %w", err)
		}
		value := copyCookie.String()
		if err := validateResponseHeader("Set-Cookie", value); err != nil {
			return err
		}
		c.responseControls().headers.Add("Set-Cookie", value)
		return nil
	})
}

// SetStatus changes a successful page's HTTP status. A page error or not-found
// boundary retains its error status. SetStatus clears an earlier Redirect.
func (c *Context) SetStatus(status int) error {
	return c.responseControls().mutate(func() error {
		if status < 200 || status > 599 {
			return fmt.Errorf("nextgo: invalid response status %d", status)
		}
		c.responseControls().status, c.responseControls().redirect = status, ""
		return nil
	})
}

// Redirect sends an ordinary HTTP redirect to a local absolute URL. Supported
// statuses are 301, 302, 303, 307, and 308. Navigation fetches follow the redirect
// and receive the target page's canonical URL through the navigation envelope.
// A redirect is an explicit control result even inside an error/not-found
// boundary. Return from the page after calling it; it does not stop Go execution.
func (c *Context) Redirect(location string, status int) error {
	return c.responseControls().mutate(func() error {
		if status != 301 && status != 302 && status != 303 && status != 307 && status != 308 {
			return fmt.Errorf("nextgo: invalid redirect status %d", status)
		}
		if !localURL(location) || validateResponseHeader("Location", location) != nil {
			return fmt.Errorf("nextgo: redirect requires a local absolute URL")
		}
		resolved, err := URL(c.Context, location)
		if err != nil {
			return err
		}
		c.responseControls().status, c.responseControls().redirect = status, resolved
		return nil
	})
}

func validateResponseHeader(name, value string) error {
	if name == "" {
		return fmt.Errorf("nextgo: empty response header name")
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", r)) {
			return fmt.Errorf("nextgo: invalid response header name %q", name)
		}
	}
	switch strings.ToLower(name) {
	case "connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "proxy-connection", "te", "trailer", "transfer-encoding", "upgrade", "http2-settings", "content-length":
		return fmt.Errorf("nextgo: reserved response header %q", name)
	}
	for _, r := range []byte(value) {
		if r < 32 && r != '\t' || r == 127 {
			return fmt.Errorf("nextgo: invalid response header value for %q", name)
		}
	}
	return nil
}

// applyResponse seals the context before rendering starts. Application errors
// take precedence over status choices; explicit redirects remain control
// results. Validation failures apply none of the staged response changes.
func (a *App) applyResponse(w http.ResponseWriter, r *http.Request, c *Context, status int) (int, bool, error) {
	snapshot := c.responseControls().seal()
	if snapshot.violated {
		return http.StatusInternalServerError, false, ErrPublicCacheResponse
	}
	if snapshot.err != nil {
		return http.StatusInternalServerError, false, snapshot.err
	}
	for key, values := range snapshot.headers {
		if snapshot.replace[key] {
			w.Header()[key] = append([]string(nil), values...)
		} else {
			for _, value := range values {
				w.Header().Add(key, value)
			}
		}
	}
	if len(w.Header().Values("Set-Cookie")) > 0 {
		w.Header().Set("Cache-Control", "private, no-store")
		clearNavigationReuse(w.Header())
	}
	if snapshot.redirect != "" {
		if w.Header().Get("Cache-Control") == "" {
			w.Header().Set("Cache-Control", "no-store")
		}
		http.Redirect(w, r, snapshot.redirect, snapshot.status)
		return snapshot.status, true, nil
	}
	if status == http.StatusOK && snapshot.status != 0 {
		status = snapshot.status
	}
	return status, false, nil
}
