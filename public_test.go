package nextgo

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Suhaibinator/Next.go/ui"
)

func TestPublicRuntimeSnapshot(t *testing.T) {
	values := map[string]string{"message": "<script>&\"public"}
	app, err := New(Config{Public: values, Routes: []Route{{Pattern: "/", Page: func(c *Context) (Page, error) {
		value, ok := PublicValue(c.Context, "message")
		if !ok {
			t.Fatal("missing request settings")
		}
		return Page{Body: ui.Text(value)}, nil
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	values["message"] = "mutated"
	w := httptest.NewRecorder()
	app.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	body := w.Body.String()
	if w.Code != 200 || strings.Contains(body, "mutated") || strings.Contains(body, "<script>&") {
		t.Fatalf("invalid snapshot/escaping: %d", w.Code)
	}
	if !strings.Contains(body, "public") || !strings.Contains(body, "data-nextgo-navigation") {
		t.Fatal("missing public manifest")
	}
	other, err := New(Config{Public: map[string]string{"message": "changed"}})
	if err != nil {
		t.Fatal(err)
	}
	if app.configurationID == other.configurationID {
		t.Fatal("public deployment settings omitted from configuration identity")
	}
}
func TestPublicAvailableToMiddleware(t *testing.T) {
	app, err := New(Config{Public: map[string]string{"key": "value"}, Middleware: []func(http.Handler) http.Handler{func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if value, ok := PublicValue(r.Context(), "key"); !ok || value != "value" {
				t.Fatal("missing middleware settings")
			}
			next.ServeHTTP(w, r)
		})
	}}})
	if err != nil {
		t.Fatal(err)
	}
	app.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/", nil))
}
func TestPublicValidation(t *testing.T) {
	for _, values := range []map[string]string{{"": "x"}, {"secret": strings.Repeat("private-value", 100)}, {"invalid\x00": "x"}} {
		_, err := New(Config{Public: values})
		if err == nil || strings.Contains(err.Error(), "private-value") {
			t.Fatal("invalid setting or error disclosure")
		}
	}
}

func TestEnvironmentAssetsNeverServed(t *testing.T) {
	app, err := New(Config{Assets: fstest.MapFS{"public/.env": {Data: []byte("synthetic-secret")}, "public/nested/.env.local": {Data: []byte("synthetic-secret")}, "public/.ENV": {Data: []byte("synthetic-secret")}, "public/nested/.Env.Local": {Data: []byte("synthetic-secret")}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/assets/.env", "/assets/%2eenv", "/assets/nested/.env.local", "/assets/.ENV", "/assets/nested/.Env.Local"} {
		w := httptest.NewRecorder()
		app.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 || strings.Contains(w.Body.String(), "synthetic-secret") {
			t.Fatal("environment asset exposed", path, w.Code)
		}
	}
}
