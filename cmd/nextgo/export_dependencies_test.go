package main

import (
	"context"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/ui"
)

func exportDependencySnapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, path)
		files[relative] = string(data)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return files
}

func TestExportDeclaredServerDependenciesPreservePublishedGeneration(t *testing.T) {
	assets, base := exportFixture(t)
	out := filepath.Join(t.TempDir(), "published")
	if err := exportApplication(context.Background(), base, assets, out, "test", []string{"/"}); err != nil {
		t.Fatal(err)
	}
	previous := exportDependencySnapshot(t, out)
	for _, mode := range []string{"html", "navigation"} {
		t.Run(mode, func(t *testing.T) {
			app, err := nextgo.New(nextgo.Config{BuildID: "test", Routes: []nextgo.Route{
				{Pattern: "/about", Page: func(*nextgo.Context) (nextgo.Page, error) {
					return nextgo.Page{Body: ui.Text("would stage a new page")}, nil
				}},
				{Pattern: "/dependent", Page: func(c *nextgo.Context) (nextgo.Page, error) {
					if mode == "html" || c.Request.Header.Get("X-Nextgo-Navigate") == "1" {
						if err := c.RequireServer(); err != nil {
							return nextgo.Page{}, err
						}
					}
					return nextgo.Page{Body: ui.El("a", ui.Attrs{"href": "/api/unpublished"}, ui.Text("runtime dependency"))}, nil
				}},
			}})
			if err != nil {
				t.Fatal(err)
			}
			defer app.Close(context.Background())
			server := httptest.NewServer(app)
			defer server.Close()
			err = exportApplication(context.Background(), server.URL, assets, out, "test", []string{"/about", "/dependent"})
			if err == nil || !strings.Contains(err.Error(), "runtime dependencies that require a server") {
				t.Fatal("declared server dependency was published", err)
			}
			if after := exportDependencySnapshot(t, out); !reflect.DeepEqual(after, previous) {
				t.Fatal("rejected dependency changed previously published files")
			}
		})
	}
}

func TestExportServerDependencyRejectsBeforeReadingHTMLOrNavigationBody(t *testing.T) {
	for _, nav := range []bool{false, true} {
		t.Run(map[bool]string{false: "html", true: "navigation"}[nav], func(t *testing.T) {
			closed := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set(nextgo.RequiresServerHeader, "1")
				w.Header().Set("Content-Type", "text/html")
				w.WriteHeader(http.StatusOK)
				w.(http.Flusher).Flush()
				<-r.Context().Done()
				close(closed)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_, err := exportResponse(ctx, server.Client(), server.URL, nav, "")
			if err == nil || !strings.Contains(err.Error(), "runtime dependencies that require a server") || ctx.Err() != nil {
				t.Fatal("export waited for a runtime-dependent body", err, ctx.Err())
			}
			select {
			case <-closed:
			case <-ctx.Done():
				t.Fatal("dependency rejection retained the response body")
			}
		})
	}
}
