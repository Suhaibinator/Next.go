package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func TestExportRejectsRootLayoutSkewWithoutReplacingPublication(t *testing.T) {
	assets, previous := exportFixture(t)
	output := filepath.Join(t.TempDir(), "published")
	if err := exportApplication(context.Background(), previous, assets, output, "test", []string{"/"}); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(output, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	layout := func(_ *nextgo.Context, body ui.Node) (ui.Node, error) { return body, nil }
	page := func(*nextgo.Context) (nextgo.Page, error) { return nextgo.Page{Body: ui.Text("root")}, nil }
	app, err := nextgo.New(nextgo.Config{BuildID: "test", Routes: []nextgo.Route{{Pattern: "/", Page: page, Layouts: []nextgo.LayoutFunc{layout}, LayoutIDs: []string{"a"}}, {Pattern: "/other", Page: page, Layouts: []nextgo.LayoutFunc{layout}, LayoutIDs: []string{"b"}}}})
	if err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{"", strings.Repeat("c", 64)} {
		t.Run("root="+root, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				r.Header.Set("Accept-Encoding", "identity")
				recorder := httptest.NewRecorder()
				app.ServeHTTP(recorder, r)
				for k, values := range recorder.Header() {
					w.Header()[k] = values
				}
				body := recorder.Body.Bytes()
				if r.Header.Get("X-Nextgo-Navigate") == "1" {
					var envelope nextgo.Envelope
					if err := json.Unmarshal(body, &envelope); err != nil {
						t.Error(err)
					}
					envelope.Root = root
					body, _ = json.Marshal(envelope)
				}
				w.WriteHeader(recorder.Code)
				_, _ = w.Write(body)
			}))
			defer server.Close()
			err := exportApplication(context.Background(), server.URL, assets, output, "test", []string{"/"})
			if err == nil || !strings.Contains(err.Error(), "root layout changed") {
				t.Fatal("mixed-root export accepted", err)
			}
			after, _ := os.ReadFile(filepath.Join(output, "index.html"))
			if string(after) != string(before) {
				t.Fatal("failed root export replaced publication")
			}
		})
	}
}
