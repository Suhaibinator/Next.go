//go:build !js

package embed

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestEmbeddedDocumentEscapesAndRestrictsContent(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/api/embed?"+url.Values{"message": {`<script>alert("bad")</script>`}}.Encode(), nil)
	w := httptest.NewRecorder()
	Handle(w, r)
	if w.Code != 200 || !strings.Contains(w.Body.String(), "&lt;script&gt;") || strings.Contains(w.Body.String(), "<script>") || !strings.Contains(w.Body.String(), "No referrer sent") {
		t.Fatal(w.Code, w.Body.String())
	}
	if got := w.Header().Get("Content-Security-Policy"); !strings.Contains(got, "default-src 'none'") || !strings.Contains(got, "frame-ancestors 'self'") {
		t.Fatal(got)
	}
	w = httptest.NewRecorder()
	Handle(w, httptest.NewRequest(http.MethodHead, "/api/embed", nil))
	if w.Code != 200 || w.Body.Len() != 0 || w.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatal(w.Code, w.Body.String(), w.Header())
	}
	w = httptest.NewRecorder()
	Handle(w, httptest.NewRequest(http.MethodPost, "/api/embed", nil))
	if w.Code != 405 || w.Header().Get("Allow") != "GET, HEAD" {
		t.Fatal(w.Code, w.Header())
	}
}
