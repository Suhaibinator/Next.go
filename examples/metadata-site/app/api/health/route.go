//go:build !js

package health

import "net/http"

func Handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if r.Method == "GET" {
		_, _ = w.Write([]byte("{\"ok\":true,\"fixture\":\"metadata-site\"}\n"))
	}
}
