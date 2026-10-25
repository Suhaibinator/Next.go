//go:build !js

package cookie

import (
	nextgo "github.com/Suhaibinator/Next.go"
	"independent.example/blog/internal/state"
	"net/http"
)

func Handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(405)
		return
	}
	path := nextgo.CurrentZone(r.Context()).BasePath
	if path == "" {
		path = "/"
	}
	// This public marker is not a login credential or identity claim.
	http.SetCookie(w, &http.Cookie{Name: "independent-demo", Value: state.NativeVersion, Path: path, HttpOnly: true, SameSite: http.SameSiteLaxMode})
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	if r.Method == "GET" {
		_, _ = w.Write([]byte("Public non-authentication cookie marker.\n"))
	}
}
