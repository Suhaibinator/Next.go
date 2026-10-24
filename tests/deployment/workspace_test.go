//go:build deployment

package deployment

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func workspaceExecutable(t *testing.T) {
	t.Helper()
	binary := os.Getenv("NEXTGO_TEST_WORKSPACE_BINARY")
	if binary == "" {
		t.Skip("set NEXTGO_TEST_WORKSPACE_BINARY to a built workspace sample")
	}
	t.Setenv("NEXTGO_TEST_BINARY", binary)
}
func workspacePayload(t *testing.T, status int, payload []byte) {
	t.Helper()
	if status != 200 || !bytes.Contains(payload, []byte("Workspace counter")) || !bytes.Contains(payload, []byte("This text was embedded from a sibling Go module.")) {
		t.Error("workspace executable SSR contract failed", status)
	}
}
func TestWorkspaceHTTPExecutable(t *testing.T) {
	workspaceExecutable(t)
	addr := address(t)
	p := start(t, nil, "-addr", addr)
	client := &http.Client{Timeout: 3 * time.Second}
	for until := time.Now().Add(5 * time.Second); ; {
		response, err := client.Get("http://" + addr + "/")
		if err == nil {
			workspacePayload(t, response.StatusCode, body(t, response))
			break
		}
		if time.Now().After(until) {
			t.Fatal(err, p.logs.String())
		}
		time.Sleep(25 * time.Millisecond)
	}
	p.stop(t)
}
func TestWorkspaceLambdaExecutable(t *testing.T) {
	workspaceExecutable(t)
	for _, mode := range []string{"buffered", "streaming"} {
		t.Run(mode, func(t *testing.T) {
			var issued, reports atomic.Int32
			done := make(chan struct{})
			runtime := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "/next") {
					if issued.Add(1) > 1 {
						<-r.Context().Done()
						return
					}
					w.Header().Set("Lambda-Runtime-Aws-Request-Id", "workspace")
					w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(5*time.Second).UnixMilli(), 10))
					json.NewEncoder(w).Encode(map[string]any{"version": "2.0", "rawPath": "/", "headers": map[string]string{"host": "workspace.test"}, "requestContext": map[string]any{"domainName": "workspace.test", "http": map[string]string{"method": "GET", "path": "/", "sourceIp": "127.0.0.1"}}})
					return
				}
				data, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
				if err != nil {
					t.Error(err)
				}
				var response struct {
					StatusCode int    `json:"statusCode"`
					Body       string `json:"body"`
				}
				var payload []byte
				if mode == "buffered" {
					if err := json.Unmarshal(data, &response); err != nil {
						t.Error(err)
					}
					payload = []byte(response.Body)
				} else {
					split := bytes.Index(data, make([]byte, 8))
					if split < 0 {
						t.Error("missing streaming delimiter")
					} else {
						if err := json.Unmarshal(data[:split], &response); err != nil {
							t.Error(err)
						}
						payload = data[split+8:]
					}
				}
				workspacePayload(t, response.StatusCode, payload)
				if reports.Add(1) == 1 {
					close(done)
				}
				w.WriteHeader(202)
			}))
			defer runtime.Close()
			p := start(t, []string{"AWS_LAMBDA_RUNTIME_API=" + strings.TrimPrefix(runtime.URL, "http://"), "NEXTGO_LAMBDA_RESPONSE_MODE=" + mode})
			select {
			case <-done:
			case <-time.After(8 * time.Second):
				t.Fatal("workspace Lambda timeout", p.logs.String())
			}
			p.stop(t)
		})
	}
}
