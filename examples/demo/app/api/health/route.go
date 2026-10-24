//go:build !js

package health

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
	if r.Method != "HEAD" {
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "framework": "Next.go", "runtime": "Go + WebAssembly"})
	}
}
