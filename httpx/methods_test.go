package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMethodsDispatchOptionsAndSnapshot(t *testing.T) {
	hits := 0
	get := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++; w.WriteHeader(201) })
	table := map[string]http.Handler{"GET": get, "POST": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(202) })}
	handler, err := Methods(table)
	if err != nil {
		t.Fatal(err)
	}
	delete(table, "GET")
	for _, tc := range []struct {
		method string
		code   int
	}{{"GET", 201}, {"HEAD", 201}, {"POST", 202}, {"OPTIONS", 204}, {"DELETE", 405}, {"get", 405}} {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(tc.method, "/", nil))
		if w.Code != tc.code {
			t.Errorf("%s=%d want%d", tc.method, w.Code, tc.code)
		}
		if (tc.code == 204 || tc.code == 405) && w.Header().Get("Allow") != "GET, HEAD, OPTIONS, POST" {
			t.Fatalf("Allow=%s", w.Header().Get("Allow"))
		}
	}
	if hits != 2 {
		t.Fatal("GET/HEAD dispatch lost")
	}
	explicit, _ := Methods(map[string]http.Handler{"HEAD": get, "OPTIONS": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })})
	w := httptest.NewRecorder()
	explicit.ServeHTTP(w, httptest.NewRequest("OPTIONS", "/", nil))
	if w.Code != 200 {
		t.Fatal("explicit OPTIONS ignored")
	}
	for _, table := range []map[string]http.Handler{nil, {"get": get}, {"GET\r\nX": get}, {"GET": nil}, {"": get}} {
		if _, err := Methods(table); err == nil {
			t.Fatalf("accepted invalid table%v", table)
		}
	}
}
