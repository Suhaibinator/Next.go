//go:build !js

package status

import (
	"encoding/json"
	"github.com/Suhaibinator/Next.go/examples/refresh/serverstate"
	"net/http"
)

func Handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(405)
		return
	}
	if r.Method == "GET" {
		_ = json.NewEncoder(w).Encode(map[string]any{"instance": serverstate.Instance(), "requests": serverstate.NextRequest(), "source": serverstate.Source})
	}
}
