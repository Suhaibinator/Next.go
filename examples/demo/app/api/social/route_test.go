//go:build !js

package social

import (
	"bytes"
	"image/png"
	"net/http/httptest"
	"testing"
)

func TestPublicSocialCardEndpoint(t *testing.T) {
	w := httptest.NewRecorder()
	Handle(w, httptest.NewRequest("GET", "http://example.test/api/social?title=Hello+Go&description=Public+pixels", nil))
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	cfg, err := png.DecodeConfig(bytes.NewReader(w.Body.Bytes()))
	if err != nil || cfg.Width != 1200 || cfg.Height != 630 {
		t.Fatal(cfg, err)
	}
	etag := w.Header().Get("ETag")
	r := httptest.NewRequest("HEAD", "http://example.test/api/social?title=Hello+Go&description=Public+pixels", nil)
	r.Header.Set("If-None-Match", etag)
	w = httptest.NewRecorder()
	Handle(w, r)
	if w.Code != 304 || w.Body.Len() != 0 {
		t.Fatal(w.Code)
	}
	for _, query := range []string{"title=%ZZ", "title=a&title=b", "src=https://private.test", "title=%E9", "title=%E2%98%83", "title="} {
		w = httptest.NewRecorder()
		Handle(w, httptest.NewRequest("GET", "http://example.test/api/social?"+query, nil))
		if w.Code != 400 {
			t.Errorf("%s: %d", query, w.Code)
		}
	}
}
