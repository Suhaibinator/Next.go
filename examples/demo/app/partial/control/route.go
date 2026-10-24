//go:build !js

package partialcontrol

import (
	"crypto/subtle"
	"net/http"
	"os"
	"strings"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/cache/shared"
)

// Handle is an explicitly enabled fixture control, never a general cache API.
// The bearer secret stays in the fixture's server process and is not page data.
func Handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "private, no-store")
	if os.Getenv("NEXTGO_DEMO_PARTIAL_CONTROLS") != "true" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	secret := os.Getenv("NEXTGO_DEMO_PARTIAL_CONTROL_TOKEN")
	valid := len(secret) >= 32 && len(secret) <= 256
	for i := range secret {
		if secret[i] < 33 || secret[i] > 126 {
			valid = false
		}
	}
	values := r.Header.Values("Authorization")
	provided := ""
	if len(values) == 1 && len(values[0]) >= 7 && strings.EqualFold(values[0][:7], "Bearer ") {
		provided = values[0][7:]
	}
	if !valid || len(values) != 1 || subtle.ConstantTimeCompare([]byte(provided), []byte(secret)) != 1 {
		w.Header().Set("WWW-Authenticate", `Bearer realm="nextgo-demo-partial-control"`)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	// Request bodies and selectors are deliberately ignored. This endpoint can
	// expire only the sample's fixed public-shell tag in its own App context.
	if err := nextgo.ExpireCache(r.Context(), shared.Selector{Kind: "tag", Value: "partial-demo"}); err != nil {
		http.Error(w, "Cache invalidation failed", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
