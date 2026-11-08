//go:build !js

package health

import "net/http"

func Handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"framework":"Next.go","fixture":"widgets"}`))
}
