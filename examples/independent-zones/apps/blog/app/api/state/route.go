//go:build !js

package stateapi

import (
	"encoding/json"
	nextgo "github.com/Suhaibinator/Next.go"
	"independent.example/blog/internal/state"
	"net/http"
)

func Handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(405)
		return
	}
	if r.Method == "HEAD" {
		return
	}
	info := nextgo.CurrentZone(r.Context())
	_ = json.NewEncoder(w).Encode(map[string]any{"zone": info.ID, "nativeVersion": state.NativeVersion, "instance": state.Instance(), "revision": state.Revision(), "requests": state.NextRequest(), "host": info.Host, "basePath": info.BasePath, "publicOrigin": nextgo.PublicOrigin(r.Context())})
}
