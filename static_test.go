package nextgo

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Suhaibinator/Next.go/ui"
)

func staticPage(*Context) (Page, error) { return Page{Body: ui.Text("public content")}, nil }
func TestStaticPathsEnumerationEscapingAndScope(t *testing.T) {
	var provided context.Context
	calls := 0
	a, err := New(Config{Routes: []Route{
		{Pattern: "/", Page: staticPage}, {Pattern: "/about", Page: staticPage},
		{Pattern: "/users/:name", Page: staticPage, StaticParams: func(ctx context.Context) ([]Params, error) {
			provided = ctx
			for i := 0; i < 2; i++ {
				_, err := Memo(ctx, "build-data", func(context.Context) (int, error) { calls++; return 1, nil })
				if err != nil {
					return nil, err
				}
			}
			return []Params{{"name": "café"}, {"name": "a?b#c"}, {"name": "a,b"}, {"name": "café"}}, nil
		}},
		{Pattern: "/docs/*tail", Page: staticPage, StaticParams: func(context.Context) ([]Params, error) { return []Params{{"tail": "go/wasm"}}, nil }},
		{Pattern: "/catalog/*tail?", Page: staticPage, StaticParams: func(context.Context) ([]Params, error) { return []Params{nil, {"tail": "go/wasm"}}, nil }},
		{Pattern: "/unlisted/:id", Page: staticPage},
		{Pattern: "/api/health", API: func(http.ResponseWriter, *http.Request) {}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	paths, err := a.StaticPaths(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	expected := []string{"/", "/about", "/catalog", "/catalog/go/wasm", "/docs/go/wasm", "/users/a%2Cb", "/users/a%3Fb%23c", "/users/caf%C3%A9"}
	if !reflect.DeepEqual(paths, expected) || calls != 1 {
		t.Fatalf("paths=%v loads=%d", paths, calls)
	}
	if provided.Err() == nil {
		t.Fatal("enumerator context was not cleaned up")
	}
}
func TestStaticPathsRejectInvalidAndShadowedParameters(t *testing.T) {
	for _, params := range []Params{nil, {"id": ""}, {"typo": "x"}, {"id": "ok", "extra": "x"}, {"id": "a/b"}, {"id": ".."}, {"id": "a\\b"}, {"id": "a\x00b"}, {"id": string([]byte{255})}, {"id": strings.Repeat("a", 8192)}} {
		a, err := New(Config{Routes: []Route{{Pattern: "/:id", Page: staticPage, StaticParams: func(context.Context) ([]Params, error) { return []Params{params}, nil }}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.StaticPaths(context.Background()); err == nil {
			t.Errorf("accepted parameters %q", params)
		}
	}
	a, err := New(Config{Routes: []Route{{Pattern: "/posts/new", Page: staticPage}, {Pattern: "/posts/:id", Page: staticPage, StaticParams: func(context.Context) ([]Params, error) { return []Params{{"id": "new"}}, nil }}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.StaticPaths(context.Background()); err == nil || !strings.Contains(err.Error(), "shadowed") {
		t.Fatalf("shadowing: %v", err)
	}
	for _, pattern := range []string{"/docs/*tail", "/docs/*tail?"} {
		a, err := New(Config{Routes: []Route{{Pattern: pattern, Page: staticPage, StaticParams: func(context.Context) ([]Params, error) { return []Params{{"tail": "a//b"}}, nil }}}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := a.StaticPaths(context.Background()); err == nil {
			t.Fatal("accepted empty catch-all segment")
		}
	}
}
func TestStaticPathsLoaderFailureCancellationAndLimits(t *testing.T) {
	for _, load := range []StaticParamsFunc{
		func(context.Context) ([]Params, error) { return nil, errors.New("failed") },
		func(context.Context) ([]Params, error) { panic("private panic text") },
		func(context.Context) ([]Params, error) { runtime.Goexit(); return nil, nil },
		func(context.Context) ([]Params, error) { return make([]Params, 10001), nil },
	} {
		a, err := New(Config{Routes: []Route{{Pattern: "/:id", Page: staticPage, StaticParams: load}}})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		_, err = a.StaticPaths(ctx)
		cancel()
		if err == nil || strings.Contains(err.Error(), "private panic text") {
			t.Fatalf("failure handling: %v", err)
		}
	}
	a, err := New(Config{Routes: []Route{{Pattern: "/:id", Page: staticPage, StaticParams: func(ctx context.Context) ([]Params, error) { <-ctx.Done(); return nil, ctx.Err() }}}})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := a.StaticPaths(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cancellation: %v", err)
	}
	for _, route := range []Route{{Pattern: "/", Page: staticPage}, {Pattern: "/api/:id", API: func(http.ResponseWriter, *http.Request) {}}} {
		route.StaticParams = func(context.Context) ([]Params, error) { return nil, nil }
		if _, err := New(Config{Routes: []Route{route}}); err == nil {
			t.Fatal("accepted enumerator on a static page or HTTP handler")
		}
	}
}
