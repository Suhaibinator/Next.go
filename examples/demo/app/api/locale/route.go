//go:build !js

package locale

import (
	"encoding/json"
	nextgo "github.com/Suhaibinator/Next.go"
	"net/http"
)

func Handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(405)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if r.Method != "HEAD" {
		_ = json.NewEncoder(w).Encode(map[string]any{"locale": nextgo.CurrentLocale(r.Context()), "locales": nextgo.AvailableLocales(r.Context())})
	}
}
