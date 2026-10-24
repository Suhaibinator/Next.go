//go:build !js

// Package data is server-only. Its contents never enter the browser import graph.
package data

import (
	"context"
	"fmt"
	nextgo "github.com/Suhaibinator/Next.go"
	_ "github.com/Suhaibinator/Next.go/serveronly"
	"time"
)

type Post struct{ Slug, Title, Summary, Body string }

var posts = []Post{
	{"html-first", "Start with the HTML", "A useful page should arrive before its runtime.", "Server rendering is the foundation. The first response contains the page, its data, and each component’s initial state. WebAssembly adds behavior to those existing nodes. If it never loads, the content and ordinary navigation remain useful."},
	{"go-islands", "Small islands, explicit state", "A clear boundary keeps the browser runtime understandable.", "A component is a Go function on the server and a mount function in the browser. Mount validates the markup, subscribes to state, and attaches event listeners. Every subscription and listener belongs to a scope that is disposed when you leave the page."},
	{"routes-as-packages", "Routes as Go packages", "File routing without a new language or compiler plugin.", "Go import paths cannot use the bracket syntax common in JavaScript frameworks. Next.go uses param_slug directories instead. A build-time generator discovers page.go, layout.go, and route.go files, then emits ordinary typed Go registration code."},
}

func All(ctx context.Context) ([]Post, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return append([]Post(nil), posts...), nil
}

// AllForRequest shares an immutable collection with this request's page and
// layouts. All remains available for non-framework callers and unit tests.
func AllForRequest(ctx context.Context) ([]Post, error) {
	return nextgo.Memo(ctx, "demo.posts.all", All)
}

func Find(ctx context.Context, slug string) (Post, error) {
	if err := ctx.Err(); err != nil {
		return Post{}, err
	}
	for _, p := range posts {
		if p.Slug == slug {
			return p, nil
		}
	}
	return Post{}, nextgo.ErrNotFound
}

// Preview demonstrates cancellable loading and error states, capped at two seconds.
func Preview(c *nextgo.Context) error {
	if c.Query.Get("fail") == "1" {
		return fmt.Errorf("demonstration loader failure")
	}
	if raw := c.Query.Get("delay"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d < 0 || d > 2*time.Second {
			return fmt.Errorf("delay must be between 0 and 2s")
		}
		timer := time.NewTimer(d)
		defer timer.Stop()
		select {
		case <-c.Done():
			return c.Err()
		case <-timer.C:
		}
	}
	return nil
}

// FindForRequest shares an immutable post between page and metadata providers.
func FindForRequest(ctx context.Context, slug string) (Post, error) {
	return nextgo.Memo(ctx, "demo.posts.one:"+slug, func(ctx context.Context) (Post, error) { return Find(ctx, slug) })
}
