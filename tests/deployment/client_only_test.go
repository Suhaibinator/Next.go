//go:build deployment

package deployment

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/Suhaibinator/Next.go/internal/navigation"
)

// Use the same actual-process HTTP/Runtime API controllers as the cookie
// campaign. Nothing here substitutes an in-process app for the compiled demo.
func TestClientOnlyExecutable(t *testing.T) {
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
				path, about := scope.prefix()+"/client-only", scope.prefix()+"/about"
				if scope.slash {
					path += "/"
					about += "/"
				}
				for _, query := range []string{"", "?fail=error", "?fail=panic"} {
					for _, representation := range []string{"html", "head", "navigation"} {
						t.Run(representation+query, func(t *testing.T) {
							event := actionViewEvent{method: "GET", path: path + query, headers: make(http.Header)}
							if representation == "head" {
								event.method = "HEAD"
							} else if representation == "navigation" {
								event.headers.Set("X-Nextgo-Navigate", "1")
							}
							reply := worker.send(event)
							if reply.status != 200 {
								t.Fatalf("client boundary status %d: %s", reply.status, reply.data)
							}
							if representation == "head" {
								if len(reply.data) != 0 {
									t.Fatal("HEAD published a boundary body")
								}
								return
							}
							payload := reply.data
							if representation == "navigation" {
								var page struct {
									Version, Status  int
									HTML, Title, URL string
								}
								if err := json.Unmarshal(payload, &page); err != nil {
									t.Fatal(err)
								}
								if page.Version != navigation.ProtocolVersion || page.Status != 200 || page.URL != path+query || page.Title != "Explicit client-only views — Next.go" {
									t.Fatalf("client boundary navigation contract: %+v", page)
								}
								payload = []byte(page.HTML)
							}
							for marker, count := range map[string]int{
								`data-nextgo-client="1"`:               4,
								`data-nextgo-island="clientpanel"`:     2,
								`data-nextgo-island="clientpanellazy"`: 2,
								`data-nextgo-island="counter"`:         1,
								`data-client-panel-fallback=""`:        4,
								`data-client-panel-draft=""`:           4,
								`data-client-panel=""`:                 0,
								`data-count="">2</output>`:             1,
								`data-client-only-exit=""`:             1,
							} {
								if got := bytes.Count(payload, []byte(marker)); got != count {
									t.Fatalf("%q count %d, want %d", marker, got, count)
								}
							}
							if !bytes.Contains(payload, []byte(`href="`+about+`"`)) {
								t.Fatal("client boundary exit lost its mounted/locale/slash path")
							}
						})
					}
				}
			})
		}
	}
}
