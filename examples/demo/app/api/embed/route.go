//go:build !js

package embed

import (
	"io"
	"net/http"

	"github.com/Suhaibinator/Next.go/ui"
)

// Handle is a local, script-free document for the sandboxed embed example.
// It overrides the demo document's frame-ancestors policy only for this endpoint.
func Handle(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	message := r.URL.Query().Get("message")
	if len(message) > 1024 {
		http.Error(w, "Message is too long", http.StatusBadRequest)
		return
	}
	if message == "" {
		message = "A local document inside a restrictive iframe."
	}
	referrer := "No referrer sent"
	if r.Referer() != "" {
		referrer = "A referrer was sent"
	}
	markup, err := ui.HTML(ui.El("html", ui.Attrs{"lang": "en"},
		ui.El("head", nil, ui.El("meta", ui.Attrs{"charset": "utf-8"}), ui.El("title", nil, ui.Text("Local sandboxed document"))),
		ui.El("body", nil, ui.El("main", nil,
			ui.El("h1", nil, ui.Text("Local embedded document")),
			ui.El("p", ui.Attrs{"id": "embed-message"}, ui.Text(message)),
			ui.El("p", ui.Attrs{"id": "embed-referrer"}, ui.Text(referrer))))))
	if err != nil {
		http.Error(w, "Unable to render document", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'self'; base-uri 'none'; form-action 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method != http.MethodHead {
		_, _ = io.WriteString(w, "<!doctype html>"+markup)
	}
}
