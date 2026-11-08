//go:build !js

package health

import "net/http"

func Handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method == "GET" {
		_, _ = w.Write([]byte(`{"framework":"Next.go","fixture":"social-variants"}`))
	} else if r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(405)
	}
}
