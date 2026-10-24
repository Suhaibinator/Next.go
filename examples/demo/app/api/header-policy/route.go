//go:build !js

package headerpolicy

import (
	"encoding/json"
	"net/http"
)

func Handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	// The framework's conditional header policy must clamp this at final write.
	w.Header().Set("Cache-Control", "public, max-age=999")
	if r.Method == "HEAD" {
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]string{"message": "A conditional header source remains private on matches and misses."})
}
