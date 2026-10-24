//go:build deployment

package deployment

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func environmentDir(t *testing.T) string {
	t.Helper()
	if dir := os.Getenv("NEXTGO_TEST_ENV_DIR"); dir != "" {
		return dir
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".env.production"), []byte("NEXTGO_DEMO_PUBLIC_MESSAGE=Production file setting\nSERVER_ONLY=synthetic-private-environment-728\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestEnvironmentHTTPExecutable(t *testing.T) {
	dir := environmentDir(t)
	for _, ambient := range []bool{false, true} {
		t.Run(strconv.FormatBool(ambient), func(t *testing.T) {
			addr := address(t)
			env := []string{"AWS_LAMBDA_RUNTIME_API="}
			expected := "Production file setting"
			if ambient {
				env = append(env, "NEXTGO_DEMO_PUBLIC_MESSAGE=Ambient override")
				expected = "Ambient override"
			}
			p := start(t, env, "-addr", addr, "-env-dir", dir)
			client := &http.Client{Timeout: 3 * time.Second}
			var data []byte
			for until := time.Now().Add(5 * time.Second); ; {
				response, err := client.Get("http://" + addr + "/environment")
				if err == nil {
					data = body(t, response)
					if response.StatusCode != 200 {
						t.Fatal(response.StatusCode)
					}
					break
				}
				if time.Now().After(until) {
					t.Fatal(err, p.logs.String())
				}
				time.Sleep(25 * time.Millisecond)
			}
			if !bytes.Contains(data, []byte(expected)) || bytes.Contains(data, []byte("synthetic-private-environment-728")) {
				t.Fatal("runtime settings or secret boundary failed")
			}
			version := versionRE.FindSubmatch(data)
			if len(version) != 2 {
				t.Fatal("missing runtime")
			}
			response, err := client.Get("http://" + addr + "/_nextgo/" + string(version[1]) + "/app.wasm")
			if err != nil {
				t.Fatal(err)
			}
			wasm := body(t, response)
			if bytes.Contains(wasm, []byte("Production file setting")) || bytes.Contains(wasm, []byte("synthetic-private-environment-728")) {
				t.Fatal("runtime environment entered WASM")
			}
			p.stop(t)
		})
	}
}

func TestEnvironmentLambdaExecutable(t *testing.T) {
	dir := environmentDir(t)
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
					w.Header().Set("Lambda-Runtime-Aws-Request-Id", "environment")
					w.Header().Set("Lambda-Runtime-Deadline-Ms", strconv.FormatInt(time.Now().Add(5000*time.Millisecond).UnixMilli(), 10))
					json.NewEncoder(w).Encode(map[string]any{"version": "2.0", "rawPath": "/environment", "headers": map[string]string{"host": "demo.test"}, "requestContext": map[string]any{"domainName": "demo.test", "http": map[string]string{"method": "GET", "path": "/environment", "sourceIp": "127.0.0.1"}}})
					return
				}
				raw, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
				if err != nil {
					t.Error(err)
				}
				var response struct {
					StatusCode int    `json:"statusCode"`
					Body       string `json:"body"`
				}
				var payload []byte
				if mode == "buffered" {
					if err := json.Unmarshal(raw, &response); err != nil {
						t.Error(err)
					}
					payload = []byte(response.Body)
				} else {
					split := bytes.Index(raw, make([]byte, 8))
					if split < 0 {
						t.Error("stream delimiter missing")
					} else {
						if err := json.Unmarshal(raw[:split], &response); err != nil {
							t.Error(err)
						}
						payload = raw[split+8:]
					}
				}
				if response.StatusCode != 200 || !bytes.Contains(payload, []byte("Production file setting")) || bytes.Contains(payload, []byte("synthetic-private-environment-728")) {
					t.Error("Lambda environment boundary failed", response.StatusCode)
				}
				if reports.Add(1) == 1 {
					close(done)
				}
				w.WriteHeader(202)
			}))
			defer runtime.Close()
			p := start(t, []string{"AWS_LAMBDA_RUNTIME_API=" + strings.TrimPrefix(runtime.URL, "http://"), "NEXTGO_LAMBDA_RESPONSE_MODE=" + mode}, "-env-dir", dir)
			select {
			case <-done:
			case <-time.After(8 * time.Second):
				t.Fatal("Lambda environment timeout", p.logs.String())
			}
			p.stop(t)
		})
	}
}
