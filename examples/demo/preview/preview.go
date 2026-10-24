//go:build !js

// Package preview demonstrates application-authorized draft access. Signing
// keys and editor credentials are loaded only by the native server.
package preview

import (
	"context"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"time"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/cache"
	"github.com/Suhaibinator/Next.go/draft"
	"github.com/Suhaibinator/Next.go/examples/demo/access"
	"github.com/Suhaibinator/Next.go/examples/demo/state"
)

type Article struct {
	Title, Body string
	Generation  int64
}

func (a Article) CacheSize() int64 { return int64(len(a.Title) + len(a.Body) + 64) }

var generation atomic.Int64

func Load(ctx context.Context) (Article, error) {
	return cache.GetOrLoad(ctx, state.Cache, "demo.preview.article.v1", cache.Options{TTL: time.Hour}, func(ctx context.Context) (Article, error) {
		if err := ctx.Err(); err != nil {
			return Article{}, err
		}
		a := Article{Title: "Published field note", Body: "This is the public version. Ordinary visitors continue to receive the published cache.", Generation: generation.Add(1)}
		if draft.Enabled(ctx) {
			a.Title = "Unpublished field note"
			a.Body = "This draft is loaded for your verified preview session without reading or replacing the published cache."
		}
		return a, nil
	})
}

func Configure(c *nextgo.Config) {
	keyText := os.Getenv("NEXTGO_DEMO_DRAFT_KEY")
	if keyText == "" {
		return
	}
	key, err := hex.DecodeString(keyText)
	if err != nil {
		panic("NEXTGO_DEMO_DRAFT_KEY must be a hex-encoded 32–64-byte random key")
	}
	plainHTTP := os.Getenv("NEXTGO_DEMO_DRAFT_HTTP") == "true"
	manager, err := draft.New(draft.Options{Keys: map[string][]byte{"current": key}, ActiveKey: "current", Scope: "nextgo-demo-preview-v1", Path: c.BasePath, AllowInsecure: plainHTTP})
	if err != nil {
		panic(fmt.Errorf("demo preview configuration: %w", err))
	}
	c.Draft = manager
	if c.Handlers == nil {
		c.Handlers = map[string]http.Handler{}
	}
	for _, enable := range []bool{true, false} {
		endpoint := "/api/preview/disable"
		if enable {
			endpoint = "/api/preview/enable"
		}
		c.Handlers[endpoint] = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "private, no-store")
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("X-Robots-Tag", "noindex")
			if r.Method != http.MethodPost {
				w.Header().Set("Allow", "POST")
				http.Error(w, "Use POST", 405)
				return
			}
			origin, err := url.Parse(r.Header.Get("Origin"))
			scheme := "https"
			if plainHTTP {
				scheme = "http"
			}
			if err != nil || origin.Scheme != scheme || !strings.EqualFold(origin.Host, r.Host) || origin.User != nil || origin.Path != "" || origin.RawQuery != "" || origin.Fragment != "" || r.Header.Get("Sec-Fetch-Site") == "cross-site" {
				http.Error(w, "Origin rejected", 403)
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 4096)
			payload, err := io.ReadAll(r.Body)
			if err != nil {
				http.Error(w, "Invalid or oversized form", 413)
				return
			}
			if enable {
				credentialRequest := r.Clone(r.Context())
				credentialRequest.Header = r.Header.Clone()
				if credentialRequest.Header.Get("Authorization") == "" {
					media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
					values, err := url.ParseQuery(string(payload))
					if media != "application/x-www-form-urlencoded" || err != nil || len(values) != 1 || len(values["token"]) != 1 {
						http.Error(w, "Editor authorization required", 401)
						return
					}
					credentialRequest.Header.Set("Authorization", "Bearer "+values.Get("token"))
				}
				role, err := access.Authenticate(credentialRequest)
				if err != nil {
					http.Error(w, "Editor authorization required", 401)
					return
				}
				if role != access.Administrator {
					http.Error(w, "Editor access required", 403)
					return
				}
				cookie, err := manager.Cookie(r)
				if err != nil {
					http.Error(w, "Preview unavailable", 500)
					return
				}
				http.SetCookie(w, cookie)
			} else {
				http.SetCookie(w, manager.ClearCookie())
			}
			// Fixed application-owned destination; no caller-provided redirect.
			http.Redirect(w, r, nextgo.MustURL(r.Context(), "/preview"), http.StatusSeeOther)
		})
	}
}
