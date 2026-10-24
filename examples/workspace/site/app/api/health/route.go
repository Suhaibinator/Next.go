//go:build !js

package health

import (
	"encoding/json"
	"net/http"
	"workspace.example/shared"
)

func Handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "label": shared.Label})
}
