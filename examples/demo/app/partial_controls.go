//go:build !js

package app

import (
	"net/http"
	"os"
	"strings"

	nextgo "github.com/Suhaibinator/Next.go"
)

// This opt-in middleware lets the browser fixture revoke a request-time grant
// after prefetching a public shell. Real applications supply their own auth.
func configurePartialControls(config *nextgo.Config) {
	if os.Getenv("NEXTGO_DEMO_PARTIAL_AUTH_FIXTURE") != "true" {
		return
	}
	config.Middleware = append(config.Middleware, func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if (r.URL.Path == "/partial" || strings.HasPrefix(r.URL.Path, "/partial/")) && r.URL.Query().Get("protected") == "1" {
				grants, allowed := 0, false
				for _, cookie := range r.Cookies() {
					if cookie.Name == "nextgo_partial_grant" {
						grants++
						allowed = cookie.Value == "allowed"
					}
				}
				if grants != 1 || !allowed {
					w.Header().Set("Cache-Control", "private, no-store")
					http.Error(w, "Request permission denied", http.StatusForbidden)
					return
				}
			}
			next.ServeHTTP(w, r)
		})
	})
}
