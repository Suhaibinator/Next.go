//go:build !js

package resources

import (
	"net/http"
	"time"

	"github.com/Suhaibinator/Next.go/examples/demo/resourcedata"
)

func Handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if r.URL.Query().Get("fail") == "1" {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusServiceUnavailable)
		if r.Method != "HEAD" {
			_, _ = w.Write([]byte(`{"error":"The explicitly requested demo failure."}`))
		}
		return
	}
	_, body, err := resourcedata.Load(r.Context(), 120*time.Millisecond)
	if err != nil {
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	// There are no cookies, authorization-dependent fields or private inputs in
	// this loader. Both its HTTP and Go-resource freshness are explicitly public.
	w.Header().Set("Cache-Control", "public, max-age=30")
	if r.Method != "HEAD" {
		_, _ = w.Write(body)
	}
}
