//go:build !js

package validate

import (
	"encoding/json"
	"github.com/Suhaibinator/Next.go/examples/form-validation/model"
	"net/http"
	"net/url"
	"unicode/utf8"
)

func Handle(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if r.Method != "GET" && r.Method != "HEAD" {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "Method Not Allowed", 405)
		return
	}
	if len(r.URL.RawQuery) > 1024 {
		http.Error(w, "Bad Request", 400)
		return
	}
	q, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil || len(q) != 2 || len(q["field"]) != 1 || len(q["value"]) != 1 {
		http.Error(w, "Bad Request", 400)
		return
	}
	field, value := q.Get("field"), q.Get("value")
	if field != "name" && field != "code" || len(value) > 128 || !utf8.ValidString(value) {
		http.Error(w, "Bad Request", 400)
		return
	}
	message, err := model.Check(r.Context(), field, value)
	if r.Context().Err() != nil {
		return
	}
	if err != nil {
		http.Error(w, "Public check unavailable; retry explicitly.", 503)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if r.Method != "HEAD" {
		_ = json.NewEncoder(w).Encode(model.ValidationResult{Message: message})
	}
}
