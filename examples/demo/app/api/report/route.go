//go:build !js

package report

import (
	"encoding/json"
	"errors"
	"net/http"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/examples/demo/access"
)

func Handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(405)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Robots-Tag", "noindex")
	value, err := access.ReadReport(r.Context(), r)
	status := http.StatusOK
	if errors.Is(err, nextgo.ErrUnauthorized) {
		status = 401
		w.Header().Set("WWW-Authenticate", `Bearer realm="nextgo-demo"`)
	} else if errors.Is(err, nextgo.ErrForbidden) {
		status = 403
	} else if err != nil {
		status = 500
	}
	w.WriteHeader(status)
	if r.Method == "HEAD" {
		return
	}
	if err != nil {
		_ = json.NewEncoder(w).Encode(map[string]string{"error": http.StatusText(status)})
		return
	}
	_ = json.NewEncoder(w).Encode(value)
}
