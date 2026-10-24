//go:build !js

package app

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/scripts"
	"net/http"
	"os"
	"strings"
)

func configureScripts(config *nextgo.Config) {
	if os.Getenv("NEXTGO_DEMO_EARLY_SCRIPTS") == "true" {
		config.EarlyScripts = []scripts.Script{{ID: "demo-early", Src: "/vendor-early.js", Asset: true, Strategy: scripts.BeforeInteractive}, {ID: "demo-early-second", Src: "/vendor-early-second.js", Asset: true, Strategy: scripts.BeforeInteractive}}
	}
	if os.Getenv("NEXTGO_DEMO_CSP") != "true" {
		return
	}
	config.Middleware = append(config.Middleware, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if strings.HasPrefix(r.URL.Path, "/assets/") || strings.HasPrefix(r.URL.Path, "/_nextgo/") {
				next.ServeHTTP(w, r)
				return
			}
			nonce, err := scripts.NewNonce()
			if err != nil {
				http.Error(w, "Unable to create security policy", 500)
				return
			}
			ctx, err := scripts.WithNonce(r.Context(), nonce)
			if err != nil {
				http.Error(w, "Invalid security policy", 500)
				return
			}
			w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'nonce-"+nonce+"' 'strict-dynamic' 'wasm-unsafe-eval'; style-src 'self' 'nonce-"+nonce+"'; img-src 'self' data:; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	})
}
