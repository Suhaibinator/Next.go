//go:build deployment

package deployment

import (
	"bytes"
	"net/http"
	"strings"
	"testing"
)

func TestPrivateRoutesExecutable(t *testing.T) {
	for _, mode := range []string{"http", "buffered", "streaming"} {
		for _, scope := range []generatedActionScope{{}, {base: "/scoped", locale: "fr", slash: true}} {
			t.Run(mode+scope.prefix(), func(t *testing.T) {
				var worker *sessionExecutable
				if mode == "http" {
					worker = newSessionHTTP(t, scope, nil)
				} else {
					worker = newSessionLambda(t, mode, scope, nil)
				}
				defer worker.close()
				for _, path := range []string{"/routing-private", "/_public", "/private_helpers", "/private_helpers/data.go", "/private_helpers/slot_ignored", "/_native", "/_native/data.go"} {
					t.Run(strings.ReplaceAll(path, "/", "_"), func(t *testing.T) {
						url := scope.prefix() + path
						if scope.slash && !strings.Contains(path, ".") && path != "/_public" {
							url += "/"
						}
						response := worker.send(actionViewEvent{method: "GET", path: url, headers: make(http.Header)})
						if path == "/routing-private" {
							if response.status != 200 || !bytes.Contains(response.data, []byte("Imported from private_helpers")) || !bytes.Contains(response.data, []byte("Imported from _native")) {
								t.Fatal("native private imports did not render", response.status, string(response.data))
							}
						} else if path == "/_public" {
							if scope.slash { // A document underscore route still follows document slash policy.
								response = worker.send(actionViewEvent{method: "GET", path: scope.prefix() + path + "/", headers: make(http.Header)})
							}
							if response.status != 200 || !bytes.Contains(response.data, []byte("intentional underscore URL")) {
								t.Fatal("literal escape not routed", response.status)
							}
						} else if response.status != 404 {
							t.Fatal("private route/source path became public", path, response.status)
						}
					})
				}
			})
		}
	}
}
