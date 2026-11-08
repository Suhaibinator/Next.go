//go:build deployment

package deployment

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"strings"
	"testing"

	nextgo "github.com/Suhaibinator/Next.go"
	"github.com/Suhaibinator/Next.go/internal/navigation"
)

func TestRootLayoutsExecutable(t *testing.T) {
	binary := os.Getenv("NEXTGO_TEST_ROOTS_BINARY")
	if binary == "" {
		t.Skip("set NEXTGO_TEST_ROOTS_BINARY to the built multi-root example")
	}
	t.Setenv("NEXTGO_TEST_BINARY", binary)
	for _, mode := range []string{"http", "buffered", "streaming"} {
		for _, base := range []string{"", "/scoped"} {
			t.Run(mode+base, func(t *testing.T) {
				extra := []string{"NEXTGO_METADATA_BASE_PATH=" + base, "NEXTGO_METADATA_ASSET_PREFIX=/independent-assets"}
				var worker *sessionExecutable
				if mode == "http" {
					worker = newSocialMetadataHTTP(t, extra, base+"/api/health")
				} else {
					worker = newSessionLambda(t, mode, generatedActionScope{}, extra)
				}
				defer worker.close()
				roots := map[string]string{}
				for _, path := range []string{"/", "/guide", "/dashboard", "/dashboard/settings", "/dashboard/stream", "/masked?other=1", "/plain"} {
					t.Run(strings.ReplaceAll(path, "/", "_"), func(t *testing.T) {
						publicPath := base + path
						if base != "" && path == "/" {
							publicPath = base
						}
						response := worker.send(actionViewEvent{method: "GET", path: publicPath, headers: make(http.Header)})
						start := bytes.Index(response.data, []byte("<body "))
						if response.status != 200 || start < 0 {
							t.Fatal(path, response.status, string(response.data))
						}
						end := bytes.IndexByte(response.data[start:], '>')
						if end < 0 {
							t.Fatal("body not closed")
						}
						root := viewportAttribute(string(response.data[start:start+end]), "data-nextgo-root")
						if !navigation.ValidConfigurationID(root) {
							t.Fatal("missing root", path)
						}
						roots[path] = root
						response = worker.send(actionViewEvent{method: "GET", path: publicPath, headers: http.Header{"X-Nextgo-Navigate": {"1"}, navigation.StreamHeader: {"1"}}})
						data := response.data
						if strings.Contains(response.headers.Get("Content-Type"), navigation.StreamMediaType) {
							line, _, _ := bytes.Cut(data, []byte("\n"))
							frame, err := navigation.DecodeStreamFrame(line)
							if err != nil {
								t.Fatal(err)
							}
							data = frame.Page
						}
						var envelope nextgo.Envelope
						if response.status != 200 || json.Unmarshal(data, &envelope) != nil || envelope.Root != root {
							t.Fatal("document/navigation root mismatch", path, response.status, string(data))
						}
					})
				}
				if roots["/"] != roots["/guide"] || roots["/dashboard"] != roots["/dashboard/settings"] || roots["/dashboard"] != roots["/dashboard/stream"] || roots["/dashboard"] != roots["/masked?other=1"] || roots["/"] == roots["/dashboard"] || roots["/plain"] == roots["/"] || roots["/plain"] == roots["/dashboard"] {
					t.Fatal("root ownership", roots)
				}
			})
		}
	}
}
