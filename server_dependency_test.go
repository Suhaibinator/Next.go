package nextgo

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/ui"
)

func TestRequireServerStagesHTMLAndNavigationWithoutChangingProtocol(t *testing.T) {
	var saved *Context
	app := testApp(t, Config{BuildID: "dependencies", Routes: []Route{{Pattern: "/", Page: func(c *Context) (Page, error) {
		saved = c
		if err := c.RequireServer(); err != nil {
			return Page{}, err
		}
		return Page{Body: ui.Text("API-backed interface")}, c.RequireServer()
	}}}})
	t.Cleanup(func() { _ = app.Close(context.Background()) })
	for _, nav := range []bool{false, true} {
		w := request(app, "/", nav)
		if w.Code != http.StatusOK || w.Header().Get(RequiresServerHeader) != "1" || len(w.Header().Values(RequiresServerHeader)) != 1 || !strings.Contains(w.Body.String(), "API-backed interface") {
			t.Fatalf("missing or duplicated dependency declaration: status=%d headers=%v body=%s", w.Code, w.Header(), w.Body.String())
		}
		if nav && w.Header().Get("X-Nextgo-Protocol") != "3" {
			t.Fatal("declaration changed navigation protocol", w.Header())
		}
		if err := saved.RequireServer(); !errors.Is(err, ErrHeadersCommitted) {
			t.Fatal("retained context accepted a late dependency declaration", err)
		}
	}
}

func TestRequireServerCannotBeDeferredOrSilentlyCached(t *testing.T) {
	t.Run("late-render", func(t *testing.T) {
		var late error
		app := testApp(t, Config{Routes: []Route{{Pattern: "/", Page: func(c *Context) (Page, error) {
			return Page{Body: responseNodeFunc(func(w io.Writer) error {
				late = c.RequireServer()
				_, err := io.WriteString(w, "already rendering")
				return err
			})}, nil
		}}}})
		t.Cleanup(func() { _ = app.Close(context.Background()) })
		w := request(app, "/", false)
		if w.Code != http.StatusOK || !errors.Is(late, ErrHeadersCommitted) || w.Header().Get(RequiresServerHeader) != "" {
			t.Fatalf("late dependency changed committed response: status=%d error=%v headers=%v", w.Code, late, w.Header())
		}
	})
	t.Run("public-cache", func(t *testing.T) {
		var rejected error
		app := testApp(t, Config{Routes: []Route{{Pattern: "/", Cache: &CachePolicy{TTL: time.Hour}, Page: func(c *Context) (Page, error) {
			rejected = c.RequireServer()
			// Even ignoring a response-control error must not publish this result.
			return Page{Body: ui.Text("must not be cached")}, nil
		}}}})
		t.Cleanup(func() { _ = app.Close(context.Background()) })
		w := request(app, "/", false)
		if w.Code != http.StatusInternalServerError || !errors.Is(rejected, ErrPublicCacheResponse) || strings.Contains(w.Body.String(), "must not be cached") || w.Header().Get(RequiresServerHeader) != "" {
			t.Fatalf("public loader silently retained dependency declaration: status=%d error=%v body=%s", w.Code, rejected, w.Body.String())
		}
	})
}
